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

	"github.com/88250/lute/ast"
	"github.com/siyuan-note/filelock"
)

type Config struct {
	StorageRoot   string
	IndexRoot     string
	SchedulerRoot string
	Now           func() time.Time
	Location      *time.Location
	ReadOnly      bool
	BlockReader   BlockReferenceReader
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
	if c.BlockReader == nil {
		c.BlockReader = unresolvedBlockReferenceReader{}
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
	AFactor             float64   `json:"aFactor,omitempty"`
	MinimumIntervalDays int       `json:"minimumIntervalDays,omitempty"`
	RequestRetention    float64   `json:"requestRetention,omitempty"`
	MaximumIntervalDays int       `json:"maximumIntervalDays,omitempty"`
	SkipPolicy          string    `json:"skipPolicy,omitempty"`
	Weights             []float64 `json:"weights,omitempty"`
	EnableShortTerm     bool      `json:"enableShortTerm,omitempty"`
	EnableFuzz          bool      `json:"enableFuzz,omitempty"`
}

func (c Config) LoadSchedulerConfig(name string) (SchedulerConfig, error) {
	path := filepath.Join(c.SchedulerRoot, name+".json")
	data, err := filelock.ReadFile(path)
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

type LearningDayConfigV1 struct {
	Spec               int    `json:"spec"`
	TimeZoneIANA       string `json:"timeZoneIana"`
	MidnightShiftHours int    `json:"midnightShiftHours"`
}

type EffectiveSchedulerConfig struct {
	FSRS                SchedulerConfig
	Topic               SchedulerConfig
	LearningDay         LearningDayConfigV1
	LearningDayLocation *time.Location
	PersistedComplete   bool
	Diagnostics         []ElementSourceDiagnostic
}

func defaultSchedulerConfigs() map[string]SchedulerConfig {
	return map[string]SchedulerConfig{
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
		"topic-afactor-v1": {
			Spec:                SupportedConfigSpec,
			Algorithm:           topicAFactorV1ID,
			AFactor:             2.5,
			MinimumIntervalDays: 1,
			MaximumIntervalDays: 36500,
			SkipPolicy:          "none",
		},
		"arena-v1": {
			Spec:              SupportedConfigSpec,
			Primary:           fsrsV1ID,
			EnabledAlgorithms: []string{fsrsV1ID, simpleV1ID},
			Fallback:          simpleV1ID,
		},
	}
}

func schedulerConfigNames() []string {
	return []string{"collection", "simple-v1", "fsrs-v1", "topic-afactor-v1", "arena-v1"}
}

func defaultLearningDayConfig() LearningDayConfigV1 {
	return LearningDayConfigV1{Spec: SupportedConfigSpec, TimeZoneIANA: "UTC", MidnightShiftHours: 4}
}

func (c Config) LoadLearningDayConfig() (LearningDayConfigV1, error) {
	path := filepath.Join(c.SchedulerRoot, "learning-day.json")
	data, err := filelock.ReadFile(path)
	if err != nil {
		return LearningDayConfigV1{}, err
	}
	var cfg LearningDayConfigV1
	if err = json.Unmarshal(data, &cfg); err != nil {
		return LearningDayConfigV1{}, fmt.Errorf("decode learning day config: %w", err)
	}
	if cfg.Spec != SupportedConfigSpec {
		return LearningDayConfigV1{}, fmt.Errorf("unsupported learning day config spec %d", cfg.Spec)
	}
	return cfg, nil
}

func (c Config) LoadEffectiveSchedulerConfig() EffectiveSchedulerConfig {
	defaults := defaultSchedulerConfigs()
	builtins := defaultSchedulerConfigs()
	learningDayConfig := defaultLearningDayConfig()
	learningDayLocation, _ := time.LoadLocation(learningDayConfig.TimeZoneIANA)
	effective := EffectiveSchedulerConfig{FSRS: defaults["fsrs-v1"], Topic: defaults["topic-afactor-v1"], LearningDay: learningDayConfig, LearningDayLocation: learningDayLocation, PersistedComplete: true}
	for _, name := range schedulerConfigNames() {
		config, err := c.LoadSchedulerConfig(name)
		if err != nil {
			effective.PersistedComplete = false
			code := "invalid-scheduler-config"
			reason := "Scheduler configuration is invalid."
			if errors.Is(err, os.ErrNotExist) {
				code = "missing-scheduler-config"
				reason = "Scheduler configuration is missing."
			}
			effective.Diagnostics = append(effective.Diagnostics, ElementSourceDiagnostic{
				SourcePath: filepath.ToSlash(filepath.Join("scheduler", name+".json")),
				Code:       code,
				Reason:     reason,
			})
			continue
		}
		defaults[name] = config
	}
	learningDay, learningDayErr := c.LoadLearningDayConfig()
	if learningDayErr != nil {
		effective.PersistedComplete = false
		code := "invalid-scheduler-config"
		reason := "Learning Day configuration is invalid."
		if errors.Is(learningDayErr, os.ErrNotExist) {
			code = "missing-scheduler-config"
			reason = "Learning Day configuration is missing."
		}
		effective.Diagnostics = append(effective.Diagnostics, ElementSourceDiagnostic{
			SourcePath: filepath.ToSlash(filepath.Join("scheduler", "learning-day.json")),
			Code:       code,
			Reason:     reason,
		})
	} else {
		learningDayConfig = learningDay
	}
	learningDayLocation, locationErr := time.LoadLocation(learningDayConfig.TimeZoneIANA)
	if locationErr != nil || learningDayConfig.MidnightShiftHours < 0 || learningDayConfig.MidnightShiftHours > 16 {
		effective.PersistedComplete = false
		effective.Diagnostics = append(effective.Diagnostics, ElementSourceDiagnostic{SourcePath: filepath.ToSlash(filepath.Join("scheduler", "learning-day.json")), Code: "invalid-scheduler-config", Reason: "Learning Day configuration is invalid."})
		learningDayConfig = defaultLearningDayConfig()
		learningDayLocation, _ = time.LoadLocation(learningDayConfig.TimeZoneIANA)
	}
	if defaults["collection"].Primary != fsrsV1ID || defaults["collection"].Fallback != simpleV1ID {
		effective.PersistedComplete = false
		effective.Diagnostics = append(effective.Diagnostics, ElementSourceDiagnostic{SourcePath: filepath.ToSlash(filepath.Join("scheduler", "collection.json")), Code: "invalid-scheduler-config", Reason: "Scheduler configuration is invalid."})
	}
	if defaults["simple-v1"].Algorithm != simpleV1ID {
		effective.PersistedComplete = false
		effective.Diagnostics = append(effective.Diagnostics, ElementSourceDiagnostic{SourcePath: filepath.ToSlash(filepath.Join("scheduler", "simple-v1.json")), Code: "invalid-scheduler-config", Reason: "Scheduler configuration is invalid."})
	}
	fsrsConfig := defaults["fsrs-v1"]
	if fsrsConfig.Algorithm != fsrsV1ID || len(fsrsConfig.Weights) != 19 || fsrsConfig.RequestRetention <= 0 || fsrsConfig.RequestRetention >= 1 || fsrsConfig.MaximumIntervalDays <= 0 || fsrsConfig.EnableFuzz {
		effective.PersistedComplete = false
		effective.Diagnostics = append(effective.Diagnostics, ElementSourceDiagnostic{SourcePath: filepath.ToSlash(filepath.Join("scheduler", "fsrs-v1.json")), Code: "invalid-scheduler-config", Reason: "Scheduler configuration is invalid."})
		fsrsConfig = defaultFSRSV1SchedulerConfig()
	}
	topicConfig := defaults["topic-afactor-v1"]
	if topicConfig.Algorithm != topicAFactorV1ID || topicConfig.AFactor <= 0 || topicConfig.MinimumIntervalDays < 1 || topicConfig.MaximumIntervalDays < topicConfig.MinimumIntervalDays || (topicConfig.SkipPolicy != "" && topicConfig.SkipPolicy != "none") {
		effective.PersistedComplete = false
		effective.Diagnostics = append(effective.Diagnostics, ElementSourceDiagnostic{SourcePath: filepath.ToSlash(filepath.Join("scheduler", "topic-afactor-v1.json")), Code: "invalid-scheduler-config", Reason: "Scheduler configuration is invalid."})
		topicConfig = builtins["topic-afactor-v1"]
	}
	if defaults["arena-v1"].Primary != fsrsV1ID || defaults["arena-v1"].Fallback != simpleV1ID {
		effective.PersistedComplete = false
		effective.Diagnostics = append(effective.Diagnostics, ElementSourceDiagnostic{SourcePath: filepath.ToSlash(filepath.Join("scheduler", "arena-v1.json")), Code: "invalid-scheduler-config", Reason: "Scheduler configuration is invalid."})
	}
	effective.FSRS = fsrsConfig
	effective.Topic = topicConfig
	effective.LearningDay = learningDayConfig
	effective.LearningDayLocation = learningDayLocation
	sort.Slice(effective.Diagnostics, func(i, j int) bool {
		if effective.Diagnostics[i].SourcePath != effective.Diagnostics[j].SourcePath {
			return effective.Diagnostics[i].SourcePath < effective.Diagnostics[j].SourcePath
		}
		return effective.Diagnostics[i].Code < effective.Diagnostics[j].Code
	})
	return effective
}

func (effective EffectiveSchedulerConfig) ResolveLearningDayID(instant time.Time) string {
	location := effective.LearningDayLocation
	if location == nil {
		location = time.UTC
	}
	local := instant.In(location)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	boundary, err := localWallClockBoundary(date, effective.LearningDay.MidnightShiftHours, location)
	if err == nil && instant.Before(boundary) {
		date = date.AddDate(0, 0, -1)
	}
	return date.Format("2006-01-02")
}

func (effective EffectiveSchedulerConfig) TimeForLearningDayID(day string) (time.Time, error) {
	location := effective.LearningDayLocation
	if location == nil {
		location = time.UTC
	}
	date, err := time.Parse("2006-01-02", day)
	if err != nil {
		return time.Time{}, err
	}
	return localWallClockBoundary(date, effective.LearningDay.MidnightShiftHours, location)
}

func localWallClockBoundary(date time.Time, hour int, location *time.Location) (time.Time, error) {
	wallClock := time.Date(date.Year(), date.Month(), date.Day(), hour, 0, 0, 0, time.UTC)
	offsets := map[int]struct{}{}
	for probe := wallClock.Add(-48 * time.Hour); !probe.After(wallClock.Add(48 * time.Hour)); probe = probe.Add(time.Hour) {
		_, offset := probe.In(location).Zone()
		offsets[offset] = struct{}{}
	}
	var boundary time.Time
	bestWallSecond := 24 * 60 * 60
	for offset := range offsets {
		candidate := wallClock.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(location)
		if local.Year() != date.Year() || local.Month() != date.Month() || local.Day() != date.Day() {
			continue
		}
		wallSecond := local.Hour()*60*60 + local.Minute()*60 + local.Second()
		if wallSecond < hour*60*60 {
			continue
		}
		if boundary.IsZero() || wallSecond < bestWallSecond || (wallSecond == bestWallSecond && candidate.Before(boundary)) {
			boundary = candidate
			bestWallSecond = wallSecond
		}
	}
	if boundary.IsZero() {
		return time.Time{}, fmt.Errorf("resolve local Learning Day boundary for %s at %02d:00", date.Format("2006-01-02"), hour)
	}
	return boundary, nil
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
	return c.BootstrapSchedulerConfig()
}

func (c Config) BootstrapSchedulerConfig() error {
	if c.ReadOnly {
		return nil
	}
	events, err := c.LoadEventFiles()
	if err != nil {
		return err
	}
	if len(events) > 0 {
		return nil
	}
	if err := os.MkdirAll(c.SchedulerRoot, 0755); err != nil {
		return err
	}
	configs := defaultSchedulerConfigs()
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
	learningDayPath := filepath.Join(c.SchedulerRoot, "learning-day.json")
	if _, err := os.Stat(learningDayPath); errors.Is(err, os.ErrNotExist) {
		data, marshalErr := json.MarshalIndent(defaultLearningDayConfig(), "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		if err = filelock.WriteFile(learningDayPath, append(data, '\n')); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func (c Config) LoadElement(id string) (Element, error) {
	var element Element
	if id == "" {
		return element, errors.New("empty element id")
	}
	scan, err := c.scanElements()
	if err != nil {
		return element, err
	}
	element, ok := scan.Elements[id]
	if !ok {
		return Element{}, os.ErrNotExist
	}
	return element, nil
}

func (c Config) LoadElements() (map[string]Element, error) {
	scan, err := c.scanElements()
	return scan.Elements, err
}

type elementScanResult struct {
	Elements    map[string]Element
	Records     map[string]elementSourceRecord
	Diagnostics []ElementSourceDiagnostic
}

type elementSourceRecord struct {
	Element        Element
	SourcePath     string
	ParentID       string
	RootID         string
	StorageKind    StorageKind
	DiscoveryOrder int
	SortRank       *int
	Material       *ElementMaterialDiagnostic
}

const (
	sourceUnreadableCode             = "unreadable-element-source"
	sourceMalformedCode              = "malformed-element-source"
	sourceMissingCode                = "missing-element-source"
	sourceSpecCode                   = "unsupported-element-spec"
	sourceIdentityCode               = "element-id-mismatch"
	sourcePayloadCode                = "unsupported-element-payload"
	sourceIncompleteCode             = "incomplete-element-source"
	sourceDuplicateCode              = "duplicate-element-id"
	sourceMissingParent              = "missing-root-parent"
	sourceInvalidSort                = "invalid-sort-metadata"
	materialInvalidBlock             = "invalid-block-reference"
	materialEncrypted                = "encrypted-source-unsupported"
	schedulingHistoryUnavailableCode = "unusable-scheduling-history"
	missingTopicInitializationCode   = "missing-topic-initialization"
)

func (c Config) scanElements() (elementScanResult, error) {
	return c.scanElementsWithWalker(filepath.WalkDir)
}

func (c Config) scanElementsWithWalker(walkDir func(string, fs.WalkDirFunc) error) (elementScanResult, error) {
	result := elementScanResult{Elements: map[string]Element{}, Records: map[string]elementSourceRecord{}}
	root := c.ElementsRoot()
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return result, nil
	} else if err != nil {
		return result, err
	}
	sortRanks, sortDiagnostics := c.loadSortRanks()
	result.Diagnostics = append(result.Diagnostics, sortDiagnostics...)
	candidates := map[string][]elementSourceRecord{}
	missingParents := map[string]bool{}
	discoveryOrder := 0
	err := walkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if filepath.Clean(path) == filepath.Clean(root) {
				return walkErr
			}
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return walkErr
			}
			result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: filepath.ToSlash(filepath.Clean(relative)), Code: sourceUnreadableCode, Reason: "Element source could not be read."})
			return nil
		}
		if entry.IsDir() || filepath.Ext(path) != ".sme" {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		sourcePath := filepath.ToSlash(filepath.Clean(relative))
		data, readErr := filelock.ReadFile(path)
		if readErr != nil {
			result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, Code: sourceUnreadableCode, Reason: "Element source could not be read."})
			return nil
		}
		var element Element
		if decodeErr := json.Unmarshal(data, &element); decodeErr != nil {
			result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, Code: sourceMalformedCode, Reason: "Element source is malformed."})
			return nil
		}
		if hasInvalidRootAncestor(relative) {
			result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, Code: sourceIdentityCode, Reason: "Element identity does not match its source path."})
			return nil
		}
		missing := missingRootParents(root, relative)
		if len(missing) > 0 {
			for _, missingPath := range missing {
				normalized := filepath.ToSlash(filepath.Clean(missingPath))
				if missingParents[normalized] {
					continue
				}
				missingParents[normalized] = true
				result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: normalized, Code: sourceMissingParent, Reason: "A root Element parent is missing."})
			}
			return nil
		}
		if code, reason, validateErr := rootElementSourceIssue(element, filepath.Base(path)); validateErr != nil {
			result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, ElementID: trustworthyElementID(element, filepath.Base(path)), Code: code, Reason: reason})
			return nil
		}
		parentID := rootDocumentParentID(relative)
		var collect func(element Element, parentID, rootID string, kind StorageKind)
		collect = func(element Element, parentID, rootID string, kind StorageKind) {
			if code, reason, issue := childElementSourceIssue(element); issue != nil {
				result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, ElementID: element.ID, Code: code, Reason: reason})
				return
			}
			recordElement := element
			recordElement.Children = nil
			if material := recordElement.Payload.Material; material != nil && material.Kind == "html" && material.CleaningPolicyVersion == topicHTMLCleaningPolicyVersion {
				hardenedMaterial := *material
				hardenedHTML, hardenErr := hardenStoredTopicHTML(hardenedMaterial.HTML)
				if hardenErr != nil {
					result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, ElementID: recordElement.ID, Code: sourcePayloadCode, Reason: "Element HTML material is not renderable."})
					return
				}
				hardenedMaterial.HTML = hardenedHTML
				recordElement.Payload.Material = &hardenedMaterial
			}
			record := elementSourceRecord{
				Element:        recordElement,
				SourcePath:     sourcePath,
				ParentID:       parentID,
				RootID:         rootID,
				StorageKind:    kind,
				DiscoveryOrder: discoveryOrder,
			}
			if rank, ok := sortRanks[element.ID]; ok {
				record.SortRank = intPointer(rank)
			}
			if diagnostic := materialDiagnostic(recordElement); diagnostic != nil {
				record.Material = diagnostic
				result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, ElementID: recordElement.ID, Code: diagnostic.Code, Reason: diagnostic.Reason})
			}
			discoveryOrder++
			candidates[recordElement.ID] = append(candidates[recordElement.ID], record)
			for _, child := range element.Children {
				collect(child, element.ID, rootID, StorageKindInternal)
			}
		}
		collect(element, parentID, element.ID, StorageKindRootDocument)
		return nil
	})
	if err != nil {
		return result, err
	}
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		records := candidates[id]
		if len(records) == 1 {
			result.Elements[id] = records[0].Element
			result.Records[id] = records[0]
			continue
		}
		paths := make([]string, 0, len(records))
		for _, record := range records {
			paths = append(paths, record.SourcePath)
		}
		sort.Strings(paths)
		result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: paths[0], ElementID: id, Code: sourceDuplicateCode, Reason: "Element identity is duplicated.", RelatedPaths: paths})
	}
	if invalidateDuplicateSiblingSortRanks(result.Records) {
		result.Diagnostics = append(result.Diagnostics, ElementSourceDiagnostic{SourcePath: ".siyuan/sort.json", Code: sourceInvalidSort, Reason: "Sort metadata is invalid."})
	}
	result.Diagnostics = normalizeSourceDiagnostics(result.Diagnostics)
	return result, nil
}

