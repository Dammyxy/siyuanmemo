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
	"reflect"
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

func TestRebuildPreservesRecordedLearningDayAfterConfigurationChange(t *testing.T) {
	current := time.Date(2026, time.July, 20, 2, 0, 0, 0, time.UTC)
	config := copyFixtureWorkspace(t)
	config.Location = time.UTC
	config.Now = func() time.Time { return current }
	installDailyLearningConfig(t, config, "UTC", 4)
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	event := modernItemIntroductionEvent(t, config, fixtureElementID, "20260720020000-recorded-day", 0, 4)
	writeTestJSON(t, reviewPath, EventFile{Spec: 1, Month: "2026-07", Events: []SchedulingEvent{event}})
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(config.SchedulerRoot, "learning-day.json"), LearningDayConfigV1{Spec: 1, TimeZoneIANA: "Asia/Shanghai", MidnightShiftHours: 4})
	removeSQLiteFiles(config.IndexPath())
	rebuilt, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rebuilt.Close() })
	after, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("projection rebuild rewrote accepted history after Learning Day configuration change")
	}
	replayed := eventByID(t, mustEvents(t, config), event.EventID)
	projection, err := rebuilt.ledger.Snapshot(fixtureElementID)
	if err != nil || replayed.LearningDayID != event.LearningDayID || replayed.LearningDate != event.LearningDate || projection.LastLearningDate != event.LearningDayID {
		t.Fatalf("rebuilt recorded day changed: event=%#v projection=%#v err=%v", replayed, projection, err)
	}
}

