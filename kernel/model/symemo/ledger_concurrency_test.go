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
	"bytes"
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
		{name: "modern scheduled discriminator", reason: "invalid-legacy-scheduled-review", mutate: func(event *SchedulingEvent) { event.ReviewKind = "scheduled" }},
		{name: "learning date", reason: "review-learning-date-invalid", mutate: func(event *SchedulingEvent) { event.LearningDate = "not-a-date" }},
		{name: "decision", reason: "review-decision-invalid", mutate: func(event *SchedulingEvent) { event.AlgorithmDecision.Winner = "" }},
		{name: "candidates", reason: "review-winner-candidate-missing", mutate: func(event *SchedulingEvent) { event.AlgorithmCandidates = nil }},
		{name: "active algorithm", reason: "review-active-algorithm-mismatch", mutate: func(event *SchedulingEvent) { event.After.ActiveAlgorithm = "other" }},
		{name: "schedule profile", reason: "review-schedule-profile-invalid", mutate: func(event *SchedulingEvent) {
			event.After.ScheduleProfile = topicAFactorV1ID
			event.After.AcceptedReviewAction = "NextTopic"
		}},
		{name: "algorithm state", reason: "review-winner-state-missing", mutate: func(event *SchedulingEvent) { delete(event.After.AlgorithmStates, event.AlgorithmDecision.Winner) }},
		{name: "last grade", reason: "review-after-grade-mismatch", mutate: func(event *SchedulingEvent) { event.After.LastRawGrade = nil }},
		{name: "due learning day", reason: "review-due-learning-day-invalid", mutate: func(event *SchedulingEvent) { event.After.DueLearningDay = "2026-08-01" }},
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

func TestLedgerAcceptsFeature001ScheduledReview(t *testing.T) {
	root, review := acceptedReviewHistory(t)
	legacy := cloneSchedulingEvent(t, review)
	legacy.ReviewKind = "scheduled"
	legacy.LearningDayID = ""
	legacy.After.ScheduleProfile = ""
	legacy.After.AcceptedReviewAction = ""
	legacy.After.DueLearningDay = ""

	projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{root, legacy})
	if projection := projections[root.ElementID]; projection.AdoptedTerminalID != legacy.EventID {
		t.Fatalf("Feature 001 review was not adopted: projection=%#v diagnostics=%#v", projection, diagnostics)
	}
}

func TestLedgerRejectsLegacyScheduledReviewAfterModernParent(t *testing.T) {
	root, parent := acceptedReviewAt(t, time.Date(2026, time.July, 19, 9, 0, 0, 0, time.UTC), 2, "modern-low-parent")
	child := modernItemReviewEvent(t, parent.After, parent.OccurredAt.Add(time.Hour), 2, "legacy-shaped-child")
	child.ReviewKind = "scheduled"
	child.LearningDayID = ""
	child.After.ScheduleProfile = ""
	child.After.AcceptedReviewAction = ""
	child.After.DueLearningDay = ""
	child.DrillEffect = ""

	projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{root, parent, child}, "2026-07-19")
	if projection := projections[root.ElementID]; projection.AdoptedTerminalID != parent.EventID {
		t.Fatalf("legacy-shaped child drove modern projection: %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, child.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != "invalid-legacy-scheduled-review" {
		t.Fatalf("legacy-shaped child diagnostic = %#v", diagnostic)
	}
}

func TestLedgerRejectsCausalTransitionWithDifferentAlgorithmState(t *testing.T) {
	root, review := acceptedReviewHistory(t)
	tampered := cloneSchedulingEvent(t, review)
	state := tampered.Before.AlgorithmStates[simpleV1ID]
	state.State = map[string]any{"tampered": true}
	tampered.Before.AlgorithmStates[simpleV1ID] = state

	projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{root, tampered})
	if projection := projections[root.ElementID]; projection.AdoptedTerminalID != root.EventID {
		t.Fatalf("incompatible algorithm state drove projection: %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, tampered.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != "review-candidate-transition-invalid" {
		t.Fatalf("incompatible algorithm state diagnostic = %#v", diagnostic)
	}
}

func TestLedgerRejectsItemEventDrivenByTopicOnlyCandidate(t *testing.T) {
	root, review := acceptedReviewHistory(t)
	malformed := cloneSchedulingEvent(t, review)
	topicState := VersionedAlgorithmState{
		Algorithm:     topicAFactorV1ID,
		SchemaVersion: 1,
		State: TopicAFactorV1State{
			IntervalDays: malformed.After.IntervalDays,
			Repetitions:  1,
		},
	}
	malformed.AlgorithmDecision = AlgorithmDecision{Policy: "primary", Winner: topicAFactorV1ID, EnabledAlgorithms: []string{topicAFactorV1ID}}
	malformed.AlgorithmCandidates = []AlgorithmCandidate{{
		Algorithm:          topicAFactorV1ID,
		AlgorithmVersion:   "1",
		StateSchemaVersion: 1,
		Status:             "valid",
		NextIntervalDays:   malformed.After.IntervalDays,
		NextDueAt:          malformed.After.DueAt,
		NextState:          topicState,
	}}
	malformed.After.ActiveAlgorithm = topicAFactorV1ID
	malformed.After.AlgorithmStates = map[string]VersionedAlgorithmState{topicAFactorV1ID: topicState}

	projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{root, malformed})
	if projection := projections[root.ElementID]; projection.AdoptedTerminalID != root.EventID {
		t.Fatalf("Topic-only candidate drove Item projection: %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, malformed.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != "review-winner-target-kind-invalid" {
		t.Fatalf("Topic-only Item candidate diagnostic = %#v", diagnostic)
	}
}

