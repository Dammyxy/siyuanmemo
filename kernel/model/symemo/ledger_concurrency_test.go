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
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLedgerConcurrentSiblings(t *testing.T) {
	events := concurrentHistory(t)
	projections, _ := projectSchedulingEvents(events[:3])
	if projections[fixtureElementID].AdoptedTerminalID != "branch-b" {
		t.Fatalf("terminal = %s", projections[fixtureElementID].AdoptedTerminalID)
	}
}

func TestLedgerBranchExtension(t *testing.T) {
	projections, _ := projectSchedulingEvents(concurrentHistory(t))
	if projections[fixtureElementID].AdoptedTerminalID != "branch-a2" || projections[fixtureElementID].Repetitions != 3 {
		t.Fatalf("projection = %#v", projections[fixtureElementID])
	}
}

func TestLedgerInputOrderIndependence(t *testing.T) {
	events := concurrentHistory(t)
	expectedProjection, expectedDiagnostics := projectSchedulingEvents(events)
	expected, _ := json.Marshal(struct {
		Projection  map[string]SchedulingProjection
		Diagnostics []EventDiagnostic
	}{expectedProjection, expectedDiagnostics})
	for iteration := 0; iteration < 20; iteration++ {
		shuffled := append([]SchedulingEvent(nil), events...)
		rand.New(rand.NewSource(int64(iteration+1))).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		projection, diagnostics := projectSchedulingEvents(shuffled)
		actual, _ := json.Marshal(struct {
			Projection  map[string]SchedulingProjection
			Diagnostics []EventDiagnostic
		}{projection, diagnostics})
		if string(actual) != string(expected) {
			t.Fatalf("iteration %d differs\nactual=%s\nexpected=%s", iteration, actual, expected)
		}
	}
}

func TestLedgerDuplicateAndInvalidHistory(t *testing.T) {
	events := concurrentHistory(t)
	_, diagnostics := projectSchedulingEvents(events)
	byID := map[string]EventDiagnostic{}
	for _, diagnostic := range diagnostics {
		byID[diagnostic.EventID] = diagnostic
	}
	if byID["branch-b"].Classification != "duplicate" {
		t.Fatalf("duplicate = %#v", byID["branch-b"])
	}
	for id, reason := range map[string]string{"missing": "missing-base", "cross": "cross-item-base", "cycle-a": "cyclic-base", "incompatible": "incompatible-transition", "conflict": "conflicting-event-identity"} {
		if byID[id].Classification != "invalid" || byID[id].Reason != reason {
			t.Fatalf("%s = %#v", id, byID[id])
		}
	}
}