func TestExpiredFinalDrillGenerationRejectsDelayedActivityAndStartsFresh(t *testing.T) {
	current := time.Date(2026, time.July, 19, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	config, _ := finalDrillFixtureConfig(t, 1, func() time.Time { return current })
	admission := mustEvents(t, config)[0]
	normalized, err := NormalizeGrade(2)
	if err != nil {
		t.Fatal(err)
	}
	delayed := SchedulingEvent{
		Spec:                  SupportedEventSpec,
		EventID:               "20260720090000-delayed-old-generation",
		OccurredAt:            current.AddDate(0, 0, 1),
		Type:                  "drillElement",
		ElementID:             admission.ElementID,
		SessionID:             "delayed-session",
		ReviewKind:            "drillGrade",
		RawGrade:              intPointer(2),
		Passed:                boolPointer(normalized.Passed),
		RatingLabel:           normalized.RatingLabel,
		RatingMapping:         normalized.RatingMapping,
		LearningDate:          "2026-07-20",
		LearningDayID:         "2026-07-20",
		DrillEffect:           "retain",
		DrillAdmissionEventID: admission.EventID,
		Before:                admission.After,
		After:                 admission.After,
	}
	_, drill, diagnostics := projectSchedulingTruth([]SchedulingEvent{admission, delayed}, "2026-07-20")
	old := drill[admission.ElementID]
	if old.Member || !old.Expired || old.AdoptedTerminalEventID != "" {
		t.Fatalf("delayed activity revived expired generation: %#v", old)
	}
	if diagnostic := diagnosticByID(diagnostics, delayed.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != "drill-member-missing" {
		t.Fatalf("delayed activity diagnostic = %#v", diagnostic)
	}

	const newID = "20260722050101-newgene"
	newConfig := config
	newConfig.Now = func() time.Time { return current.AddDate(0, 0, 4) }
	newAdmission := modernItemIntroductionEvent(t, newConfig, newID, "20260723090000-new-generation", 1, 3)
	_, drill, diagnostics = projectSchedulingTruth([]SchedulingEvent{admission, delayed, newAdmission}, "2026-07-23")
	if old = drill[admission.ElementID]; old.Member || !old.Expired {
		t.Fatalf("new generation implicitly restored former member: %#v", old)
	}
	if fresh := drill[newID]; !fresh.Member || fresh.Expired || fresh.AdmissionEventID != newAdmission.EventID {
		t.Fatalf("fresh generation = %#v", fresh)
	}
	if diagnostic := diagnosticByID(diagnostics, delayed.EventID); diagnostic.Classification != "invalid" {
		t.Fatalf("new generation reclassified delayed activity: %#v", diagnostic)
	}
}

func TestRebuildTwentyTimesProducesIdenticalLearningTruth(t *testing.T) {
	config, ids := finalDrillFixtureConfig(t, 2, nil)
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	combined := concurrentHistory(t)
	for _, event := range file.Events {
		if event.ElementID == ids[1] {
			combined = append(combined, event)
		}
	}
	file.Events = combined
	writeTestJSON(t, reviewPath, file)

	expected := ""
	for iteration := 0; iteration < 20; iteration++ {
		removeSQLiteFiles(config.IndexPath())
		engine, err := NewEngine(t.Context(), config)
		if err != nil {
			t.Fatalf("rebuild %d: %v", iteration, err)
		}
		snapshot, err := engine.index.snapshot()
		if err != nil {
			_ = engine.Close()
			t.Fatal(err)
		}
		plan, err := engine.scheduler.BuildLearningPlan(t.Context())
		if err != nil {
			_ = engine.Close()
			t.Fatal(err)
		}
		actual, err := canonicalHash(struct {
			Snapshot projectionSnapshot
			Plan     learningPlan
		}{Snapshot: snapshot, Plan: plan})
		if closeErr := engine.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			expected = actual
			continue
		}
		if actual != expected {
			t.Fatalf("rebuild %d differs: got %s, want %s", iteration, actual, expected)
		}
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

func TestCorruptProjectionPayloadDoesNotBlockAuthoritativeRebuild(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	corruptProjectionPayload(t, engine)
	if err := engine.refreshProjection(t.Context()); err != nil {
		t.Fatalf("rebuild from corrupt projection payload: %v", err)
	}
	projection, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil || projection.AdoptedTerminalID != "20260716080000-intro001" {
		t.Fatalf("rebuilt projection = %#v, err=%v", projection, err)
	}
}

func TestProjectionSnapshotPersistsLearningDayAndSeparateDrillProjection(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	projection := SchedulingProjection{
		ElementID:            fixtureElementID,
		ScheduleProfile:      fsrsV1ID,
		AcceptedReviewAction: "GradeItem",
		LifecycleState:       "memorized",
		AdoptedTerminalID:    "projection-fields",
		DueAt:                time.Date(2026, time.July, 25, 8, 0, 0, 0, engine.config.Location),
		DueLearningDay:       "2026-07-25",
		IntervalDays:         6,
		Repetitions:          2,
		PriorityPosition:     3,
	}
	finalDrill := FinalDrillProjection{ElementID: fixtureElementID, Member: true, LastActivityDay: "2026-07-19", AdmissionEventID: "projection-fields", AdoptedTerminalEventID: "drill-terminal"}
	if err := engine.index.replaceAll(context.Background(), projectionBuild{
		Elements:              map[string]Element{fixtureElementID: mustElement(t, engine)},
		Projections:           map[string]SchedulingProjection{fixtureElementID: projection},
		FinalDrillProjections: map[string]FinalDrillProjection{fixtureElementID: finalDrill},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := engine.index.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	got := snapshot.Projections[fixtureElementID]
	gotDrill := snapshot.FinalDrillProjections[fixtureElementID]
	if got.DueLearningDay != projection.DueLearningDay || gotDrill != finalDrill {
		t.Fatalf("projection fields were not persisted: formal=%#v Drill=%#v", got, gotDrill)
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
		clock := time.Date(2026, time.July, 19, 3, 59, 0, 0, config.Location)
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
	if err := engine.refreshProjection(context.Background()); err != nil {
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

func TestProjectionDeduplicatesSourceDiagnostics(t *testing.T) {
	config := copyFixtureWorkspace(t)
	rootID := "20260720020401-diagdup"
	duplicateID := "20260720020402-baddupe"
	blocked := Element{
		Spec:            SupportedElementSpec,
		ID:              duplicateID,
		Type:            "topic",
		Title:           "Duplicate blocked material",
		ProcessingState: "reading",
		PayloadSpec:     SupportedPayloadSpec,
		Payload:         ElementPayload{Material: &TopicMaterial{Kind: "siyuanBlock", BlockID: "invalid"}},
	}
	root := Element{
		Spec:            SupportedElementSpec,
		ID:              rootID,
		Type:            "topic",
		Title:           "Diagnostic duplicates",
		ProcessingState: "reading",
		PayloadSpec:     SupportedPayloadSpec,
		Payload:         ElementPayload{Material: &TopicMaterial{Kind: "html", HTML: "<p>root</p>"}},
		Children:        []Element{blocked, blocked},
	}
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), rootID+".sme"), root)
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatalf("complete projection publication failed on duplicate diagnostics: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	diagnostics, err := engine.index.sourceDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, diagnostic := range diagnostics {
		key := diagnostic.SourcePath + "\x00" + diagnostic.Code + "\x00" + diagnostic.ElementID
		if seen[key] {
			t.Fatalf("duplicate diagnostic key was published: %#v", diagnostic)
		}
		seen[key] = true
	}
	materialKey := rootID + ".sme\x00" + materialInvalidBlock + "\x00" + duplicateID
	if !seen[materialKey] {
		t.Fatalf("deduplicated material diagnostic missing: %#v", diagnostics)
	}
}

func assertSourceDiagnostics(t *testing.T, actual []ElementSourceDiagnostic, expected map[string]ElementSourceDiagnostic) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("source diagnostics = %#v", actual)
	}
	for _, diagnostic := range actual {
		if want, ok := expected[diagnostic.SourcePath]; !ok || !reflect.DeepEqual(diagnostic, want) {
			t.Fatalf("source diagnostic = %#v, expected=%#v", diagnostic, want)
		}
	}
}