func TestLedgerRejectsInvalidItemCandidateFacts(t *testing.T) {
	root, review := acceptedReviewHistory(t)
	tests := []struct {
		name   string
		reason string
		mutate func(*SchedulingEvent)
	}{
		{
			name:   "FSRS algorithm version",
			reason: "review-winner-candidate-invalid",
			mutate: func(event *SchedulingEvent) {
				event.AlgorithmCandidates[0].AlgorithmVersion = "2"
			},
		},
		{
			name:   "FSRS difficulty range",
			reason: "review-winner-candidate-invalid",
			mutate: func(event *SchedulingEvent) {
				candidate := &event.AlgorithmCandidates[0]
				difficulty := 11.0
				candidate.Difficulty = &difficulty
				state, err := decodeAlgorithmState[FSRSV1State](candidate.NextState, fsrsV1ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				state.Difficulty = difficulty
				candidate.NextState.State = state
				event.After.AlgorithmStates[fsrsV1ID] = candidate.NextState
			},
		},
		{
			name:   "missing shadow candidate",
			reason: "review-candidate-set-invalid",
			mutate: func(event *SchedulingEvent) {
				event.AlgorithmCandidates = event.AlgorithmCandidates[:1]
				event.AlgorithmDecision.EnabledAlgorithms = event.AlgorithmDecision.EnabledAlgorithms[:1]
			},
		},
		{
			name:   "candidate order",
			reason: "review-candidate-set-invalid",
			mutate: func(event *SchedulingEvent) {
				event.AlgorithmCandidates[0], event.AlgorithmCandidates[1] = event.AlgorithmCandidates[1], event.AlgorithmCandidates[0]
			},
		},
		{
			name:   "shadow state missing",
			reason: "review-candidate-state-missing",
			mutate: func(event *SchedulingEvent) {
				delete(event.After.AlgorithmStates, simpleV1ID)
			},
		},
		{
			name:   "shadow state mismatch",
			reason: "review-candidate-state-mismatch",
			mutate: func(event *SchedulingEvent) {
				event.After.AlgorithmStates[simpleV1ID] = event.Before.AlgorithmStates[simpleV1ID]
			},
		},
		{
			name:   "non-Item candidate",
			reason: "review-candidate-target-kind-invalid",
			mutate: func(event *SchedulingEvent) {
				event.AlgorithmCandidates = append(event.AlgorithmCandidates, AlgorithmCandidate{
					Algorithm: topicAFactorV1ID, AlgorithmVersion: "1", StateSchemaVersion: 1, Status: "error",
				})
			},
		},
		{
			name:   "winner state transition",
			reason: "review-candidate-transition-invalid",
			mutate: func(event *SchedulingEvent) {
				for i := range event.AlgorithmCandidates {
					candidate := &event.AlgorithmCandidates[i]
					if candidate.Algorithm != event.AlgorithmDecision.Winner {
						continue
					}
					switch candidate.Algorithm {
					case fsrsV1ID:
						state, err := decodeAlgorithmState[FSRSV1State](candidate.NextState, fsrsV1ID, 1)
						if err != nil {
							t.Fatal(err)
						}
						state.Repetitions++
						candidate.NextState.State = state
					case simpleV1ID:
						state, err := decodeAlgorithmState[SimpleV1State](candidate.NextState, simpleV1ID, 1)
						if err != nil {
							t.Fatal(err)
						}
						state.Repetitions++
						candidate.NextState.State = state
					}
					event.After.AlgorithmStates[candidate.Algorithm] = candidate.NextState
				}
			},
		},
		{
			name:   "FSRS scheduled interval",
			reason: "review-candidate-transition-invalid",
			mutate: func(event *SchedulingEvent) {
				candidate := &event.AlgorithmCandidates[0]
				state, err := decodeAlgorithmState[FSRSV1State](candidate.NextState, fsrsV1ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				state.ScheduledDays++
				candidate.NextState.State = state
				event.After.AlgorithmStates[fsrsV1ID] = candidate.NextState
			},
		},
		{
			name:   "Simple scheduled interval",
			reason: "review-candidate-transition-invalid",
			mutate: func(event *SchedulingEvent) {
				candidate := &event.AlgorithmCandidates[1]
				state, err := decodeAlgorithmState[SimpleV1State](candidate.NextState, simpleV1ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				state.IntervalDays++
				candidate.NextState.State = state
				event.After.AlgorithmStates[simpleV1ID] = candidate.NextState
			},
		},
		{
			name:   "Simple formula",
			reason: "review-candidate-transition-invalid",
			mutate: func(event *SchedulingEvent) {
				candidate := &event.AlgorithmCandidates[1]
				candidate.NextIntervalDays++
				candidate.NextDueAt = event.OccurredAt.AddDate(0, 0, candidate.NextIntervalDays)
				state, err := decodeAlgorithmState[SimpleV1State](candidate.NextState, simpleV1ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				state.IntervalDays = candidate.NextIntervalDays
				state.DueAt = timePointer(candidate.NextDueAt)
				candidate.NextState.State = state
				event.After.AlgorithmStates[simpleV1ID] = candidate.NextState
			},
		},
		{
			name:   "FSRS last review time",
			reason: "review-candidate-transition-invalid",
			mutate: func(event *SchedulingEvent) {
				candidate := &event.AlgorithmCandidates[0]
				state, err := decodeAlgorithmState[FSRSV1State](candidate.NextState, fsrsV1ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				state.LastReviewAt = state.LastReviewAt.Add(time.Hour)
				candidate.NextState.State = state
				event.After.AlgorithmStates[fsrsV1ID] = candidate.NextState
			},
		},
		{
			name:   "FSRS unexpected lapse",
			reason: "review-candidate-transition-invalid",
			mutate: func(event *SchedulingEvent) {
				candidate := &event.AlgorithmCandidates[0]
				state, err := decodeAlgorithmState[FSRSV1State](candidate.NextState, fsrsV1ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				state.Lapses++
				candidate.NextState.State = state
				event.After.AlgorithmStates[fsrsV1ID] = candidate.NextState
				event.After.Lapses++
			},
		},
		{
			name:   "Simple last review time",
			reason: "review-candidate-transition-invalid",
			mutate: func(event *SchedulingEvent) {
				candidate := &event.AlgorithmCandidates[1]
				state, err := decodeAlgorithmState[SimpleV1State](candidate.NextState, simpleV1ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				changed := state.LastReviewAt.Add(time.Hour)
				state.LastReviewAt = &changed
				candidate.NextState.State = state
				event.After.AlgorithmStates[simpleV1ID] = candidate.NextState
			},
		},
		{
			name:   "Simple unexpected lapse",
			reason: "review-candidate-transition-invalid",
			mutate: func(event *SchedulingEvent) {
				candidate := &event.AlgorithmCandidates[1]
				state, err := decodeAlgorithmState[SimpleV1State](candidate.NextState, simpleV1ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				state.Lapses++
				candidate.NextState.State = state
				event.After.AlgorithmStates[simpleV1ID] = candidate.NextState
			},
		},
		{
			name:   "winner projection repetitions",
			reason: "review-winner-projection-state-mismatch",
			mutate: func(event *SchedulingEvent) { event.After.Repetitions++ },
		},
		{
			name:   "winner projection lapses",
			reason: "review-winner-projection-state-mismatch",
			mutate: func(event *SchedulingEvent) { event.After.Lapses++ },
		},
		{
			name:   "valid primary mislabeled invalid",
			reason: "review-candidate-status-invalid",
			mutate: func(event *SchedulingEvent) {
				primary := &event.AlgorithmCandidates[0]
				primary.Status = "invalid"
				primary.ValidationReason = "forged-invalid"
				fallback := event.AlgorithmCandidates[1]
				event.AlgorithmDecision.Policy = "fallback"
				event.AlgorithmDecision.Winner = fallback.Algorithm
				event.AlgorithmDecision.FallbackReason = primary.ValidationReason
				event.After.ActiveAlgorithm = fallback.Algorithm
				event.After.IntervalDays = fallback.NextIntervalDays
				event.After.DueAt = fallback.NextDueAt
				event.After.DueLearningDay = addLearningDays(eventLearningDayID(*event), fallback.NextIntervalDays)
			},
		},
		{
			name:   "fallback selected over valid primary",
			reason: "review-decision-invalid",
			mutate: func(event *SchedulingEvent) {
				fallback := event.AlgorithmCandidates[1]
				event.AlgorithmDecision.Policy = "fallback"
				event.AlgorithmDecision.Winner = fallback.Algorithm
				event.AlgorithmDecision.FallbackReason = "forged"
				event.After.ActiveAlgorithm = fallback.Algorithm
				event.After.IntervalDays = fallback.NextIntervalDays
				event.After.DueAt = fallback.NextDueAt
				event.After.DueLearningDay = fallback.NextDueAt.Format("2006-01-02")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			malformed := cloneSchedulingEvent(t, review)
			test.mutate(&malformed)
			projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{root, malformed})
			if projection := projections[root.ElementID]; projection.AdoptedTerminalID != root.EventID {
				t.Fatalf("malformed candidate drove projection: %#v", projection)
			}
			if diagnostic := diagnosticByID(diagnostics, malformed.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != test.reason {
				t.Fatalf("candidate diagnostic = %#v", diagnostic)
			}
		})
	}
}

func TestRebuildPreservesRecordedFSRSOutputWithoutCurrentAdapterEvaluation(t *testing.T) {
	engine, config := newFixtureEngine(t)
	if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	grade := 5
	const eventID = "20260722060101-recorded-fsrs"
	if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: eventID}); err != nil {
		t.Fatal(err)
	}
	recorded := eventByID(t, mustEvents(t, config), eventID)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	changed := defaultFSRSV1SchedulerConfig()
	changed.RequestRetention = 0.99
	changed.MaximumIntervalDays = 1
	fresh := evaluateAlgorithmAdapter(NewFSRSV1Adapter(changed), reviewAlgorithmInput(recorded, "element.item", fsrsV1ID))
	if fresh.Status != "valid" || fresh.NextIntervalDays == recorded.AlgorithmCandidates[0].NextIntervalDays {
		t.Fatalf("fresh changed-configuration FSRS result = %#v, recorded=%#v", fresh, recorded.AlgorithmCandidates[0])
	}
	writeTestJSON(t, filepath.Join(config.SchedulerRoot, fsrsV1ID+".json"), changed)
	removeSQLiteFiles(config.IndexPath())
	rebuilt, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rebuilt.Close() })
	projection, err := rebuilt.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	wantHash, err := canonicalHash(recorded.After)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, err := canonicalHash(projection)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != wantHash {
		t.Fatalf("rebuild recalculated recorded FSRS output: got=%#v want=%#v", projection, recorded.After)
	}
	probe := &replayProbeFSRSAdapter{}
	rebuilt.scheduler.arena.primary = probe
	if err = rebuilt.refreshProjection(t.Context()); err != nil {
		t.Fatal(err)
	}
	if probe.calls != 0 {
		t.Fatalf("FSRS replay invoked the current Adapter lifecycle %d times", probe.calls)
	}
}

type replayProbeFSRSAdapter struct {
	calls int
}

func (adapter *replayProbeFSRSAdapter) Describe() AlgorithmDescriptor {
	return NewFSRSV1Adapter(defaultFSRSV1SchedulerConfig()).Describe()
}

func (adapter *replayProbeFSRSAdapter) Initialize(AlgorithmInput) (VersionedAlgorithmState, error) {
	adapter.calls++
	return VersionedAlgorithmState{}, nil
}

func (adapter *replayProbeFSRSAdapter) Predict(AlgorithmInput) (Prediction, error) {
	adapter.calls++
	return Prediction{}, nil
}

func (adapter *replayProbeFSRSAdapter) Review(AlgorithmInput) (AlgorithmCandidate, error) {
	adapter.calls++
	return AlgorithmCandidate{}, nil
}

func (adapter *replayProbeFSRSAdapter) Migrate(state VersionedAlgorithmState) (VersionedAlgorithmState, error) {
	adapter.calls++
	return state, nil
}

func TestLedgerRejectsInvalidTopicCandidateTransition(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	addDueDailyTopic(t, config, dailyTopicID, "Due Topic", "2026-07-19", 1)
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil {
		t.Fatal(err)
	}
	if started.Session == nil || started.Session.Current == nil {
		t.Fatalf("Start = %#v", started.Session)
	}
	if started.Session.Current.ElementID == fixtureElementID {
		if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
			t.Fatal(err)
		}
		grade := 4
		if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "before-topic-transition"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionNextTopic, ElementID: dailyTopicID, EventID: "topic-transition"}); err != nil {
		t.Fatal(err)
	}
	baseEvents := mustEvents(t, config)
	valid := eventByID(t, baseEvents, "topic-transition")
	tests := []struct {
		name   string
		reason string
		mutate func(*SchedulingEvent, *TopicAFactorV1State)
	}{
		{name: "repetitions", reason: "topic-state-invalid", mutate: func(_ *SchedulingEvent, state *TopicAFactorV1State) { state.Repetitions++ }},
		{name: "last learning day", reason: "topic-candidate-transition-invalid", mutate: func(_ *SchedulingEvent, state *TopicAFactorV1State) { state.LastLearningDay = "2026-07-18" }},
		{name: "state due learning day", reason: "topic-candidate-transition-invalid", mutate: func(_ *SchedulingEvent, state *TopicAFactorV1State) { state.DueLearningDay = "2026-07-25" }},
		{name: "effective A-Factor", reason: "topic-interval-formula-invalid", mutate: func(event *SchedulingEvent, state *TopicAFactorV1State) {
			event.TopicEffectiveAFactor = 3
			state.EffectiveAFactor = 3
		}},
		{name: "previous interval formula", reason: "topic-review-policy-invalid", mutate: func(event *SchedulingEvent, _ *TopicAFactorV1State) { event.TopicPreviousIntervalDays++ }},
		{name: "projection due learning day", reason: "topic-due-learning-day-invalid", mutate: func(event *SchedulingEvent, _ *TopicAFactorV1State) { event.After.DueLearningDay = "2026-07-25" }},
		{name: "projection repetitions", reason: "topic-repetition-invalid", mutate: func(event *SchedulingEvent, state *TopicAFactorV1State) {
			event.After.Repetitions++
			state.Repetitions++
		}},
		{name: "projection lapse", reason: "topic-item-memory-state-invalid", mutate: func(event *SchedulingEvent, _ *TopicAFactorV1State) { event.After.Lapses++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			malformed := cloneSchedulingEvent(t, valid)
			state, err := decodeAlgorithmState[TopicAFactorV1State](malformed.AlgorithmCandidates[0].NextState, topicAFactorV1ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&malformed, &state)
			malformed.AlgorithmCandidates[0].NextState.State = state
			malformed.After.AlgorithmStates[topicAFactorV1ID] = malformed.AlgorithmCandidates[0].NextState
			events := append([]SchedulingEvent(nil), baseEvents...)
			for i := range events {
				if events[i].EventID == malformed.EventID {
					events[i] = malformed
				}
			}
			projections, diagnostics := projectSchedulingEvents(events)
			if projection := projections[dailyTopicID]; projection.AdoptedTerminalID == malformed.EventID {
				t.Fatalf("invalid Topic transition drove projection: %#v", projection)
			}
			if diagnostic := diagnosticByID(diagnostics, malformed.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != test.reason {
				t.Fatalf("Topic transition diagnostic = %#v", diagnostic)
			}
		})
	}
}

func TestLedgerRejectsIntroductionWithoutCleanPendingState(t *testing.T) {
	item := acceptedPendingItemIntroductionEvent(t)
	topic := acceptedPendingTopicIntroductionEvent(t)
	tests := []struct {
		name   string
		event  SchedulingEvent
		reason string
		mutate func(*SchedulingEvent)
	}{
		{name: "dismissed Topic", event: topic, reason: "invalid-introduction-transition", mutate: func(event *SchedulingEvent) { event.Before.LifecycleState = "dismissed" }},
		{name: "ghost Item terminal", event: item, reason: "invalid-introduction-transition", mutate: func(event *SchedulingEvent) { event.Before.AdoptedTerminalID = "ghost-terminal" }},
		{name: "ghost Item algorithm state", event: item, reason: "invalid-introduction-transition", mutate: func(event *SchedulingEvent) { event.Before.AlgorithmStates = event.After.AlgorithmStates }},
		{name: "empty Item discriminator bypass", event: item, reason: "invalid-legacy-introduction", mutate: func(event *SchedulingEvent) { event.ReviewKind = "" }},
		{name: "empty Topic discriminator bypass", event: topic, reason: "invalid-legacy-introduction", mutate: func(event *SchedulingEvent) { event.ReviewKind = "" }},
		{name: "old discriminator bypass", event: item, reason: "review-kind-invalid", mutate: func(event *SchedulingEvent) {
			event.ReviewKind = "introduceElement"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			malformed := cloneSchedulingEvent(t, test.event)
			test.mutate(&malformed)
			projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{malformed})
			if projection := projections[malformed.ElementID]; projection.ElementID != "" {
				t.Fatalf("invalid introduction drove projection: %#v", projection)
			}
			if diagnostic := diagnosticByID(diagnostics, malformed.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != test.reason {
				t.Fatalf("introduction diagnostic = %#v", diagnostic)
			}
		})
	}
}

func TestLedgerRejectsInvalidDrillFacts(t *testing.T) {
	config, _ := finalDrillFixtureConfig(t, 1, nil)
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil || started.Session.Confirmation == nil {
		t.Fatalf("start = %#v, err=%v", started.Session, err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	grade := 4
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: fixtureElementID, RawGrade: &grade, EventID: "valid-drill"}); err != nil {
		t.Fatal(err)
	}
	events := mustEvents(t, config)
	valid := eventByID(t, events, "valid-drill")
	tests := []struct {
		name   string
		reason string
		base   func() []SchedulingEvent
		mutate func(*SchedulingEvent)
	}{
		{name: "formal schedule mutation", reason: "drill-schedule-changed", mutate: func(event *SchedulingEvent) { event.After.IntervalDays++ }},
		{name: "grade effect mismatch", reason: "drill-effect-grade-mismatch", mutate: func(event *SchedulingEvent) { event.DrillEffect = "retain" }},
		{name: "non-Item target", reason: "drill-target-kind-invalid", mutate: func(event *SchedulingEvent) {
			event.Before.ScheduleProfile = topicAFactorV1ID
			event.Before.AcceptedReviewAction = "NextTopic"
			event.After.ScheduleProfile = topicAFactorV1ID
			event.After.AcceptedReviewAction = "NextTopic"
		}},
		{name: "non-admission root", reason: "drill-admission-invalid", base: func() []SchedulingEvent { return nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			malformed := cloneSchedulingEvent(t, valid)
			malformed.EventID = "malformed-drill-" + test.name
			if test.mutate != nil {
				test.mutate(&malformed)
			}
			base := append([]SchedulingEvent(nil), events[:len(events)-1]...)
			if test.base != nil {
				base = test.base()
			}
			_, diagnostics := projectSchedulingEvents(append(base, malformed), "2026-07-19")
			if diagnostic := diagnosticByID(diagnostics, malformed.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != test.reason {
				t.Fatalf("Drill diagnostic = %#v", diagnostic)
			}
		})
	}
}

func TestDrillGradesRecordSeparateCausalFacts(t *testing.T) {
	config, _ := finalDrillFixtureConfig(t, 1, nil)
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil || started.Session.Confirmation == nil {
		t.Fatalf("start = %#v, err=%v", started.Session, err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	initialDrill, err := engine.ledger.FinalDrillSnapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	grade := 2
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: fixtureElementID, RawGrade: &grade, EventID: "drill-causal-first"}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: fixtureElementID, RawGrade: &grade, EventID: "drill-causal-second"}); err != nil {
		t.Fatal(err)
	}

	events := mustEvents(t, config)
	first := eventByID(t, events, "drill-causal-first")
	second := eventByID(t, events, "drill-causal-second")
	firstFacts := eventJSONFacts(t, first)
	secondFacts := eventJSONFacts(t, second)
	admissionID := initialDrill.AdmissionEventID
	if firstFacts["drillAdmissionEventId"] != admissionID || firstFacts["baseDrillEventId"] != nil {
		t.Fatalf("first Drill causal facts = %#v, admission=%q", firstFacts, admissionID)
	}
	if secondFacts["drillAdmissionEventId"] != admissionID || secondFacts["baseDrillEventId"] != first.EventID {
		t.Fatalf("second Drill causal facts = %#v, admission=%q", secondFacts, admissionID)
	}
	for _, event := range []SchedulingEvent{first, second} {
		for side, projection := range map[string]SchedulingProjection{"before": event.Before, "after": event.After} {
			data, marshalErr := json.Marshal(projection)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if bytes.Contains(data, []byte("finalDrill")) {
				t.Fatalf("%s formal projection contains Drill state: %s", side, data)
			}
		}
	}
	_, drillProjections, diagnostics := projectSchedulingTruth(events, "2026-07-19")
	if projection := drillProjections[fixtureElementID]; !projection.Member || projection.AdmissionEventID != admissionID || projection.AdoptedTerminalEventID != second.EventID {
		t.Fatalf("sequential Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, first.EventID); diagnostic.Classification != "adopted" {
		t.Fatalf("first sequential diagnostic = %#v", diagnostic)
	}
}

func TestLedgerAdoptsOneConcurrentDrillBranch(t *testing.T) {
	config, _ := finalDrillFixtureConfig(t, 1, nil)
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil || started.Session.Confirmation == nil {
		t.Fatalf("start = %#v, err=%v", started.Session, err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	grade := 2
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: fixtureElementID, RawGrade: &grade, EventID: "drill-concurrent-retain"}); err != nil {
		t.Fatal(err)
	}
	events := mustEvents(t, config)
	retain := eventByID(t, events, "drill-concurrent-retain")
	remove := cloneSchedulingEvent(t, retain)
	remove.EventID = "drill-concurrent-remove"
	remove.OccurredAt = retain.OccurredAt.Add(time.Minute)
	remove.RawGrade = intPointer(4)
	normalized, err := NormalizeGrade(4)
	if err != nil {
		t.Fatal(err)
	}
	remove.Passed = boolPointer(normalized.Passed)
	remove.RatingLabel = normalized.RatingLabel
	remove.RatingMapping = normalized.RatingMapping
	remove.DrillEffect = "remove"

	_, drillProjections, diagnostics := projectSchedulingTruth(append(events, remove), "2026-07-19")
	if projection := drillProjections[fixtureElementID]; projection.Member || projection.AdoptedTerminalEventID != remove.EventID {
		t.Fatalf("concurrent Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, retain.EventID); diagnostic.Classification != "concurrent-superseded" {
		t.Fatalf("retain branch diagnostic = %#v", diagnostic)
	}
	if diagnostic := diagnosticByID(diagnostics, remove.EventID); diagnostic.Classification != "adopted" {
		t.Fatalf("remove branch diagnostic = %#v", diagnostic)
	}
}

func TestLedgerRejectsDrillGradeRootedInSupersededAdmission(t *testing.T) {
	config, _ := finalDrillFixtureConfig(t, 1, nil)
	events := mustEvents(t, config)
	initialAdmission := events[0]
	readmission := modernItemReviewEvent(t, initialAdmission.After, initialAdmission.OccurredAt.Add(24*time.Hour), 2, "drill-readmission")
	stale := SchedulingEvent{
		Spec:                  SupportedEventSpec,
		EventID:               "drill-stale-root",
		OccurredAt:            readmission.OccurredAt.Add(time.Hour),
		Type:                  "drillElement",
		ElementID:             initialAdmission.ElementID,
		SessionID:             "drill-stale-session",
		ReviewKind:            "drillGrade",
		RawGrade:              intPointer(2),
		Passed:                boolPointer(false),
		RatingLabel:           RatingAgain,
		RatingMapping:         "supermemo-grade-v1",
		LearningDate:          "2026-07-17",
		LearningDayID:         "2026-07-17",
		DrillEffect:           "retain",
		DrillAdmissionEventID: initialAdmission.EventID,
		Before:                initialAdmission.After,
		After:                 initialAdmission.After,
	}
	_, drillProjections, diagnostics := projectSchedulingTruth([]SchedulingEvent{initialAdmission, readmission, stale}, "2026-07-17")
	if projection := drillProjections[fixtureElementID]; !projection.Member || projection.AdmissionEventID != readmission.EventID || projection.AdoptedTerminalEventID != "" {
		t.Fatalf("stale-root Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, stale.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != "drill-admission-stale" {
		t.Fatalf("stale-root diagnostic = %#v", diagnostic)
	}
}

func TestLedgerRejectsDrillGradeWhoseAdmissionDoesNotMatchObservedFormalTerminal(t *testing.T) {
	config, _ := finalDrillFixtureConfig(t, 1, nil)
	events := mustEvents(t, config)
	initialAdmission := events[0]
	readmission := modernItemReviewEvent(t, initialAdmission.After, initialAdmission.OccurredAt.Add(24*time.Hour), 2, "drill-readmission")
	forged := SchedulingEvent{
		Spec:                  SupportedEventSpec,
		EventID:               "drill-forged-new-admission-old-formal-terminal",
		OccurredAt:            readmission.OccurredAt.Add(time.Hour),
		Type:                  "drillElement",
		ElementID:             initialAdmission.ElementID,
		SessionID:             "drill-forged-session",
		ReviewKind:            "drillGrade",
		RawGrade:              intPointer(2),
		Passed:                boolPointer(false),
		RatingLabel:           RatingAgain,
		RatingMapping:         "supermemo-grade-v1",
		LearningDate:          "2026-07-17",
		LearningDayID:         "2026-07-17",
		DrillEffect:           "retain",
		DrillAdmissionEventID: readmission.EventID,
		Before:                initialAdmission.After,
		After:                 initialAdmission.After,
	}

	_, drillProjections, diagnostics := projectSchedulingTruth([]SchedulingEvent{initialAdmission, readmission, forged}, "2026-07-17")
	if projection := drillProjections[fixtureElementID]; !projection.Member || projection.AdmissionEventID != readmission.EventID || projection.AdoptedTerminalEventID != "" {
		t.Fatalf("forged Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, forged.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != "drill-admission-formal-base-mismatch" {
		t.Fatalf("forged Drill diagnostic = %#v", diagnostic)
	}
}

func TestLedgerAppliesSameTimestampDrillChainInCausalOrder(t *testing.T) {
	config, _ := finalDrillFixtureConfig(t, 1, nil)
	events := mustEvents(t, config)
	admission := events[0]
	activityAt := admission.OccurredAt.Add(time.Hour)
	parent := SchedulingEvent{
		Spec:                  SupportedEventSpec,
		EventID:               "z-parent-drill-retain",
		OccurredAt:            activityAt,
		Type:                  "drillElement",
		ElementID:             admission.ElementID,
		SessionID:             "drill-causal-order-session",
		ReviewKind:            "drillGrade",
		RawGrade:              intPointer(2),
		Passed:                boolPointer(false),
		RatingLabel:           RatingAgain,
		RatingMapping:         "supermemo-grade-v1",
		LearningDate:          "2026-07-16",
		LearningDayID:         "2026-07-16",
		DrillEffect:           "retain",
		DrillAdmissionEventID: admission.EventID,
		Before:                admission.After,
		After:                 admission.After,
	}
	child := cloneSchedulingEvent(t, parent)
	child.EventID = "a-child-drill-retain"
	child.BaseDrillEventID = parent.EventID

	_, drillProjections, diagnostics := projectSchedulingTruth([]SchedulingEvent{admission, parent, child}, "2026-07-16")
	if projection := drillProjections[fixtureElementID]; !projection.Member || projection.AdoptedTerminalEventID != child.EventID {
		t.Fatalf("same-timestamp Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, parent.EventID); diagnostic.Classification != "adopted" {
		t.Fatalf("parent Drill diagnostic = %#v", diagnostic)
	}
	if diagnostic := diagnosticByID(diagnostics, child.EventID); diagnostic.Classification != "adopted" {
		t.Fatalf("child Drill diagnostic = %#v", diagnostic)
	}
}

func TestLedgerAcceptsSameTimestampDrillRootedInCausallyReferencedAdmission(t *testing.T) {
	config, _ := finalDrillFixtureConfig(t, 1, nil)
	admission := mustEvents(t, config)[0]
	drill := SchedulingEvent{
		Spec:                  SupportedEventSpec,
		EventID:               "000-same-timestamp-drill-retain",
		OccurredAt:            admission.OccurredAt,
		Type:                  "drillElement",
		ElementID:             admission.ElementID,
		SessionID:             "same-timestamp-admission-session",
		ReviewKind:            "drillGrade",
		RawGrade:              intPointer(2),
		Passed:                boolPointer(false),
		RatingLabel:           RatingAgain,
		RatingMapping:         "supermemo-grade-v1",
		LearningDate:          admission.LearningDate,
		LearningDayID:         admission.LearningDayID,
		DrillEffect:           "retain",
		DrillAdmissionEventID: admission.EventID,
		Before:                admission.After,
		After:                 admission.After,
	}

	_, drillProjections, diagnostics := projectSchedulingTruth([]SchedulingEvent{drill, admission}, admission.LearningDayID)
	if projection := drillProjections[admission.ElementID]; !projection.Member || projection.AdmissionEventID != admission.EventID || projection.AdoptedTerminalEventID != drill.EventID {
		t.Fatalf("same-timestamp admission Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, drill.EventID); diagnostic.Classification != "adopted" {
		t.Fatalf("same-timestamp admission Drill diagnostic = %#v", diagnostic)
	}
}

func TestLedgerUsesFormalCausalityForSameTimestampReadmission(t *testing.T) {
	config, _ := finalDrillFixtureConfig(t, 1, nil)
	initialAdmission := mustEvents(t, config)[0]
	readmission := modernItemReviewEvent(t, initialAdmission.After, initialAdmission.OccurredAt, 2, "000-same-timestamp-readmission")
	drill := SchedulingEvent{
		Spec:                  SupportedEventSpec,
		EventID:               "zzz-current-readmission-drill",
		OccurredAt:            readmission.OccurredAt,
		Type:                  "drillElement",
		ElementID:             readmission.ElementID,
		SessionID:             "same-timestamp-readmission-session",
		ReviewKind:            "drillGrade",
		RawGrade:              intPointer(2),
		Passed:                boolPointer(false),
		RatingLabel:           RatingAgain,
		RatingMapping:         "supermemo-grade-v1",
		LearningDate:          readmission.LearningDate,
		LearningDayID:         readmission.LearningDayID,
		DrillEffect:           "retain",
		DrillAdmissionEventID: readmission.EventID,
		Before:                readmission.After,
		After:                 readmission.After,
	}

	projections, drillProjections, diagnostics := projectSchedulingTruth([]SchedulingEvent{drill, initialAdmission, readmission}, readmission.LearningDayID)
	if projection := projections[readmission.ElementID]; projection.AdoptedTerminalID != readmission.EventID {
		t.Fatalf("same-timestamp formal projection = %#v", projection)
	}
	if projection := drillProjections[readmission.ElementID]; !projection.Member || projection.AdmissionEventID != readmission.EventID || projection.AdoptedTerminalEventID != drill.EventID {
		t.Fatalf("same-timestamp readmission Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, drill.EventID); diagnostic.Classification != "adopted" {
		t.Fatalf("same-timestamp readmission Drill diagnostic = %#v", diagnostic)
	}
}

func TestSortFinalDrillEventsIsCausalAndPermutationIndependentAtSameTimestamp(t *testing.T) {
	at := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	parent := SchedulingEvent{EventID: "z-parent", OccurredAt: at, Type: "drillElement"}
	child := SchedulingEvent{EventID: "a-child", OccurredAt: at, Type: "drillElement", BaseDrillEventID: parent.EventID}
	unrelated := SchedulingEvent{EventID: "m-unrelated", OccurredAt: at, Type: "drillElement"}
	permutations := [][]SchedulingEvent{
		{parent, child, unrelated},
		{parent, unrelated, child},
		{child, parent, unrelated},
		{child, unrelated, parent},
		{unrelated, parent, child},
		{unrelated, child, parent},
	}
	canonical := ""
	for index, events := range permutations {
		authority := make(map[string]SchedulingEvent, len(events))
		for _, event := range events {
			authority[event.EventID] = event
		}
		sortFinalDrillEvents(events, authority)
		parentIndex, childIndex, key := -1, -1, ""
		for position, event := range events {
			key += "\x00" + event.EventID
			switch event.EventID {
			case parent.EventID:
				parentIndex = position
			case child.EventID:
				childIndex = position
			}
		}
		if parentIndex >= childIndex {
			t.Fatalf("permutation %d is not causal: %#v", index, events)
		}
		if canonical == "" {
			canonical = key
		} else if key != canonical {
			t.Fatalf("permutation %d order = %q, want canonical %q", index, key, canonical)
		}
	}
}

func eventJSONFacts(t *testing.T, event SchedulingEvent) map[string]any {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var facts map[string]any
	if err = json.Unmarshal(data, &facts); err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestLedgerConcurrentSupersededLowGradeDoesNotAdmitFinalDrill(t *testing.T) {
	root, low := acceptedReviewAt(t, time.Date(2026, time.July, 19, 9, 0, 0, 0, time.UTC), 2, "concurrent-low")
	_, high := acceptedReviewAt(t, time.Date(2026, time.July, 19, 10, 0, 0, 0, time.UTC), 4, "concurrent-high")
	projections, drillProjections, diagnostics := projectSchedulingTruth([]SchedulingEvent{root, low, high}, "2026-07-19")
	projection := projections[fixtureElementID]
	if drill := drillProjections[fixtureElementID]; projection.AdoptedTerminalID != high.EventID || drill.Member || drill.AdmissionEventID != "" {
		t.Fatalf("concurrent Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, low.EventID); diagnostic.Classification != "concurrent-superseded" {
		t.Fatalf("low branch diagnostic = %#v", diagnostic)
	}
}

func TestLedgerConcurrentSupersededPendingLowGradeDoesNotAdmitFinalDrill(t *testing.T) {
	low := acceptedPendingItemIntroductionAt(t, time.Date(2026, time.July, 19, 9, 0, 0, 0, time.UTC), 2, "pending-concurrent-low")
	high := acceptedPendingItemIntroductionAt(t, time.Date(2026, time.July, 19, 10, 0, 0, 0, time.UTC), 4, "pending-concurrent-high")
	projections, drillProjections, diagnostics := projectSchedulingTruth([]SchedulingEvent{low, high}, "2026-07-19")
	projection := projections[low.ElementID]
	if drill := drillProjections[low.ElementID]; projection.AdoptedTerminalID != high.EventID || drill.Member || drill.AdmissionEventID != "" {
		t.Fatalf("concurrent Pending Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, low.EventID); diagnostic.Classification != "concurrent-superseded" {
		t.Fatalf("low Pending branch diagnostic = %#v", diagnostic)
	}
}

func TestLedgerDuplicateLowGradeDoesNotAdmitFinalDrill(t *testing.T) {
	root, low := acceptedReviewAt(t, time.Date(2026, time.July, 19, 9, 0, 0, 0, time.UTC), 2, "duplicate-low")
	projections, drillProjections, diagnostics := projectSchedulingTruth([]SchedulingEvent{root, low, low}, "2026-07-19")
	projection := projections[fixtureElementID]
	if drill := drillProjections[fixtureElementID]; projection.AdoptedTerminalID != low.EventID || drill.Member || drill.AdmissionEventID != "" {
		t.Fatalf("duplicate Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, low.EventID); diagnostic.Classification != "duplicate" || diagnostic.DuplicateCount != 2 {
		t.Fatalf("duplicate diagnostic = %#v", diagnostic)
	}
}

func TestLedgerInvalidLowGradeDoesNotAdmitFinalDrill(t *testing.T) {
	root, low := acceptedReviewAt(t, time.Date(2026, time.July, 19, 9, 0, 0, 0, time.UTC), 2, "invalid-low")
	low.After.LastRawGrade = intPointer(4)
	projections, drillProjections, diagnostics := projectSchedulingTruth([]SchedulingEvent{root, low}, "2026-07-19")
	projection := projections[fixtureElementID]
	if drill := drillProjections[fixtureElementID]; projection.AdoptedTerminalID != root.EventID || drill.Member || drill.AdmissionEventID != "" {
		t.Fatalf("invalid Drill projection = %#v", projection)
	}
	if diagnostic := diagnosticByID(diagnostics, low.EventID); diagnostic.Classification != "invalid" {
		t.Fatalf("invalid diagnostic = %#v", diagnostic)
	}
}

func TestEngineFailsClosedWhenKnownRawHistoryDisappears(t *testing.T) {
	engine, config := newFixtureEngine(t)
	before, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(config.ReviewsRoot(), "2026-07.smr")); err != nil {
		t.Fatal(err)
	}
	if err = engine.refreshProjection(t.Context()); !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("missing history refresh error = %v", err)
	}
	after, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("missing history replaced the prior projection\nbefore=%s\nafter=%s", beforeJSON, afterJSON)
	}
}

func TestEngineFailsClosedInsteadOfRollingBackWhenLatestHistoryMonthDisappears(t *testing.T) {
	config := copyFixtureWorkspace(t)
	config.Now = func() time.Time {
		return time.Date(2026, time.August, 1, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
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
	grade := 4
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "august-terminal"}); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(config.ReviewsRoot(), "2026-08.smr")); err != nil {
		t.Fatal(err)
	}
	if err = engine.refreshProjection(t.Context()); !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("missing latest month refresh error = %v", err)
	}
	projection, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil || projection.AdoptedTerminalID != "august-terminal" {
		t.Fatalf("missing latest month rolled projection back: %#v, err=%v", projection, err)
	}
}

func TestEngineFailsClosedWhenStandaloneDrillHistoryDisappears(t *testing.T) {
	tests := []struct {
		name       string
		grade      int
		effect     string
		member     bool
		advanceNow bool
	}{
		{name: "remove", grade: 4, effect: "remove", member: false},
		{name: "retain", grade: 2, effect: "retain", member: true, advanceNow: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := time.Date(2026, time.August, 1, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
			config, _ := finalDrillFixtureConfig(t, 1, func() time.Time { return current })

			engine, err := NewEngine(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Close() })
			before, err := engine.ledger.Snapshot(fixtureElementID)
			if err != nil {
				t.Fatal(err)
			}
			drillBefore, err := engine.ledger.FinalDrillSnapshot(fixtureElementID)
			if err != nil || !drillBefore.Member {
				t.Fatalf("initial Drill projection = %#v, err=%v", drillBefore, err)
			}
			after := before
			normalized, err := NormalizeGrade(test.grade)
			if err != nil {
				t.Fatal(err)
			}
			drill := SchedulingEvent{
				Spec:                  SupportedEventSpec,
				EventID:               "20260801090000-drill-" + test.effect,
				OccurredAt:            current,
				Type:                  "drillElement",
				ElementID:             fixtureElementID,
				SessionID:             "standalone-drill-session",
				ReviewKind:            "drillGrade",
				RawGrade:              intPointer(test.grade),
				Passed:                boolPointer(normalized.Passed),
				RatingLabel:           normalized.RatingLabel,
				RatingMapping:         normalized.RatingMapping,
				LearningDate:          "2026-08-01",
				LearningDayID:         "2026-08-01",
				DrillEffect:           test.effect,
				DrillAdmissionEventID: drillBefore.AdmissionEventID,
				BaseDrillEventID:      drillBefore.AdoptedTerminalEventID,
				Before:                before,
				After:                 after,
			}
			augustPath := filepath.Join(config.ReviewsRoot(), "2026-08.smr")
			writeTestJSON(t, augustPath, EventFile{Spec: SupportedEventSpec, Month: "2026-08", Events: []SchedulingEvent{drill}})
			if err = engine.refreshProjection(t.Context()); err != nil {
				t.Fatalf("apply standalone Drill event: %v", err)
			}
			expected, err := engine.ledger.FinalDrillSnapshot(fixtureElementID)
			if err != nil || expected.Member != test.member || expected.LastActivityDay != "2026-08-01" {
				t.Fatalf("standalone Drill projection = %#v, err=%v", expected, err)
			}
			if err = os.Remove(augustPath); err != nil {
				t.Fatal(err)
			}
			if test.advanceNow {
				current = time.Date(2026, time.August, 4, 9, 0, 0, 0, config.Location)
			}
			if err = engine.refreshProjection(t.Context()); !hasCode(err, ErrHistoryRequiresRepair) {
				t.Fatalf("missing standalone Drill history error = %v", err)
			}
			actual, err := engine.ledger.FinalDrillSnapshot(fixtureElementID)
			if err != nil {
				t.Fatal(err)
			}
			expectedJSON, _ := json.Marshal(expected)
			actualJSON, _ := json.Marshal(actual)
			if string(actualJSON) != string(expectedJSON) {
				t.Fatalf("missing Drill history replaced the prior projection\nexpected=%s\nactual=%s", expectedJSON, actualJSON)
			}
		})
	}
}

func acceptedReviewAt(t *testing.T, at time.Time, grade int, eventID string) (SchedulingEvent, SchedulingEvent) {
	t.Helper()
	config := copyFixtureWorkspace(t)
	config.Now = func() time.Time { return at }
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
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: eventID}); err != nil {
		t.Fatal(err)
	}
	events := mustEvents(t, config)
	return events[0], eventByID(t, events, eventID)
}

func modernItemReviewEvent(t *testing.T, before SchedulingProjection, at time.Time, grade int, eventID string) SchedulingEvent {
	t.Helper()
	normalized, err := NormalizeGrade(grade)
	if err != nil {
		t.Fatal(err)
	}
	normalized.ElementID = before.ElementID
	normalized.TargetKind = "element.item"
	normalized.ActionKind = string(ActionGradeItem)
	normalized.ReviewAt = at
	normalized.LearningDate = at.Format("2006-01-02")
	normalized.LearningDayID = normalized.LearningDate
	normalized.SessionID = "fixture-session"
	normalized.EventID = eventID
	normalized.ObservedBaseSchedulingEvent = before.AdoptedTerminalID

	arena := algorithmArena{primary: NewFSRSV1Adapter(defaultFSRSV1SchedulerConfig()), fallback: NewSimpleV1Adapter()}
	candidates, winner, decision, err := arena.review(AlgorithmInput{ElementID: before.ElementID, TargetKind: normalized.TargetKind, Review: normalized, Before: before, CurrentState: before.AlgorithmStates[fsrsV1ID]})
	if err != nil {
		t.Fatal(err)
	}
	after, err := projectionFromCandidate(before, winner, normalized)
	if err != nil {
		t.Fatal(err)
	}
	after.ScheduleProfile = fsrsV1ID
	after.AcceptedReviewAction = "GradeItem"
	after.DueLearningDay = addLearningDays(normalized.LearningDayID, winner.NextIntervalDays)
	for _, candidate := range candidates {
		if candidate.Status == "valid" {
			after.AlgorithmStates[candidate.Algorithm] = candidate.NextState
		}
	}
	event := SchedulingEvent{
		Spec: SupportedEventSpec, EventID: eventID, OccurredAt: at, Type: "reviewElement", ElementID: before.ElementID,
		SessionID: normalized.SessionID, BaseEventID: before.AdoptedTerminalID, ReviewKind: "gradeItem",
		RawGrade: intPointer(grade), Passed: boolPointer(normalized.Passed), RatingLabel: normalized.RatingLabel, RatingMapping: normalized.RatingMapping,
		LearningDate: normalized.LearningDate, LearningDayID: normalized.LearningDayID, AlgorithmDecision: decision, AlgorithmCandidates: candidates,
		Before: before, After: after,
	}
	if grade <= 3 {
		event.DrillEffect = "admit"
	}
	return event
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

func TestEngineFailsClosedWhenElementHistoryHasNoValidProjection(t *testing.T) {
	config := copyFixtureWorkspace(t)
	path := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file EventFile
	if err = json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	file.Events[0].After.ElementID = "other-element"
	writeTestJSON(t, path, file)

	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil {
		t.Fatal(err)
	}
	if started.Session == nil || started.Session.Status != SessionCompleted || started.Session.Current != nil || started.Session.Confirmation != nil {
		t.Fatalf("invalid history was exposed as learnable: %#v", started.Session)
	}
	diagnostics, err := engine.Query(t.Context(), Query{Kind: QueryElementSourceDiagnostics, ElementID: fixtureElementID})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics.Diagnostics) != 1 || diagnostics.Diagnostics[0].Code != "unusable-scheduling-history" {
		t.Fatalf("scheduling history diagnostics = %#v", diagnostics.Diagnostics)
	}
}

func TestEngineFailsClosedWhenInvalidOnlyHistoryDisappears(t *testing.T) {
	config := copyFixtureWorkspace(t)
	path := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, path)
	file.Events[0].After.ElementID = "other-element"
	writeTestJSON(t, path, file)

	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = engine.refreshProjection(t.Context()); !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("missing invalid-only history error = %v", err)
	}
	_, err = engine.Query(t.Context(), Query{Kind: QueryElementSubset, Subset: "due"})
	if !hasCode(err, ErrProjectionRebuildFailed) {
		t.Fatalf("invalid-only history contraction did not fail closed: %v", err)
	}
}

func TestEngineFailsClosedWhenIdentityCollisionLosesOnePayload(t *testing.T) {
	config := copyFixtureWorkspace(t)
	path := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, path)
	original := file.Events[0]
	conflict := cloneSchedulingEvent(t, original)
	conflict.After.DueAt = conflict.After.DueAt.AddDate(0, 0, 1)
	file.Events = append(file.Events, conflict)
	writeTestJSON(t, path, file)

	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	writeTestJSON(t, path, EventFile{Spec: file.Spec, Month: file.Month, Events: []SchedulingEvent{original}})
	if err = engine.refreshProjection(t.Context()); !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("contracted collision history error = %v", err)
	}
	_, err = engine.Query(t.Context(), Query{Kind: QueryElementSubset, Subset: "due"})
	if !hasCode(err, ErrProjectionRebuildFailed) {
		t.Fatalf("collision history contraction did not fail closed: %v", err)
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
	completeConcurrentReviewFacts(t, file.Events)
	return file.Events
}

func completeConcurrentReviewFacts(t *testing.T, events []SchedulingEvent) {
	t.Helper()
	var root SchedulingProjection
	var branchA SchedulingProjection
	arena := algorithmArena{primary: NewFSRSV1Adapter(defaultFSRSV1SchedulerConfig()), fallback: NewSimpleV1Adapter()}
	for i := range events {
		event := &events[i]
		if event.Type == "introduceElement" {
			event.Before.LifecycleState = "pending"
			event.After.LastReviewAt = timePointer(event.OccurredAt)
			event.After.LastLearningDate = event.OccurredAt.Format("2006-01-02")
			event.After.ActiveAlgorithm = fsrsV1ID
			event.After.AlgorithmStates = itemStatesForProjection(event.After)
			root = event.After
			continue
		}
		continuesBranchA := event.EventID == "branch-a2"
		if continuesBranchA {
			event.Before = branchA
		} else if event.EventID == "branch-a" || event.EventID == "branch-b" {
			event.Before = root
		}
		review, err := NormalizeGrade(4)
		if err != nil {
			t.Fatal(err)
		}
		review.ElementID = event.ElementID
		review.TargetKind = "element.item"
		review.ActionKind = string(ActionGradeItem)
		review.ReviewAt = event.OccurredAt
		review.LearningDate = event.OccurredAt.Format("2006-01-02")
		review.LearningDayID = review.LearningDate
		review.SessionID = "fixture-session"
		review.EventID = event.EventID
		review.ObservedBaseSchedulingEvent = event.BaseEventID
		event.SessionID = "fixture-session"
		event.ReviewKind = "gradeItem"
		event.RawGrade = intPointer(review.RawGrade)
		event.Passed = boolPointer(review.Passed)
		event.RatingLabel = review.RatingLabel
		event.RatingMapping = review.RatingMapping
		event.LearningDate = review.LearningDate
		event.LearningDayID = review.LearningDayID
		event.Before.ElementID = event.ElementID
		if event.Before.LifecycleState == "" {
			event.Before.LifecycleState = "memorized"
		}
		if !continuesBranchA {
			event.Before.ActiveAlgorithm = fsrsV1ID
			event.Before.AlgorithmStates = itemStatesForProjection(event.Before)
		}
		candidates, winner, decision, err := arena.review(AlgorithmInput{ElementID: event.ElementID, TargetKind: review.TargetKind, Review: review, Before: event.Before, CurrentState: event.Before.AlgorithmStates[fsrsV1ID]})
		if err != nil {
			t.Fatalf("complete %s candidate facts: %v", event.EventID, err)
		}
		after, err := projectionFromCandidate(event.Before, winner, review)
		if err != nil {
			t.Fatalf("complete %s projection: %v", event.EventID, err)
		}
		after.ScheduleProfile = fsrsV1ID
		after.AcceptedReviewAction = "GradeItem"
		after.DueLearningDay = addLearningDays(review.LearningDayID, winner.NextIntervalDays)
		for _, candidate := range candidates {
			if candidate.Status == "valid" {
				after.AlgorithmStates[candidate.Algorithm] = candidate.NextState
			}
		}
		event.After = after
		event.AlgorithmDecision = decision
		event.AlgorithmCandidates = candidates
		if event.EventID == "branch-a" {
			branchA = event.After
		}
	}
}

func itemStatesForProjection(projection SchedulingProjection) map[string]VersionedAlgorithmState {
	return map[string]VersionedAlgorithmState{
		fsrsV1ID: {
			Algorithm:     fsrsV1ID,
			SchemaVersion: 1,
			State: FSRSV1State{
				CardState:     "review",
				DueAt:         projection.DueAt,
				Stability:     1,
				Difficulty:    5,
				ScheduledDays: uint64(projection.IntervalDays),
				Repetitions:   uint64(projection.Repetitions),
				Lapses:        uint64(projection.Lapses),
				LastReviewAt:  optionalTimeValue(projection.LastReviewAt),
			},
		},
		simpleV1ID: simpleStateForProjection(projection),
	}
}

func optionalTimeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func simpleStateForProjection(projection SchedulingProjection) VersionedAlgorithmState {
	return VersionedAlgorithmState{Algorithm: simpleV1ID, SchemaVersion: 1, State: SimpleV1State{
		IntervalDays: projection.IntervalDays,
		Repetitions:  projection.Repetitions,
		Lapses:       projection.Lapses,
		LastReviewAt: projection.LastReviewAt,
		DueAt:        timePointer(projection.DueAt),
	}}
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

func acceptedPendingItemIntroductionEvent(t *testing.T) SchedulingEvent {
	t.Helper()
	return acceptedPendingItemIntroductionAt(t, time.Date(2026, time.July, 19, 9, 30, 0, 0, time.UTC), 4, "20260719093000-item-valid")
}

func acceptedPendingItemIntroductionAt(t *testing.T, at time.Time, grade int, eventID string) SchedulingEvent {
	t.Helper()
	config, elementID, _ := projectionFailurePendingItemFixture(t)
	config.Now = func() time.Time { return at }
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StagePending}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: elementID}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: elementID, RawGrade: &grade, EventID: eventID}); err != nil {
		t.Fatal(err)
	}
	return eventByID(t, mustEvents(t, config), eventID)
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