func invalidateDuplicateSiblingSortRanks(records map[string]elementSourceRecord) bool {
	ids := make([]string, 0, len(records))
	parentsWithRootDocuments := map[string]bool{}
	for id := range records {
		ids = append(ids, id)
		record := records[id]
		if record.StorageKind == StorageKindRootDocument {
			parentsWithRootDocuments[record.ParentID] = true
		}
	}
	sort.Strings(ids)
	owners := map[string]map[int]string{}
	invalidParents := map[string]bool{}
	for _, id := range ids {
		record := records[id]
		if record.SortRank == nil || !parentsWithRootDocuments[record.ParentID] {
			continue
		}
		byRank := owners[record.ParentID]
		if byRank == nil {
			byRank = map[int]string{}
			owners[record.ParentID] = byRank
		}
		if owner, found := byRank[*record.SortRank]; found && owner != id {
			invalidParents[record.ParentID] = true
			continue
		}
		byRank[*record.SortRank] = id
	}
	if len(invalidParents) == 0 {
		return false
	}
	for _, id := range ids {
		record := records[id]
		if invalidParents[record.ParentID] {
			record.SortRank = nil
			records[id] = record
		}
	}
	return true
}

func hasInvalidRootAncestor(relative string) bool {
	dir := filepath.Dir(filepath.Clean(relative))
	if dir == "." || dir == "" {
		return false
	}
	for _, ancestorID := range strings.Split(dir, string(filepath.Separator)) {
		if !ast.IsNodeIDPattern(ancestorID) {
			return true
		}
	}
	return false
}