func TestLedgerRejectsMalformedKnownEvent(t *testing.T) {
	events := concurrentHistory(t)
	root := events[0]
	malformed := events[1]
	malformed.EventID = "malformed-projection-owner"
	malformed.OccurredAt = time.Date(2026, time.July, 20, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	malformed.After.ElementID = "other-item"
	malformed.After.AdoptedTerminalID = malformed.EventID

	projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{root, malformed})
	if projection := projections[fixtureElementID]; projection.AdoptedTerminalID != root.EventID {
		t.Fatalf("malformed event drove projection: %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, malformed.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != "projection-element-mismatch" {
		t.Fatalf("malformed diagnostic = %#v", diagnostic)
	}
}

func TestLedgerRejectsIncompleteKnownReviewEvents(t *testing.T) {
	root, review := acceptedReviewHistory(t)
	tests := []struct {
		name   string
		reason string
		mutate func(*SchedulingEvent)
	}{
		{name: "event spec", reason: "unsupported-event-spec", mutate: func(event *SchedulingEvent) { event.Spec = 99 }},
		{name: "session", reason: "review-session-missing", mutate: func(event *SchedulingEvent) { event.SessionID = "" }},
		{name: "raw grade", reason: "review-grade-missing", mutate: func(event *SchedulingEvent) { event.RawGrade = nil }},
		{name: "pass result", reason: "review-pass-mismatch", mutate: func(event *SchedulingEvent) { event.Passed = boolPointer(!*event.Passed) }},
		{name: "review kind", reason: "review-kind-invalid", mutate: func(event *SchedulingEvent) { event.ReviewKind = "" }},
		{name: "learning date", reason: "review-learning-date-invalid", mutate: func(event *SchedulingEvent) { event.LearningDate = "not-a-date" }},
		{name: "decision", reason: "review-decision-invalid", mutate: func(event *SchedulingEvent) { event.AlgorithmDecision.Winner = "" }},
		{name: "candidates", reason: "review-winner-candidate-missing", mutate: func(event *SchedulingEvent) { event.AlgorithmCandidates = nil }},
		{name: "active algorithm", reason: "review-active-algorithm-mismatch", mutate: func(event *SchedulingEvent) { event.After.ActiveAlgorithm = "other" }},
		{name: "algorithm state", reason: "review-winner-state-missing", mutate: func(event *SchedulingEvent) { delete(event.After.AlgorithmStates, event.AlgorithmDecision.Winner) }},
		{name: "last grade", reason: "review-after-grade-mismatch", mutate: func(event *SchedulingEvent) { event.After.LastRawGrade = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			malformed := cloneSchedulingEvent(t, review)
			test.mutate(&malformed)
			projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{root, malformed})
			if projection := projections[root.ElementID]; projection.AdoptedTerminalID != root.EventID {
				t.Fatalf("malformed event drove projection: %#v", projection)
			}
			if diagnostic := diagnosticByID(diagnostics, malformed.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != test.reason {
				t.Fatalf("malformed diagnostic = %#v", diagnostic)
			}
		})
	}
}

func TestLedgerInvalidKnownEventDoesNotBlockValidEnvelopeSibling(t *testing.T) {
	engine, config := newFixtureEngine(t)
	path := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file EventFile
	if err = json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	invalid := file.Events[0]
	invalid.Spec = 99
	invalid.EventID = "known-invalid-spec"
	invalid.After.AdoptedTerminalID = invalid.EventID
	file.Events = append(file.Events, invalid)
	writeTestJSON(t, path, file)

	if err = engine.refreshProjection(context.Background()); err != nil {
		t.Fatalf("compatible envelope refresh = %v", err)
	}
	projection, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil || projection.AdoptedTerminalID != file.Events[0].EventID {
		t.Fatalf("valid sibling projection = %#v, err=%v", projection, err)
	}
}

func TestLedgerBrokenEnvelopePreservesProjection(t *testing.T) {
	tests := []struct {
		name      string
		breakFile func(*testing.T, string)
	}{
		{name: "malformed", breakFile: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{"spec":`), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "not a file", breakFile: func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, config := newFixtureEngine(t)
			before, err := engine.ledger.Snapshot(fixtureElementID)
			if err != nil {
				t.Fatal(err)
			}
			test.breakFile(t, filepath.Join(config.ReviewsRoot(), "2026-07.smr"))
			err = engine.refreshProjection(context.Background())
			domainErr, ok := AsDomainError(err)
			if !ok || domainErr.Code != ErrHistoryRequiresRepair || domainErr.Cause == nil {
				t.Fatalf("broken envelope error = %#v", domainErr)
			}
			after, snapshotErr := engine.ledger.Snapshot(fixtureElementID)
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			beforeJSON, _ := json.Marshal(before)
			afterJSON, _ := json.Marshal(after)
			if string(beforeJSON) != string(afterJSON) {
				t.Fatalf("projection changed after broken envelope\nbefore=%s\nafter=%s", beforeJSON, afterJSON)
			}
		})
	}
}

func TestLedgerUnknownEventTypePreservesProjection(t *testing.T) {
	engine, config := newFixtureEngine(t)
	before, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file EventFile
	if err = json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	file.Events = append(file.Events, SchedulingEvent{
		Spec:       SupportedEventSpec,
		EventID:    "future-event",
		OccurredAt: time.Date(2026, time.July, 20, 12, 0, 0, 0, config.Location),
		Type:       "futureSchedulingAction",
		ElementID:  fixtureElementID,
	})
	writeTestJSON(t, path, file)

	if err = engine.refreshProjection(context.Background()); !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("unknown event refresh error = %v", err)
	}
	after, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(afterJSON) != string(beforeJSON) {
		t.Fatalf("projection changed after unknown event\nbefore=%s\nafter=%s", beforeJSON, afterJSON)
	}
}

func diagnosticByID(diagnostics []EventDiagnostic, eventID string) EventDiagnostic {
	for _, diagnostic := range diagnostics {
		if diagnostic.EventID == eventID {
			return diagnostic
		}
	}
	return EventDiagnostic{}
}

func concurrentHistory(t *testing.T) []SchedulingEvent {
	t.Helper()
	data, err := os.ReadFile("testdata/reviews/concurrent-cases.smr")
	if err != nil {
		t.Fatal(err)
	}
	var file EventFile
	if err = json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	completeConcurrentReviewFacts(file.Events)
	return file.Events
}

func completeConcurrentReviewFacts(events []SchedulingEvent) {
	state := VersionedAlgorithmState{Algorithm: simpleV1ID, SchemaVersion: 1, State: map[string]any{"fixture": true}}
	for i := range events {
		event := &events[i]
		if event.Type == "introduceElement" {
			event.After.ActiveAlgorithm = simpleV1ID
			event.After.AlgorithmStates = map[string]VersionedAlgorithmState{simpleV1ID: state}
			continue
		}
		rawGrade := 4
		passed := true
		event.SessionID = "fixture-session"
		event.ReviewKind = "scheduled"
		event.RawGrade = &rawGrade
		event.Passed = &passed
		event.RatingLabel = RatingGood
		event.RatingMapping = "supermemo-grade-v1"
		event.LearningDate = event.OccurredAt.Format("2006-01-02")
		event.Before.ElementID = event.ElementID
		if event.Before.LifecycleState == "" {
			event.Before.LifecycleState = "memorized"
		}
		event.Before.ActiveAlgorithm = simpleV1ID
		event.Before.AlgorithmStates = map[string]VersionedAlgorithmState{simpleV1ID: state}
		event.After.ActiveAlgorithm = simpleV1ID
		event.After.AlgorithmStates = map[string]VersionedAlgorithmState{simpleV1ID: state}
		event.After.LastReviewAt = timePointer(event.OccurredAt)
		event.After.LastLearningDate = event.LearningDate
		event.After.LastRawGrade = &rawGrade
		event.After.LastPassed = &passed
		event.AlgorithmDecision = AlgorithmDecision{Policy: "primary", Winner: simpleV1ID, EnabledAlgorithms: []string{simpleV1ID}}
		event.AlgorithmCandidates = []AlgorithmCandidate{{Algorithm: simpleV1ID, AlgorithmVersion: "1", StateSchemaVersion: 1, Status: "valid", NextIntervalDays: event.After.IntervalDays, NextDueAt: event.After.DueAt, NextState: state}}
	}
	var branchA SchedulingProjection
	for _, event := range events {
		if event.EventID == "branch-a" {
			branchA = event.After
			break
		}
	}
	for i := range events {
		if events[i].EventID == "branch-a2" {
			events[i].Before = branchA
		}
	}
}

func acceptedReviewHistory(t *testing.T) (SchedulingEvent, SchedulingEvent) {
	t.Helper()
	engine, config := newFixtureEngine(t)
	_, _ = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	_, _ = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID})
	grade := 4
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "validation-review"}); err != nil {
		t.Fatal(err)
	}
	events, err := config.LoadEventFiles()
	if err != nil || len(events) != 2 {
		t.Fatalf("accepted history = %#v, err=%v", events, err)
	}
	return events[0], events[1]
}

func cloneSchedulingEvent(t *testing.T, event SchedulingEvent) SchedulingEvent {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var cloned SchedulingEvent
	if err = json.Unmarshal(data, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}
