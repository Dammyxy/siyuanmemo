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
	"time"
)

func TestRebuildAfterProjectionRemoval(t *testing.T) {
	for raw := 0; raw <= 5; raw++ {
		t.Run(string(rune('0'+raw)), func(t *testing.T) {
			engine, config := newFixtureEngine(t)
			_, _ = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
			_, _ = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID})
			grade := raw
			result, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "rebuild-grade-" + string(rune('0'+raw))})
			if err != nil {
				t.Fatal(err)
			}
			expected, _ := json.Marshal(result.Projection)
			if err = engine.Close(); err != nil {
				t.Fatal(err)
			}
			removeSQLiteFiles(config.IndexPath())
			reopened, err := NewEngine(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			projection, err := reopened.ledger.Snapshot(fixtureElementID)
			if err != nil {
				t.Fatal(err)
			}
			actual, _ := json.Marshal(&projection)
			if string(actual) != string(expected) {
				t.Fatalf("projection differs after rebuild\nactual=%s\nexpected=%s", actual, expected)
			}
			due, err := reopened.Query(context.Background(), Query{Kind: QueryElementSubset, Subset: "due"})
			if err != nil || len(due.Items) != 0 {
				t.Fatalf("rebuilt same-day due = %#v, err=%v", due, err)
			}
		})
	}
}

func TestRebuildCorruptProjection(t *testing.T) {
	engine, config := newFixtureEngine(t)
	expected, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	_ = engine.Close()
	removeSQLiteFiles(config.IndexPath())
	if err = os.WriteFile(config.IndexPath(), []byte("not a compatible projection"), 0644); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	actual, err := reopened.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON, _ := json.Marshal(expected)
	actualJSON, _ := json.Marshal(actual)
	if string(expectedJSON) != string(actualJSON) {
		t.Fatalf("corrupt rebuild differs\nactual=%s\nexpected=%s", actualJSON, expectedJSON)
	}
}