func (c Config) loadSortRanks() (map[string]int, []ElementSourceDiagnostic) {
	path := filepath.Join(c.ElementsRoot(), ".siyuan", "sort.json")
	data, err := filelock.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []ElementSourceDiagnostic{{SourcePath: ".siyuan/sort.json", Code: sourceInvalidSort, Reason: "Sort metadata is invalid."}}
	}
	var ranks map[string]int
	if err = json.Unmarshal(data, &ranks); err != nil {
		return nil, []ElementSourceDiagnostic{{SourcePath: ".siyuan/sort.json", Code: sourceInvalidSort, Reason: "Sort metadata is invalid."}}
	}
	for id, rank := range ranks {
		if id == "" || rank < 0 {
			return nil, []ElementSourceDiagnostic{{SourcePath: ".siyuan/sort.json", Code: sourceInvalidSort, Reason: "Sort metadata is invalid."}}
		}
	}
	return ranks, nil
}

func (c Config) loadSortRanksForCreate() (map[string]int, error) {
	ranks, diagnostics := c.loadSortRanks()
	if len(diagnostics) != 0 {
		return nil, domainError(ErrHistoryRequiresRepair, "Element sort metadata requires repair", nil)
	}
	if ranks == nil {
		ranks = map[string]int{}
	}
	return ranks, nil
}

