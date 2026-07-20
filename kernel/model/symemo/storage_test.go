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
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStorageFixtureValidation(t *testing.T) {
	_, config := newFixtureEngine(t)
	elements, err := config.LoadElements()
	if err != nil {
		t.Fatal(err)
	}
	item, ok := elements[fixtureElementID]
	if !ok || item.Payload.Prompt == "" || item.Payload.Answer == "" || item.ProcessingState != "processed" {
		t.Fatalf("unexpected fixture: %#v", item)
	}
	events, err := config.LoadEventFiles()
	if err != nil || len(events) != 1 {
		t.Fatalf("events: %v %#v", err, events)
	}
}

func TestStorageDiagnosesUnsupportedSchema(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "elements", fixtureElementID+".sme")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(Element{Spec: 99, ID: fixtureElementID, Type: "item", PayloadSpec: 1, ProcessingState: "processed", Payload: ItemPayload{Kind: "qa", Prompt: "p", Answer: "a"}})
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	config := Config{StorageRoot: root, IndexRoot: filepath.Join(root, "temp")}
	scan, err := config.scanElements()
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Elements) != 0 || len(scan.Diagnostics) != 1 {
		t.Fatalf("unsupported Element scan = %#v", scan)
	}
	diagnostic := scan.Diagnostics[0]
	if diagnostic.SourcePath != fixtureElementID+".sme" || diagnostic.ElementID != fixtureElementID || diagnostic.Code != "unsupported-element-spec" {
		t.Fatalf("unsupported Element diagnostic = %#v", diagnostic)
	}
}

func TestStorageRejectsUnsupportedReviewAndSchedulerSchemas(t *testing.T) {
	t.Run("review envelope", func(t *testing.T) {
		root := t.TempDir()
		writeTestJSON(t, filepath.Join(root, "reviews", "2026-07.smr"), EventFile{Spec: 99, Month: "2026-07"})
		config := Config{StorageRoot: root}
		if _, err := config.LoadEventFiles(); !hasCode(err, ErrHistoryRequiresRepair) {
			t.Fatalf("review envelope error = %v", err)
		}
	})

	t.Run("event", func(t *testing.T) {
		root := t.TempDir()
		writeTestJSON(t, filepath.Join(root, "reviews", "2026-07.smr"), EventFile{Spec: 1, Month: "2026-07", Events: []SchedulingEvent{{Spec: 99, EventID: "future"}}})
		config := Config{StorageRoot: root}
		events, err := config.LoadEventFiles()
		if err != nil || len(events) != 1 {
			t.Fatalf("scheduling event load = %#v, err=%v", events, err)
		}
		_, diagnostics := projectSchedulingEvents(events)
		if diagnostic := diagnosticByID(diagnostics, "future"); diagnostic.Classification != "invalid" || diagnostic.Reason != "invalid-event" {
			t.Fatalf("scheduling event diagnostic = %#v", diagnostic)
		}
	})

	t.Run("scheduler", func(t *testing.T) {
		root := t.TempDir()
		writeTestJSON(t, filepath.Join(root, "fsrs-v1.json"), SchedulerConfig{Spec: 99, Algorithm: fsrsV1ID})
		config := Config{SchedulerRoot: root}
		if _, err := config.LoadSchedulerConfig(fsrsV1ID); err == nil {
			t.Fatal("expected unsupported scheduler schema error")
		}
	})
}

func TestEngineInitializesMissingSchedulerDefaults(t *testing.T) {
	config := copyFixtureWorkspace(t)
	if err := os.RemoveAll(config.SchedulerRoot); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	loaded, err := config.LoadTracerSchedulerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Algorithm != fsrsV1ID || loaded.RequestRetention != 0.9 || loaded.MaximumIntervalDays != 36500 || len(loaded.Weights) != 19 || !loaded.EnableShortTerm || loaded.EnableFuzz {
		t.Fatalf("generated scheduler defaults = %#v", loaded)
	}
	for _, name := range []string{"collection.json", "simple-v1.json", "fsrs-v1.json", "arena-v1.json"} {
		if _, err = os.Stat(filepath.Join(config.SchedulerRoot, name)); err != nil {
			t.Fatalf("missing generated scheduler config %s: %v", name, err)
		}
	}
}

func TestSchedulerDefaultsDoNotOverwriteExistingConfig(t *testing.T) {
	config := copyFixtureWorkspace(t)
	path := filepath.Join(config.SchedulerRoot, "collection.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = config.ensureTracerSchedulerConfig(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("existing scheduler config was overwritten")
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
