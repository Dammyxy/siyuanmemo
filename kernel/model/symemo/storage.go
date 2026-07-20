// SiYuan - Refactor your thinking
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package symemo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/siyuan-note/filelock"
)

type Config struct {
	StorageRoot   string
	IndexRoot     string
	SchedulerRoot string
	Now           func() time.Time
	Location      *time.Location
}

func (c Config) withDefaults() Config {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Location == nil {
		c.Location = time.Local
	}
	if c.SchedulerRoot == "" && c.StorageRoot != "" {
		c.SchedulerRoot = filepath.Join(c.StorageRoot, "scheduler")
	}
	return c
}

func (c Config) ElementsRoot() string { return filepath.Join(c.StorageRoot, "elements") }
func (c Config) ReviewsRoot() string  { return filepath.Join(c.StorageRoot, "reviews") }
func (c Config) IndexPath() string    { return filepath.Join(c.IndexRoot, "memo.db") }

type SchedulerConfig struct {
	Spec                int       `json:"spec"`
	Algorithm           string    `json:"algorithm,omitempty"`
	Primary             string    `json:"primary,omitempty"`
	EnabledAlgorithms   []string  `json:"enabledAlgorithms,omitempty"`
	Fallback            string    `json:"fallback,omitempty"`
	IntervalRule        string    `json:"intervalRule,omitempty"`
	RequestRetention    float64   `json:"requestRetention,omitempty"`
	MaximumIntervalDays int       `json:"maximumIntervalDays,omitempty"`
	Weights             []float64 `json:"weights,omitempty"`
	EnableShortTerm     bool      `json:"enableShortTerm,omitempty"`
	EnableFuzz          bool      `json:"enableFuzz,omitempty"`
}

func (c Config) LoadSchedulerConfig(name string) (SchedulerConfig, error) {
	path := filepath.Join(c.SchedulerRoot, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return SchedulerConfig{}, err
	}
	var cfg SchedulerConfig
	if err = json.Unmarshal(data, &cfg); err != nil {
		return SchedulerConfig{}, fmt.Errorf("decode scheduler config %s: %w", name, err)
	}
	if cfg.Spec != SupportedConfigSpec {
		return SchedulerConfig{}, fmt.Errorf("unsupported scheduler config spec %d", cfg.Spec)
	}
	return cfg, nil
}

func (c Config) LoadTracerSchedulerConfig() (SchedulerConfig, error) {
	collection, err := c.LoadSchedulerConfig("collection")
	if err != nil {
		return SchedulerConfig{}, err
	}
	simple, err := c.LoadSchedulerConfig(simpleV1ID)
	if err != nil {
		return SchedulerConfig{}, err
	}
	fsrsConfig, err := c.LoadSchedulerConfig(fsrsV1ID)
	if err != nil {
		return SchedulerConfig{}, err
	}
	arena, err := c.LoadSchedulerConfig("arena-v1")
	if err != nil {
		return SchedulerConfig{}, err
	}
	if collection.Primary != fsrsV1ID || collection.Fallback != simpleV1ID || simple.Algorithm != simpleV1ID || fsrsConfig.Algorithm != fsrsV1ID || arena.Primary != fsrsV1ID || arena.Fallback != simpleV1ID {
		return SchedulerConfig{}, errors.New("item-learning-core scheduler configuration is incompatible")
	}
	if len(fsrsConfig.Weights) != 19 || fsrsConfig.RequestRetention <= 0 || fsrsConfig.RequestRetention >= 1 || fsrsConfig.MaximumIntervalDays <= 0 || fsrsConfig.EnableFuzz {
		return SchedulerConfig{}, errors.New("fsrs-v1 scheduler configuration is invalid")
	}
	return fsrsConfig, nil
}

func (c Config) ensureTracerSchedulerConfig() error {
	if err := os.MkdirAll(c.SchedulerRoot, 0755); err != nil {
		return err
	}
	configs := map[string]SchedulerConfig{
		"collection": {
			Spec:              SupportedConfigSpec,
			Primary:           fsrsV1ID,
			EnabledAlgorithms: []string{fsrsV1ID, simpleV1ID},
			Fallback:          simpleV1ID,
		},
		"simple-v1": {
			Spec:         SupportedConfigSpec,
			Algorithm:    simpleV1ID,
			IntervalRule: "item-simple-v1",
		},
		"fsrs-v1": defaultFSRSV1SchedulerConfig(),
		"arena-v1": {
			Spec:              SupportedConfigSpec,
			Primary:           fsrsV1ID,
			EnabledAlgorithms: []string{fsrsV1ID, simpleV1ID},
			Fallback:          simpleV1ID,
		},
	}
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(c.SchedulerRoot, name+".json")
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		data, err := json.MarshalIndent(configs[name], "", "  ")
		if err != nil {
			return err
		}
		if err = filelock.WriteFile(path, append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) LoadElement(id string) (Element, error) {
	var element Element
	if id == "" {
		return element, errors.New("empty element id")
	}
	path, err := c.findElementPath(id)
	if err != nil {
		return element, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return element, err
	}
	if err = json.Unmarshal(data, &element); err != nil {
		return element, fmt.Errorf("decode element %s: %w", id, err)
	}
	if err = validateElement(element, filepath.Base(path)); err != nil {
		return element, unavailableElementSource(err)
	}
	return element, nil
}

func (c Config) LoadElements() (map[string]Element, error) {
	scan, err := c.scanElements()
	return scan.Elements, err
}

type elementScanResult struct {
	Elements    map[string]Element
	Diagnostics []ElementSourceDiagnostic
}

const (
	sourceUnreadableCode = "unreadable-element-source"
	sourceMalformedCode  = "malformed-element-source"
	sourceMissingCode    = "missing-element-source"
	sourceSpecCode       = "unsupported-element-spec"
	sourceIdentityCode   = "element-id-mismatch"
	sourcePayloadCode    = "unsupported-element-payload"
	sourceIncompleteCode = "incomplete-element-source"
)

func (c Config) scanElements() (elementScanResult, error) {
	result := elementScanResult{Elements: map[string]Element{}}
	root := c.ElementsRoot()
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return result, nil
	} else if err != nil {
		return result, err
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".sme" {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		sourcePath := filepath.ToSlash(filepath.Clean(relative))
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, Code: sourceUnreadableCode, Reason: "Element source could not be read."})
			return nil
		}
		var element Element
		if decodeErr := json.Unmarshal(data, &element); decodeErr != nil {
			result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, Code: sourceMalformedCode, Reason: "Element source is malformed."})
			return nil
		}
		if code, reason, validateErr := elementSourceIssue(element, filepath.Base(path)); validateErr != nil {
			result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, ElementID: trustworthyElementID(element, filepath.Base(path)), Code: code, Reason: reason})
			return nil
		}
		result.Elements[element.ID] = element
		return nil
	})
	if err != nil {
		return result, err
	}
	sort.Slice(result.Diagnostics, func(i, j int) bool { return result.Diagnostics[i].SourcePath < result.Diagnostics[j].SourcePath })
	return result, nil
}