func nextTopLevelSortRank(records map[string]elementSourceRecord) (int, error) {
	maxRank := -1
	for _, record := range records {
		if record.StorageKind != StorageKindRootDocument || record.ParentID != "" || record.SortRank == nil {
			continue
		}
		if *record.SortRank > maxRank {
			maxRank = *record.SortRank
		}
	}
	if maxRank == int(^uint(0)>>1) {
		return 0, domainError(ErrHistoryRequiresRepair, "Element sort rank space is exhausted", nil)
	}
	return maxRank + 1, nil
}

func (c Config) writeRootElementSource(element Element, data []byte) error {
	path := filepath.Join(c.ElementsRoot(), element.ID+".sme")
	if err := invokeCreateHTMLTopicAuthorityFault("before-root"); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := filelock.WriteFile(path, data); err != nil {
		return err
	}
	return invokeCreateHTMLTopicAuthorityFault("root")
}

func (c Config) writeTopLevelSortRanks(data []byte) error {
	path := filepath.Join(c.ElementsRoot(), ".siyuan", "sort.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := filelock.WriteFile(path, data); err != nil {
		return err
	}
	return invokeCreateHTMLTopicAuthorityFault("sort")
}

func missingRootParents(root, relative string) []string {
	clean := filepath.Clean(relative)
	dirs := strings.Split(filepath.Dir(clean), string(filepath.Separator))
	if len(dirs) == 0 || dirs[0] == "." {
		return nil
	}
	var missing []string
	for i := range dirs {
		ancestorDirs := dirs[:i+1]
		if !ast.IsNodeIDPattern(ancestorDirs[len(ancestorDirs)-1]) {
			continue
		}
		sourceParts := append([]string(nil), ancestorDirs[:len(ancestorDirs)-1]...)
		sourceParts = append(sourceParts, ancestorDirs[len(ancestorDirs)-1]+".sme")
		source := filepath.Join(sourceParts...)
		if _, err := os.Stat(filepath.Join(root, source)); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, source)
		}
	}
	return missing
}

