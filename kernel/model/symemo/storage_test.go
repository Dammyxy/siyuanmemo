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
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLearningDayBoundaryUsesLocalWallClockAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		day      string
		shift    int
		boundary time.Time
	}{
		{name: "spring day after gap", day: "2026-03-08", shift: 4, boundary: time.Date(2026, time.March, 8, 4, 0, 0, 0, location)},
		{name: "fall day after overlap", day: "2026-11-01", shift: 4, boundary: time.Date(2026, time.November, 1, 4, 0, 0, 0, location)},
		{name: "boundary inside spring gap", day: "2026-03-08", shift: 2, boundary: time.Date(2026, time.March, 8, 3, 0, 0, 0, location)},
		{name: "boundary at first fall occurrence", day: "2026-11-01", shift: 1, boundary: time.Date(2026, time.November, 1, 5, 0, 0, 0, time.UTC)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effective := EffectiveSchedulerConfig{
				LearningDay:         LearningDayConfigV1{Spec: 1, TimeZoneIANA: "America/New_York", MidnightShiftHours: test.shift},
				LearningDayLocation: location,
			}
			boundary, err := effective.TimeForLearningDayID(test.day)
			if err != nil {
				t.Fatal(err)
			}
			if !boundary.Equal(test.boundary) {
				t.Fatalf("boundary = %s, want %s", boundary, test.boundary)
			}
			previousDay := test.boundary.Add(-time.Nanosecond)
			previousDate := test.boundary.In(location).AddDate(0, 0, -1).Format("2006-01-02")
			if got := effective.ResolveLearningDayID(previousDay); got != previousDate {
				t.Fatalf("day immediately before boundary = %s, want %s", got, previousDate)
			}
			if got := effective.ResolveLearningDayID(test.boundary); got != test.day {
				t.Fatalf("day at boundary = %s, want %s", got, test.day)
			}
		})
	}
}

func TestLearningDayConfigChangeDoesNotRewriteRecordedEventDay(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "UTC", 4)
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	writeTestJSON(t, reviewPath, EventFile{Spec: 1, Month: "2026-07", Events: []SchedulingEvent{{
		Spec:          1,
		EventID:       "recorded-learning-day",
		OccurredAt:    time.Date(2026, time.July, 20, 2, 0, 0, 0, time.UTC),
		Type:          "reviewElement",
		ElementID:     fixtureElementID,
		LearningDate:  "2026-07-19",
		LearningDayID: "2026-07-19",
	}}})
	before, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(config.SchedulerRoot, "learning-day.json"), LearningDayConfigV1{Spec: 1, TimeZoneIANA: "Asia/Shanghai", MidnightShiftHours: 4})
	effective := config.LoadEffectiveSchedulerConfig()
	if got := effective.ResolveLearningDayID(time.Date(2026, time.July, 20, 2, 0, 0, 0, time.UTC)); got != "2026-07-20" {
		t.Fatalf("future Learning Day = %s", got)
	}
	after, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("loading changed Learning Day configuration rewrote review authority")
	}
	events, err := config.LoadEventFiles()
	if err != nil || len(events) != 1 || events[0].LearningDayID != "2026-07-19" || events[0].LearningDate != "2026-07-19" {
		t.Fatalf("recorded Learning Day changed: %#v, err=%v", events, err)
	}
}

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

func TestEngineUsesInMemorySchedulerDefaultsWithoutWriting(t *testing.T) {
	config := copyFixtureWorkspace(t)
	if err := os.RemoveAll(config.SchedulerRoot); err != nil {
		t.Fatal(err)
	}
	before := snapshotPaths(t, config.StorageRoot)
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	loaded := config.LoadEffectiveSchedulerConfig()
	if loaded.FSRS.Algorithm != fsrsV1ID || loaded.FSRS.RequestRetention != 0.9 || loaded.FSRS.MaximumIntervalDays != 36500 || len(loaded.FSRS.Weights) != 19 || !loaded.FSRS.EnableShortTerm || loaded.FSRS.EnableFuzz {
		t.Fatalf("effective scheduler defaults = %#v", loaded.FSRS)
	}
	if _, err = os.Stat(config.SchedulerRoot); !os.IsNotExist(err) {
		t.Fatalf("scheduler directory was created by Engine: %v", err)
	}
	if after := snapshotPaths(t, config.StorageRoot); !equalStringMaps(before, after) {
		t.Fatalf("Engine changed authoritative source paths\nbefore=%#v\nafter=%#v", before, after)
	}
	diagnostics, err := engine.index.sourceDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	foundMissing := false
	for _, diagnostic := range diagnostics {
		foundMissing = foundMissing || diagnostic.Code == "missing-scheduler-config"
	}
	if !foundMissing {
		t.Fatalf("workspace with review history omitted scheduler authority loss: %#v", diagnostics)
	}
}

func TestCleanWorkspaceDoesNotReportMissingSchedulerConfig(t *testing.T) {
	config := copyFixtureWorkspace(t)
	if err := os.RemoveAll(config.SchedulerRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(config.ReviewsRoot()); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	diagnostics, err := engine.index.sourceDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "missing-scheduler-config" {
			t.Fatalf("clean workspace reported scheduler authority loss: %#v", diagnostics)
		}
	}
	if _, err = os.Stat(config.SchedulerRoot); !os.IsNotExist(err) {
		t.Fatalf("clean Engine open created scheduler authority: %v", err)
	}
}