func TestRebuildSchemaMismatch(t *testing.T) {
	engine, config := newFixtureEngine(t)
	_ = engine.Close()
	removeSQLiteFiles(config.IndexPath())
	if err := os.WriteFile(config.IndexPath(), []byte(`{"schemaVersion":0,"elements":{},"projections":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.ledger.Snapshot(fixtureElementID); err != nil {
		t.Fatal(err)
	}
}

func TestRebuildAuthoritativeScanOrderAndNotDue(t *testing.T) {
	t.Run("month order", func(t *testing.T) {
		root := t.TempDir()
		writeTestJSON(t, filepath.Join(root, "reviews", "2026-08.smr"), EventFile{Spec: 1, Month: "2026-08", Events: []SchedulingEvent{{Spec: 1, EventID: "august", OccurredAt: time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)}}})
		writeTestJSON(t, filepath.Join(root, "reviews", "2026-07.smr"), EventFile{Spec: 1, Month: "2026-07", Events: []SchedulingEvent{{Spec: 1, EventID: "july", OccurredAt: time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)}}})
		events, err := (Config{StorageRoot: root}).LoadEventFiles()
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 2 || events[0].EventID != "july" || events[1].EventID != "august" {
			t.Fatalf("event discovery order = %#v", events)
		}
	})

	t.Run("not due", func(t *testing.T) {
		config := copyFixtureWorkspace(t)
		clock := time.Date(2026, time.July, 19, 7, 0, 0, 0, config.Location)
		config.Now = func() time.Time { return clock }
		engine, err := NewEngine(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		defer engine.Close()
		result, err := engine.Query(context.Background(), Query{Kind: QueryElementSubset, Subset: "due"})
		if err != nil || len(result.Items) != 0 {
			t.Fatalf("not-due query = %#v, err=%v", result, err)
		}
	})
}

func TestRebuildIsolatesAndPersistsInvalidElementSources(t *testing.T) {
	config := copyFixtureWorkspace(t)
	malformedPath := filepath.Join(config.ElementsRoot(), "nested", "malformed.sme")
	malformedData := []byte(`{"spec":1,"id":`)
	if err := os.MkdirAll(filepath.Dir(malformedPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(malformedPath, malformedData, 0644); err != nil {
		t.Fatal(err)
	}
	unsupportedID := "20260719010103-unsupported"
	unsupportedPath := filepath.Join(config.ElementsRoot(), unsupportedID+".sme")
	unsupportedData, err := json.Marshal(Element{Spec: 99, ID: unsupportedID, Type: "item", ProcessingState: "processed", PayloadSpec: 1, Payload: ItemPayload{Kind: "qa", Prompt: "p", Answer: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(unsupportedPath, unsupportedData, 0644); err != nil {
		t.Fatal(err)
	}

	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	due, err := engine.Query(context.Background(), Query{Kind: QueryElementSubset, Subset: "due"})
	if err != nil || len(due.Items) != 1 || due.Items[0].ElementID != fixtureElementID {
		t.Fatalf("valid due Items = %#v, err=%v", due.Items, err)
	}
	diagnostics, err := engine.index.sourceDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	assertSourceDiagnostics(t, diagnostics, map[string]ElementSourceDiagnostic{
		"nested/malformed.sme": {SourcePath: "nested/malformed.sme", Code: "malformed-element-source", Reason: "Element source is malformed."},
		unsupportedID + ".sme": {SourcePath: unsupportedID + ".sme", ElementID: unsupportedID, Code: "unsupported-element-spec", Reason: "Element source uses an unsupported format."},
	})
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openProjectionIndex(config.IndexPath())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	persisted, err := reopened.sourceDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	assertSourceDiagnostics(t, persisted, map[string]ElementSourceDiagnostic{
		"nested/malformed.sme": {SourcePath: "nested/malformed.sme", Code: "malformed-element-source", Reason: "Element source is malformed."},
		unsupportedID + ".sme": {SourcePath: unsupportedID + ".sme", ElementID: unsupportedID, Code: "unsupported-element-spec", Reason: "Element source uses an unsupported format."},
	})
	for path, expected := range map[string][]byte{malformedPath: malformedData, unsupportedPath: unsupportedData} {
		actual, readErr := os.ReadFile(path)
		if readErr != nil || string(actual) != string(expected) {
			t.Fatalf("authoritative source changed [%s]: %q, err=%v", path, actual, readErr)
		}
	}
}

func TestRebuildDiagnosesMissingElementSource(t *testing.T) {
	engine, _, missingPath, _ := newTwoItemFixtureEngine(t)
	if err := os.Remove(missingPath); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	due, err := engine.Query(context.Background(), Query{Kind: QueryElementSubset, Subset: "due"})
	if err != nil || len(due.Items) != 1 || due.Items[0].ElementID != fixtureElementID {
		t.Fatalf("remaining valid due Items = %#v, err=%v", due.Items, err)
	}
	diagnostics, err := engine.index.sourceDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	assertSourceDiagnostics(t, diagnostics, map[string]ElementSourceDiagnostic{
		secondFixtureElementID + ".sme": {
			SourcePath: secondFixtureElementID + ".sme",
			ElementID:  secondFixtureElementID,
			Code:       "missing-element-source",
			Reason:     "Element source is missing.",
		},
	})
	if _, err = os.Stat(missingPath); !os.IsNotExist(err) {
		t.Fatalf("missing authority was recreated: %v", err)
	}
}

func assertSourceDiagnostics(t *testing.T, actual []ElementSourceDiagnostic, expected map[string]ElementSourceDiagnostic) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("source diagnostics = %#v", actual)
	}
	for _, diagnostic := range actual {
		if want, ok := expected[diagnostic.SourcePath]; !ok || diagnostic != want {
			t.Fatalf("source diagnostic = %#v, expected=%#v", diagnostic, want)
		}
	}
}