func rootDocumentParentID(relative string) string {
	dir := filepath.Dir(filepath.Clean(relative))
	if dir == "." || dir == "" {
		return ""
	}
	return filepath.Base(dir)
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
		data, readErr := filelock.ReadFile(path)
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
	_, _, err := rootElementSourceIssue(element, filename)
	return err
}

func rootElementSourceIssue(element Element, filename string) (code, reason string, err error) {
	if element.Spec != SupportedElementSpec {
		return sourceSpecCode, "Element source uses an unsupported format.", fmt.Errorf("unsupported element spec %d", element.Spec)
	}
	if element.ID == "" || strings.TrimSuffix(filename, ".sme") != element.ID {
		return sourceIdentityCode, "Element identity does not match its source path.", errors.New("element id does not match source filename")
	}
	return childElementSourceIssue(element)
}

func childElementSourceIssue(element Element) (code, reason string, err error) {
	if element.Spec != SupportedElementSpec {
		return sourceSpecCode, "Element source uses an unsupported format.", fmt.Errorf("unsupported element spec %d", element.Spec)
	}
	if element.ID == "" {
		return sourceIdentityCode, "Element identity does not match its source path.", errors.New("element id is empty")
	}
	if element.ProcessingState == "" {
		return sourceIncompleteCode, "Element source is incomplete.", ErrUnavailableSource
	}
	switch element.Type {
	case "item":
		if element.PayloadSpec != SupportedPayloadSpec || element.Payload.Kind != "qa" {
			return sourcePayloadCode, "Element source is not a supported Q/A Item.", ErrUnavailableSource
		}
		if element.Payload.Prompt == "" || element.Payload.Answer == "" {
			return sourceIncompleteCode, "Element source is incomplete.", ErrUnavailableSource
		}
	case "topic":
		if element.PayloadSpec != SupportedPayloadSpec || element.Payload.Material == nil || element.Payload.Material.Kind == "" {
			return sourcePayloadCode, "Element source is not a supported Topic.", ErrUnavailableSource
		}
	case "concept":
		if element.PayloadSpec != SupportedPayloadSpec {
			return sourcePayloadCode, "Element source is not a supported Concept.", ErrUnavailableSource
		}
	default:
		if len(element.Payload.Raw) == 0 {
			return sourceIncompleteCode, "Element source is incomplete.", ErrUnavailableSource
		}
	}
	return "", "", nil
}

