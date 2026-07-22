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
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestAlgorithmAdapterContract(t *testing.T) {
	config := Config{SchedulerRoot: "testdata/scheduler"}
	fsrsConfig, err := config.LoadSchedulerConfig(fsrsV1ID)
	if err != nil {
		t.Fatal(err)
	}
	adapters := []AlgorithmAdapter{NewFSRSV1Adapter(fsrsConfig), NewSimpleV1Adapter()}
	for _, adapter := range adapters {
		descriptor := adapter.Describe()
		for raw := 0; raw <= 5; raw++ {
			review, err := NormalizeGrade(raw)
			if err != nil {
				t.Fatal(err)
			}
			review.ElementID = fixtureElementID
			review.ReviewAt = time.Date(2026, time.July, 19, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
			input := AlgorithmInput{ElementID: fixtureElementID, TargetKind: "element.item", Review: review, Before: SchedulingProjection{ElementID: fixtureElementID, DueAt: review.ReviewAt}}
			state, err := adapter.Initialize(input)
			if err != nil {
				t.Fatalf("%s initialize: %v", descriptor.ID, err)
			}
			input.CurrentState = state
			firstPrediction, err := adapter.Predict(input)
			if err != nil {
				t.Fatalf("%s predict: %v", descriptor.ID, err)
			}
			secondPrediction, err := adapter.Predict(input)
			if err != nil || !reflect.DeepEqual(firstPrediction, secondPrediction) {
				t.Fatalf("%s prediction is not deterministic: %#v %#v, err=%v", descriptor.ID, firstPrediction, secondPrediction, err)
			}
			candidate, err := adapter.Review(input)
			if err != nil {
				t.Fatalf("%s grade %d: %v", descriptor.ID, raw, err)
			}
			repeated, err := adapter.Review(input)
			if err != nil || !reflect.DeepEqual(candidate, repeated) {
				t.Fatalf("%s grade %d is not deterministic: %#v %#v, err=%v", descriptor.ID, raw, candidate, repeated, err)
			}
			if err = ValidateCandidate(candidate, descriptor, input.TargetKind, review.ReviewAt); err != nil {
				t.Fatalf("%s grade %d candidate: %v", descriptor.ID, raw, err)
			}
			if err = validateCandidateTransition(candidate, input); err != nil {
				t.Fatalf("%s grade %d transition: %v", descriptor.ID, raw, err)
			}
			if _, err = adapter.Migrate(candidate.NextState); err != nil {
				t.Fatalf("%s migrate: %v", descriptor.ID, err)
			}
		}
		if _, err = adapter.Migrate(VersionedAlgorithmState{Algorithm: descriptor.ID, SchemaVersion: 99}); !hasCode(err, ErrUnsupportedAlgorithmState) {
			t.Fatalf("%s unsupported migration error = %v", descriptor.ID, err)
		}
	}
}

func TestTopicAFactorV1AdapterContract(t *testing.T) {
	config := Config{SchedulerRoot: "testdata/scheduler"}
	topicConfig, err := config.LoadSchedulerConfig(topicAFactorV1ID)
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewTopicAFactorV1Adapter(topicConfig)
	descriptor := adapter.Describe()
	reviewAt := time.Date(2026, time.July, 19, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	review := NormalizedReview{ElementID: dailyTopicID, TargetKind: "element.topic", ActionKind: string(ActionNextTopic), ReviewAt: reviewAt, LearningDayID: "2026-07-19"}
	input := AlgorithmInput{
		ElementID:  dailyTopicID,
		TargetKind: "element.topic",
		Review:     review,
		Before:     SchedulingProjection{ElementID: dailyTopicID, IntervalDays: 3},
		CurrentState: VersionedAlgorithmState{Algorithm: topicAFactorV1ID, SchemaVersion: 1, State: TopicAFactorV1State{
			IntervalDays:     3,
			Repetitions:      1,
			LastLearningDay:  "2026-07-16",
			DueLearningDay:   "2026-07-19",
			EffectiveAFactor: 9.9,
		}},
	}
	candidate, err := adapter.Review(input)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.NextIntervalDays != 8 {
		t.Fatalf("topic interval = %d", candidate.NextIntervalDays)
	}
	if err = ValidateCandidate(candidate, descriptor, review.TargetKind, reviewAt); err != nil {
		t.Fatalf("topic candidate invalid: %v", err)
	}
	if err = validateCandidateTransition(candidate, input); err != nil {
		t.Fatalf("topic transition invalid: %v", err)
	}
	gradeReview := review
	gradeReview.ActionKind = string(ActionGradeItem)
	input.Review = gradeReview
	if _, err = adapter.Review(input); !hasCode(err, ErrUnsupportedOperation) {
		t.Fatalf("topic grade error = %v", err)
	}
}

func TestTopicAFactorV1AdapterRejectsUnsupportedState(t *testing.T) {
	adapter := NewTopicAFactorV1Adapter(SchedulerConfig{AFactor: 2.5, MinimumIntervalDays: 1, MaximumIntervalDays: 36500, SkipPolicy: "none"})
	reviewAt := time.Date(2026, time.July, 19, 9, 0, 0, 0, time.UTC)
	_, err := adapter.Review(AlgorithmInput{
		ElementID:  dailyTopicID,
		TargetKind: "element.topic",
		Review:     NormalizedReview{ElementID: dailyTopicID, TargetKind: "element.topic", ActionKind: string(ActionNextTopic), ReviewAt: reviewAt, LearningDayID: "2026-07-19"},
		Before:     SchedulingProjection{ElementID: dailyTopicID, IntervalDays: 3},
		CurrentState: VersionedAlgorithmState{
			Algorithm:     topicAFactorV1ID,
			SchemaVersion: 99,
			State:         map[string]any{"intervalDays": 3},
		},
	})
	if !hasCode(err, ErrUnsupportedAlgorithmState) {
		t.Fatalf("unsupported Topic state error = %v", err)
	}
}

func TestFSRSV1RejectsUnknownPersistedCardState(t *testing.T) {
	config := Config{SchedulerRoot: "testdata/scheduler"}
	fsrsConfig, err := config.LoadSchedulerConfig(fsrsV1ID)
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewFSRSV1Adapter(fsrsConfig)
	state := VersionedAlgorithmState{Algorithm: fsrsV1ID, SchemaVersion: 1, State: FSRSV1State{CardState: "future-state"}}
	if _, err = adapter.Migrate(state); !hasCode(err, ErrUnsupportedAlgorithmState) {
		t.Fatalf("unknown card state migration error = %v", err)
	}
}

func TestFSRSCurrentConfigurationCanDifferFromRecordedCandidate(t *testing.T) {
	root, recordedEvent := acceptedReviewHistory(t)
	recorded := recordedEvent.AlgorithmCandidates[0]
	changed := defaultFSRSV1SchedulerConfig()
	changed.RequestRetention = 0.99
	changed.MaximumIntervalDays = 1
	fresh := evaluateAlgorithmAdapter(NewFSRSV1Adapter(changed), reviewAlgorithmInput(recordedEvent, "element.item", fsrsV1ID))
	if fresh.Status != "valid" {
		t.Fatalf("fresh changed-configuration candidate = %#v", fresh)
	}
	recordedHash, err := canonicalHash(recorded)
	if err != nil {
		t.Fatal(err)
	}
	freshHash, err := canonicalHash(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if recordedHash == freshHash || recorded.NextIntervalDays == fresh.NextIntervalDays {
		t.Fatalf("changed current FSRS configuration did not produce a deliberately different result: recorded=%#v fresh=%#v", recorded, fresh)
	}
	projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{root, recordedEvent})
	if projection := projections[root.ElementID]; projection.AdoptedTerminalID != recordedEvent.EventID {
		t.Fatalf("recorded FSRS event was not replayed: projection=%#v diagnostics=%#v", projection, diagnostics)
	}
}

func TestFSRSV1RejectsNewStateWithRecordedRepetitions(t *testing.T) {
	_, recordedEvent := acceptedReviewHistory(t)
	candidate := recordedEvent.AlgorithmCandidates[0]
	state, err := decodeAlgorithmState[FSRSV1State](candidate.NextState, fsrsV1ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	state.CardState = "new"
	candidate.NextState.State = state
	if err = validateCandidateTransition(candidate, reviewAlgorithmInput(recordedEvent, "element.item", fsrsV1ID)); err == nil {
		t.Fatal("FSRS new state with repetitions was accepted")
	}
}

func TestFSRSRecordedCandidatesReplayAcrossAllGradesAndCurrentConfigurations(t *testing.T) {
	variants := []struct {
		requestRetention    float64
		maximumIntervalDays int
	}{
		{requestRetention: 0.99, maximumIntervalDays: 1},
		{requestRetention: 0.80, maximumIntervalDays: 36500},
	}
	for grade := 0; grade <= 5; grade++ {
		root, recordedEvent := acceptedReviewAt(t, time.Date(2026, time.July, 19, 9, 0, grade, 0, time.UTC), grade, fmt.Sprintf("fsrs-replay-grade-%d", grade))
		for variantIndex, variant := range variants {
			t.Run(fmt.Sprintf("grade-%d-variant-%d", grade, variantIndex), func(t *testing.T) {
				changed := defaultFSRSV1SchedulerConfig()
				changed.RequestRetention = variant.requestRetention
				changed.MaximumIntervalDays = variant.maximumIntervalDays
				fresh := evaluateAlgorithmAdapter(NewFSRSV1Adapter(changed), reviewAlgorithmInput(recordedEvent, "element.item", fsrsV1ID))
				if fresh.Status != "valid" {
					t.Fatalf("changed configuration candidate = %#v", fresh)
				}
				projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{root, recordedEvent})
				if projection := projections[root.ElementID]; projection.AdoptedTerminalID != recordedEvent.EventID {
					t.Fatalf("recorded grade %d did not replay: projection=%#v diagnostics=%#v", grade, projection, diagnostics)
				}
			})
		}
	}
}