func TestSchedulingWriteRequiresPersistedSchedulerConfig(t *testing.T) {
	config := copyFixtureWorkspace(t)
	if err := os.RemoveAll(config.SchedulerRoot); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	before := snapshotPaths(t, config.StorageRoot)
	grade := 4
	_, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "missing-scheduler-config-grade"})
	if !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("missing scheduler grade error = %v", err)
	}
	if _, statErr := os.Stat(config.SchedulerRoot); !os.IsNotExist(statErr) {
		t.Fatalf("scheduler directory was created by grade: %v", statErr)
	}
	if after := snapshotPaths(t, config.StorageRoot); !equalStringMaps(before, after) {
		t.Fatalf("blocked grade changed authoritative source paths\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestSchedulingWriteRequiresEngineRebuildAfterSchedulerAuthorityRecovery(t *testing.T) {
	config := copyFixtureWorkspace(t)
	fsrsPath := filepath.Join(config.SchedulerRoot, "fsrs-v1.json")
	data, err := os.ReadFile(fsrsPath)
	if err != nil {
		t.Fatal(err)
	}
	var recoveredConfig SchedulerConfig
	if err = json.Unmarshal(data, &recoveredConfig); err != nil {
		t.Fatal(err)
	}
	recoveredConfig.MaximumIntervalDays = 2
	if err = os.Remove(fsrsPath); err != nil {
		t.Fatal(err)
	}

	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, fsrsPath, recoveredConfig)
	before, err := config.LoadEventFiles()
	if err != nil {
		t.Fatal(err)
	}
	grade := 4
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "stale-scheduler-authority"}); !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("stale Engine grade error = %v", err)
	}
	after, err := config.LoadEventFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("stale Engine wrote review history: before=%d after=%d", len(before), len(after))
	}
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}

	replacement, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	if _, err = replacement.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err = replacement.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	result, err := replacement.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "recovered-scheduler-authority"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Projection == nil || result.Projection.IntervalDays != 3 {
		t.Fatalf("recovered scheduler projection = %#v", result.Projection)
	}
}

func TestSchedulerSemanticDiagnosticsIdentifyOwningFile(t *testing.T) {
	tests := []struct {
		name       string
		configName string
		config     SchedulerConfig
		wantPath   string
	}{
		{name: "simple algorithm", configName: "simple-v1", config: SchedulerConfig{Spec: 1, Algorithm: "wrong", IntervalRule: "item-simple-v1"}, wantPath: "scheduler/simple-v1.json"},
		{name: "arena algorithms", configName: "arena-v1", config: SchedulerConfig{Spec: 1, Primary: "wrong", EnabledAlgorithms: []string{fsrsV1ID, simpleV1ID}, Fallback: simpleV1ID}, wantPath: "scheduler/arena-v1.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := copyFixtureWorkspace(t)
			writeTestJSON(t, filepath.Join(config.SchedulerRoot, test.configName+".json"), test.config)
			engine, err := NewEngine(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Close() })
			diagnostics, err := engine.index.sourceDiagnostics()
			if err != nil {
				t.Fatal(err)
			}
			var invalid []ElementSourceDiagnostic
			for _, diagnostic := range diagnostics {
				if diagnostic.Code == "invalid-scheduler-config" {
					invalid = append(invalid, diagnostic)
				}
			}
			if len(invalid) != 1 || invalid[0].SourcePath != test.wantPath {
				t.Fatalf("semantic diagnostics = %#v, want owning path %s", invalid, test.wantPath)
			}
		})
	}
}

func TestBootstrapSchedulerConfigMissingOnlyAndReadOnlyBypass(t *testing.T) {
	config := copyFixtureWorkspace(t)
	path := filepath.Join(config.SchedulerRoot, "collection.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = config.BootstrapSchedulerConfig(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("existing scheduler config was overwritten")
	}
	readOnly := config
	readOnly.ReadOnly = true
	missing := filepath.Join(readOnly.SchedulerRoot, "topic-afactor-v1.json")
	if err = os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if err = readOnly.BootstrapSchedulerConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("read-only bootstrap created scheduler config: %v", err)
	}
}

func TestBootstrapSchedulerConfigDoesNotReplaceMissingHistoricalAuthority(t *testing.T) {
	config := copyFixtureWorkspace(t)
	missing := filepath.Join(config.SchedulerRoot, "fsrs-v1.json")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	before := snapshotPaths(t, config.StorageRoot)
	if err := config.BootstrapSchedulerConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bootstrap replaced missing historical scheduler authority: %v", err)
	}
	if after := snapshotPaths(t, config.StorageRoot); !equalStringMaps(before, after) {
		t.Fatalf("bootstrap changed historical authority\nbefore=%#v\nafter=%#v", before, after)
	}
}

func snapshotPaths(t *testing.T, root string) map[string]string {
	t.Helper()
	paths := map[string]string{}
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return paths
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == "temp" {
			return fs.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		paths[filepath.ToSlash(relative)] = string(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return paths
}

func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
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