func materialDiagnostic(element Element) *ElementMaterialDiagnostic {
	if element.Type != "topic" || element.Payload.Material == nil {
		return nil
	}
	switch element.Payload.Material.Kind {
	case "html":
		return nil
	case "siyuanBlock":
		if !ast.IsNodeIDPattern(element.Payload.Material.BlockID) {
			return &ElementMaterialDiagnostic{Code: materialInvalidBlock, Reason: "The SiYuan block reference is invalid."}
		}
		return nil
	default:
		return &ElementMaterialDiagnostic{Code: sourcePayloadCode, Reason: "Element material is not supported."}
	}
}

func trustworthyElementID(element Element, filename string) string {
	if element.ID != "" && strings.TrimSuffix(filename, ".sme") == element.ID {
		return element.ID
	}
	return ""
}

func sourceDiagnosticsWithMissingProjections(scan elementScanResult, projections map[string]SchedulingProjection, historyElementIDs map[string]bool) []ElementSourceDiagnostic {
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
	for elementID := range historyElementIDs {
		if _, found := scan.Elements[elementID]; !found {
			continue
		}
		if _, projected := projections[elementID]; projected {
			continue
		}
		sourcePath := filepath.ToSlash(elementID + ".sme")
		if record, found := scan.Records[elementID]; found && record.SourcePath != "" {
			sourcePath = record.SourcePath
		}
		diagnostics = append(diagnostics, ElementSourceDiagnostic{SourcePath: sourcePath, ElementID: elementID, Code: schedulingHistoryUnavailableCode, Reason: "Scheduling history cannot produce a valid projection."})
	}
	for elementID, record := range scan.Records {
		if _, projected := projections[elementID]; projected || historyElementIDs[elementID] {
			continue
		}
		material := record.Element.Payload.Material
		if record.Element.Type != "topic" || material == nil || material.CleaningPolicyVersion != topicHTMLCleaningPolicyVersion {
			continue
		}
		diagnostics = append(diagnostics, ElementSourceDiagnostic{SourcePath: record.SourcePath, ElementID: elementID, Code: missingTopicInitializationCode, Reason: "HTML Topic initialization history is missing."})
	}
	return normalizeSourceDiagnostics(diagnostics)
}

