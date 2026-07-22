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

//go:build !cgo

package symemo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/siyuan-note/filelock"
)

const projectionSchemaVersion = 6

var errProjectionNotFound = errors.New("projection not found")

type fileProjection struct {
	SchemaVersion            int                             `json:"schemaVersion"`
	Elements                 map[string]Element              `json:"elements"`
	Tree                     []ElementTreeNode               `json:"tree"`
	Projections              map[string]SchedulingProjection `json:"projections"`
	FinalDrillProjections    map[string]FinalDrillProjection `json:"finalDrillProjections"`
	HistoryEventFingerprints []string                        `json:"historyEventFingerprints"`
	Diagnostics              []EventDiagnostic               `json:"diagnostics"`
	SourceDiagnostics        []ElementSourceDiagnostic       `json:"sourceDiagnostics"`
}

type projectionIndex struct {
	path string
	mu   sync.RWMutex
	data fileProjection
}

type projectionSnapshot struct {
	Elements                 map[string]Element
	Tree                     []ElementTreeNode
	Projections              map[string]SchedulingProjection
	FinalDrillProjections    map[string]FinalDrillProjection
	HistoryEventFingerprints map[string]bool
	BlockedElementIDs        map[string]bool
}

func openProjectionIndex(path string) (*projectionIndex, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	index := &projectionIndex{path: path, data: emptyFileProjection()}
	data, err := os.ReadFile(path)
	if err == nil {
		if unmarshalErr := json.Unmarshal(data, &index.data); unmarshalErr != nil || index.data.SchemaVersion != projectionSchemaVersion || index.data.Elements == nil || index.data.Projections == nil || index.data.FinalDrillProjections == nil || index.data.HistoryEventFingerprints == nil {
			index.data = emptyFileProjection()
			if saveErr := index.saveLocked(); saveErr != nil {
				return nil, saveErr
			}
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err = index.saveLocked(); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	return index, nil
}

func emptyFileProjection() fileProjection {
	return fileProjection{SchemaVersion: projectionSchemaVersion, Elements: map[string]Element{}, Tree: []ElementTreeNode{}, Projections: map[string]SchedulingProjection{}, FinalDrillProjections: map[string]FinalDrillProjection{}, HistoryEventFingerprints: []string{}}
}

func (index *projectionIndex) replaceAll(_ context.Context, build projectionBuild) error {
	index.mu.Lock()
	defer index.mu.Unlock()
	next := fileProjection{SchemaVersion: projectionSchemaVersion, Elements: build.Elements, Tree: build.Tree, Projections: build.Projections, FinalDrillProjections: build.FinalDrillProjections, HistoryEventFingerprints: append([]string{}, build.HistoryEventFingerprints...), Diagnostics: build.EventDiagnostics, SourceDiagnostics: build.SourceDiagnostics}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err = index.saveDataLocked(data); err != nil {
		return err
	}
	index.data = next
	return nil
}

func (index *projectionIndex) tree() ([]ElementTreeNode, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	return append([]ElementTreeNode(nil), index.data.Tree...), nil
}

func (index *projectionIndex) saveDataLocked(data []byte) error {
	if info, err := os.Stat(index.path); err == nil && info.IsDir() {
		return errors.New("projection path is a directory")
	}
	return filelock.WriteFile(index.path, append(data, '\n'))
}

func (index *projectionIndex) saveLocked() error {
	data, err := json.MarshalIndent(index.data, "", "  ")
	if err != nil {
		return err
	}
	return index.saveDataLocked(data)
}

func (index *projectionIndex) sourceDiagnostics() ([]ElementSourceDiagnostic, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	return append([]ElementSourceDiagnostic(nil), index.data.SourceDiagnostics...), nil
}

func (index *projectionIndex) projection(elementID string) (SchedulingProjection, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	projection, ok := index.data.Projections[elementID]
	if !ok {
		return SchedulingProjection{}, errProjectionNotFound
	}
	return cloneSchedulingProjection(projection)
}

func (index *projectionIndex) finalDrillProjection(elementID string) (FinalDrillProjection, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	projection, ok := index.data.FinalDrillProjections[elementID]
	if !ok {
		return FinalDrillProjection{}, errProjectionNotFound
	}
	return projection, nil
}

func (index *projectionIndex) element(elementID string) (Element, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	element, ok := index.data.Elements[elementID]
	if !ok {
		return Element{}, errProjectionNotFound
	}
	return element, nil
}

func (index *projectionIndex) snapshot() (projectionSnapshot, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	snapshot := projectionSnapshot{
		Elements:                 make(map[string]Element, len(index.data.Elements)),
		Tree:                     append([]ElementTreeNode(nil), index.data.Tree...),
		Projections:              make(map[string]SchedulingProjection, len(index.data.Projections)),
		FinalDrillProjections:    make(map[string]FinalDrillProjection, len(index.data.FinalDrillProjections)),
		HistoryEventFingerprints: make(map[string]bool, len(index.data.HistoryEventFingerprints)),
		BlockedElementIDs:        make(map[string]bool),
	}
	for id, element := range index.data.Elements {
		snapshot.Elements[id] = element
	}
	for id, projection := range index.data.Projections {
		cloned, err := cloneSchedulingProjection(projection)
		if err != nil {
			return projectionSnapshot{}, err
		}
		snapshot.Projections[id] = cloned
	}
	for id, projection := range index.data.FinalDrillProjections {
		snapshot.FinalDrillProjections[id] = projection
	}
	for _, fingerprint := range index.data.HistoryEventFingerprints {
		snapshot.HistoryEventFingerprints[fingerprint] = true
	}
	for _, diagnostic := range index.data.SourceDiagnostics {
		if diagnostic.Code == schedulingHistoryUnavailableCode && diagnostic.ElementID != "" {
			snapshot.BlockedElementIDs[diagnostic.ElementID] = true
		}
	}
	return snapshot, nil
}

func (index *projectionIndex) close() error { return nil }

func removeSQLiteFiles(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}
