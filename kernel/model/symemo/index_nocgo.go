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
	"sort"
	"sync"
	"time"

	"github.com/siyuan-note/filelock"
)

const projectionSchemaVersion = 2

var errProjectionNotFound = errors.New("projection not found")

type fileProjection struct {
	SchemaVersion     int                             `json:"schemaVersion"`
	Elements          map[string]Element              `json:"elements"`
	Projections       map[string]SchedulingProjection `json:"projections"`
	Diagnostics       []EventDiagnostic               `json:"diagnostics"`
	SourceDiagnostics []ElementSourceDiagnostic       `json:"sourceDiagnostics"`
}

type projectionIndex struct {
	path string
	mu   sync.RWMutex
	data fileProjection
}

func openProjectionIndex(path string) (*projectionIndex, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	index := &projectionIndex{path: path, data: emptyFileProjection()}
	data, err := os.ReadFile(path)
	if err == nil {
		if unmarshalErr := json.Unmarshal(data, &index.data); unmarshalErr != nil || index.data.SchemaVersion != projectionSchemaVersion || index.data.Elements == nil || index.data.Projections == nil {
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
	return fileProjection{SchemaVersion: projectionSchemaVersion, Elements: map[string]Element{}, Projections: map[string]SchedulingProjection{}}
}

func (index *projectionIndex) replaceAll(_ context.Context, build projectionBuild) error {
	index.mu.Lock()
	defer index.mu.Unlock()
	index.data = fileProjection{SchemaVersion: projectionSchemaVersion, Elements: build.Elements, Projections: build.Projections, Diagnostics: build.EventDiagnostics, SourceDiagnostics: build.SourceDiagnostics}
	return index.saveLocked()
}

func (index *projectionIndex) sourceDiagnostics() ([]ElementSourceDiagnostic, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	return append([]ElementSourceDiagnostic(nil), index.data.SourceDiagnostics...), nil
}

func (index *projectionIndex) saveLocked() error {
	if info, err := os.Stat(index.path); err == nil && info.IsDir() {
		return errors.New("projection path is a directory")
	}
	data, err := json.MarshalIndent(index.data, "", "  ")
	if err != nil {
		return err
	}
	return filelock.WriteFile(index.path, append(data, '\n'))
}

func (index *projectionIndex) projection(elementID string) (SchedulingProjection, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	projection, ok := index.data.Projections[elementID]
	if !ok {
		return SchedulingProjection{}, errProjectionNotFound
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

func (index *projectionIndex) dueTargets(now time.Time, learningDate string) ([]ReviewTarget, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	var targets []ReviewTarget
	for id, projection := range index.data.Projections {
		element, ok := index.data.Elements[id]
		if !ok || projection.LifecycleState != "memorized" || projection.DueAt.After(now) || projection.LastLearningDate == learningDate {
			continue
		}
		targets = append(targets, ReviewTarget{Kind: "element.item", ElementID: id, Prompt: element.Payload.Prompt, DueAt: projection.DueAt, PriorityPosition: projection.PriorityPosition, ObservedBaseSchedulingEvent: projection.AdoptedTerminalID, ObservedProjection: projection, LearningDate: learningDate})
	}
	sort.Slice(targets, func(i, j int) bool {
		if !targets[i].DueAt.Equal(targets[j].DueAt) {
			return targets[i].DueAt.Before(targets[j].DueAt)
		}
		if targets[i].PriorityPosition != targets[j].PriorityPosition {
			return targets[i].PriorityPosition < targets[j].PriorityPosition
		}
		return targets[i].ElementID < targets[j].ElementID
	})
	return targets, nil
}

func (index *projectionIndex) close() error { return nil }

func removeSQLiteFiles(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}