func normalizeSourceDiagnostics(diagnostics []ElementSourceDiagnostic) []ElementSourceDiagnostic {
	for i := range diagnostics {
		sort.Strings(diagnostics[i].RelatedPaths)
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].SourcePath != diagnostics[j].SourcePath {
			return diagnostics[i].SourcePath < diagnostics[j].SourcePath
		}
		if diagnostics[i].Code != diagnostics[j].Code {
			return diagnostics[i].Code < diagnostics[j].Code
		}
		if diagnostics[i].ElementID != diagnostics[j].ElementID {
			return diagnostics[i].ElementID < diagnostics[j].ElementID
		}
		if diagnostics[i].Reason != diagnostics[j].Reason {
			return diagnostics[i].Reason < diagnostics[j].Reason
		}
		return strings.Join(diagnostics[i].RelatedPaths, "\x00") < strings.Join(diagnostics[j].RelatedPaths, "\x00")
	})
	normalized := diagnostics[:0]
	for _, diagnostic := range diagnostics {
		if len(normalized) == 0 || !sameSourceDiagnosticKey(normalized[len(normalized)-1], diagnostic) {
			normalized = append(normalized, diagnostic)
			continue
		}
		normalized[len(normalized)-1].RelatedPaths = mergeSortedStrings(normalized[len(normalized)-1].RelatedPaths, diagnostic.RelatedPaths)
	}
	return normalized
}