func (c Config) LoadEventFiles() ([]SchedulingEvent, error) {
	root := c.ReviewsRoot()
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var events []SchedulingEvent
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".smr" {
			continue
		}
		fileMonth := strings.TrimSuffix(entry.Name(), ".smr")
		if _, parseErr := time.Parse("2006-01", fileMonth); parseErr != nil {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			cause := fmt.Errorf("review history path is not a file: %s", path)
			return nil, domainError(ErrHistoryRequiresRepair, "review history cannot be read", cause)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, domainError(ErrHistoryRequiresRepair, "review history cannot be read", readErr)
		}
		var file EventFile
		if unmarshalErr := json.Unmarshal(data, &file); unmarshalErr != nil {
			return nil, domainError(ErrHistoryRequiresRepair, "review history is malformed", unmarshalErr)
		}
		if file.Spec != SupportedEventSpec {
			return nil, domainError(ErrHistoryRequiresRepair, fmt.Sprintf("unsupported review history spec %d", file.Spec), nil)
		}
		if file.Month != fileMonth {
			return nil, domainError(ErrHistoryRequiresRepair, "review history month does not match its filename", nil)
		}
		events = append(events, file.Events...)
	}
	return events, nil
}

func (c Config) findElementPath(id string) (string, error) {
	root := c.ElementsRoot()
	var found string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Ext(path) == ".sme" && strings.TrimSuffix(entry.Name(), ".sme") == id {
			found = path
			return fs.SkipDir
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipDir) {
		return "", err
	}
	if found == "" {
		return "", os.ErrNotExist
	}
	return found, nil
}

func validateElement(element Element, filename string) error {
	_, _, err := elementSourceIssue(element, filename)
	return err
}

func elementSourceIssue(element Element, filename string) (code, reason string, err error) {
	if element.Spec != SupportedElementSpec {
		return sourceSpecCode, "Element source uses an unsupported format.", fmt.Errorf("unsupported element spec %d", element.Spec)
	}
	if element.ID == "" || strings.TrimSuffix(filename, ".sme") != element.ID {
		return sourceIdentityCode, "Element identity does not match its source path.", errors.New("element id does not match source filename")
	}
	if element.Type != "item" || element.PayloadSpec != SupportedPayloadSpec || element.Payload.Kind != "qa" {
		return sourcePayloadCode, "Element source is not a supported Q/A Item.", ErrUnavailableSource
	}
	if element.ProcessingState == "" || element.Payload.Prompt == "" || element.Payload.Answer == "" {
		return sourceIncompleteCode, "Element source is incomplete.", ErrUnavailableSource
	}
	return "", "", nil
}

func trustworthyElementID(element Element, filename string) string {
	if element.ID != "" && strings.TrimSuffix(filename, ".sme") == element.ID {
		return element.ID
	}
	return ""
}

func sourceDiagnosticsWithMissingProjections(scan elementScanResult, projections map[string]SchedulingProjection) []ElementSourceDiagnostic {
	diagnostics := append([]ElementSourceDiagnostic(nil), scan.Diagnostics...)
	knownPaths := make(map[string]bool, len(diagnostics))
	for _, diagnostic := range diagnostics {
		knownPaths[diagnostic.SourcePath] = true
	}
	for elementID := range projections {
		if _, found := scan.Elements[elementID]; found {
			continue
		}
		sourcePath := filepath.ToSlash(elementID + ".sme")
		if knownPaths[sourcePath] {
			continue
		}
		diagnostics = append(diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, ElementID: elementID, Code: sourceMissingCode, Reason: "Element source is missing."})
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].SourcePath < diagnostics[j].SourcePath })
	return diagnostics
}

var ErrUnavailableSource = errors.New("authoritative element source unavailable")

func unavailableElementSource(cause error) error {
	return domainError(ErrAuthoritativeItemUnavailable, "authoritative Item source is unavailable", cause)
}

func monthFor(t time.Time) string { return t.Format("2006-01") }

func canonicalHash(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