func filterSourceDiagnostics(diagnostics []ElementSourceDiagnostic, elementID, sourcePath string) []ElementSourceDiagnostic {
	if sourcePath != "" {
		sourcePath = filepath.ToSlash(filepath.Clean(sourcePath))
	}
	filtered := make([]ElementSourceDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if elementID != "" && diagnostic.ElementID != elementID {
			continue
		}
		if sourcePath != "" && diagnostic.SourcePath != sourcePath {
			continue
		}
		filtered = append(filtered, diagnostic)
	}
	return filtered
}

func diagnosedElementSourceCode(elementID string, diagnostics []ElementSourceDiagnostic) ErrorCode {
	unavailable := false
	for _, diagnostic := range diagnostics {
		pathID := strings.TrimSuffix(filepath.Base(filepath.FromSlash(diagnostic.SourcePath)), ".sme")
		if diagnostic.ElementID != elementID && pathID != elementID {
			continue
		}
		if diagnostic.Code == sourceDuplicateCode {
			return ErrElementSourceAmbiguous
		}
		unavailable = true
	}
	if unavailable {
		return ErrElementSourceUnavailable
	}
	return ErrElementNotFound
}

func sameSourceDiagnosticKey(left, right ElementSourceDiagnostic) bool {
	return left.SourcePath == right.SourcePath && left.Code == right.Code && left.ElementID == right.ElementID
}

func mergeSortedStrings(left, right []string) []string {
	values := make(map[string]bool, len(left)+len(right))
	for _, value := range left {
		values[value] = true
	}
	for _, value := range right {
		values[value] = true
	}
	merged := make([]string, 0, len(values))
	for value := range values {
		merged = append(merged, value)
	}
	sort.Strings(merged)
	return merged
}

var ErrUnavailableSource = errors.New("authoritative element source unavailable")

func unavailableElementSource(cause error) error {
	return domainError(ErrAuthoritativeElementUnavailable, "authoritative Element source is unavailable", cause)
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
