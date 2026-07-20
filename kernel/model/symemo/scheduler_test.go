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
	"testing"
	"time"
)

func TestSchedulerDueOnlyAndSameDayExclusion(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	targets, err := engine.scheduler.BuildQueue()
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets=%#v err=%v", targets, err)
	}
	projection := targets[0].ObservedProjection
	projection.LastLearningDate = engine.config.Now().In(engine.config.Location).Format("2006-01-02")
	if err = engine.index.replaceAll(context.Background(), projectionBuild{Elements: map[string]Element{fixtureElementID: mustElement(t, engine)}, Projections: map[string]SchedulingProjection{fixtureElementID: projection}}); err != nil {
		t.Fatal(err)
	}
	targets, err = engine.scheduler.BuildQueue()
	if err != nil || len(targets) != 0 {
		t.Fatalf("same-day targets=%#v err=%v", targets, err)
	}
}

func TestSchedulerDueOrdering(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	now := engine.config.Now().In(engine.config.Location)
	elements := map[string]Element{}
	projections := map[string]SchedulingProjection{}
	for _, fixture := range []struct {
		id       string
		due      time.Time
		priority float64
	}{
		{id: "item-a", due: now.Add(-time.Hour), priority: 2},
		{id: "item-b", due: now.Add(-2 * time.Hour), priority: 5},
		{id: "item-c", due: now.Add(-time.Hour), priority: 1},
	} {
		elements[fixture.id] = Element{ID: fixture.id, Type: "item", Payload: ItemPayload{Kind: "qa", Prompt: fixture.id, Answer: "answer"}}
		projections[fixture.id] = SchedulingProjection{ElementID: fixture.id, LifecycleState: "memorized", DueAt: fixture.due, PriorityPosition: fixture.priority}
	}
	if err := engine.index.replaceAll(context.Background(), projectionBuild{Elements: elements, Projections: projections}); err != nil {
		t.Fatal(err)
	}
	targets, err := engine.scheduler.BuildQueue()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 || targets[0].ElementID != "item-b" || targets[1].ElementID != "item-c" || targets[2].ElementID != "item-a" {
		t.Fatalf("due order = %#v", targets)
	}
}

func TestGradeNormalization(t *testing.T) {
	for raw := 0; raw <= 5; raw++ {
		review, err := NormalizeGrade(raw)
		if err != nil {
			t.Fatal(err)
		}
		wantLabel := RatingAgain
		switch raw {
		case 3:
			wantLabel = RatingHard
		case 4:
			wantLabel = RatingGood
		case 5:
			wantLabel = RatingEasy
		}
		if review.RawGrade != raw || review.Passed != (raw >= 3) || review.RatingLabel != wantLabel || review.RatingMapping != "supermemo-grade-v1" {
			t.Fatalf("grade %d = %#v", raw, review)
		}
	}
	for _, raw := range []int{-1, 6} {
		if _, err := NormalizeGrade(raw); !hasCode(err, ErrUnsupportedGrade) {
			t.Fatalf("grade %d error = %v", raw, err)
		}
	}
}

func TestSchedulerFallsBackFromInvalidPrimary(t *testing.T) {
	review, _ := NormalizeGrade(4)
	review.ReviewAt = time.Date(2026, time.July, 19, 9, 0, 0, 0, time.UTC)
	input := AlgorithmInput{ElementID: fixtureElementID, TargetKind: "element.item", Review: review, Before: SchedulingProjection{ElementID: fixtureElementID}}
	arena := algorithmArena{primary: invalidAdapter{}, fallback: NewSimpleV1Adapter()}
	_, winner, decision, err := arena.review(input)
	if err != nil || winner.Algorithm != simpleV1ID || decision.Policy != "fallback" || decision.FallbackReason != "primary-invalid-output" {
		t.Fatalf("winner=%#v decision=%#v err=%v", winner, decision, err)
	}
}

func TestSchedulerRejectsInvalidCandidatesAndTransitions(t *testing.T) {
	review, _ := NormalizeGrade(4)
	review.ReviewAt = time.Date(2026, time.July, 19, 9, 0, 0, 0, time.UTC)
	input := AlgorithmInput{ElementID: fixtureElementID, TargetKind: "element.item", Review: review, Before: SchedulingProjection{ElementID: fixtureElementID}}

	invalidArena := algorithmArena{primary: invalidAdapter{}, fallback: invalidAdapter{}}
	if _, _, _, err := invalidArena.review(input); !hasCode(err, ErrInvalidAlgorithmOutput) {
		t.Fatalf("invalid candidates error = %v", err)
	}

	transition := algorithmArena{primary: invalidSimpleTransitionAdapter{}, fallback: invalidSimpleTransitionAdapter{}}
	candidates, _, _, err := transition.review(input)
	if !hasCode(err, ErrInvalidAlgorithmOutput) || len(candidates) != 2 || candidates[0].ValidationReason != "primary-invalid-output" {
		t.Fatalf("invalid transition candidates = %#v, err=%v", candidates, err)
	}
}

func mustElement(t *testing.T, engine *Engine) Element {
	t.Helper()
	element, err := engine.index.element(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	return element
}

type invalidAdapter struct{}

func (invalidAdapter) Describe() AlgorithmDescriptor {
	return AlgorithmDescriptor{ID: "invalid-primary", Version: "1", StateSchemaVersion: 1, SupportedTargetKinds: []string{"element.item"}}
}
func (invalidAdapter) Initialize(AlgorithmInput) (VersionedAlgorithmState, error) {
	return VersionedAlgorithmState{Algorithm: "invalid-primary", SchemaVersion: 1, State: map[string]any{}}, nil
}
func (invalidAdapter) Predict(AlgorithmInput) (Prediction, error) { return Prediction{}, nil }
func (invalidAdapter) Review(input AlgorithmInput) (AlgorithmCandidate, error) {
	return AlgorithmCandidate{Algorithm: "invalid-primary", AlgorithmVersion: "1", StateSchemaVersion: 1, NextDueAt: input.Review.ReviewAt, NextState: input.CurrentState}, nil
}
func (invalidAdapter) Migrate(state VersionedAlgorithmState) (VersionedAlgorithmState, error) {
	return state, nil
}

type invalidSimpleTransitionAdapter struct{}

func (invalidSimpleTransitionAdapter) Describe() AlgorithmDescriptor {
	return AlgorithmDescriptor{ID: simpleV1ID, Version: "1", StateSchemaVersion: 1, SupportedTargetKinds: []string{"element.item"}}
}
func (invalidSimpleTransitionAdapter) Initialize(AlgorithmInput) (VersionedAlgorithmState, error) {
	return VersionedAlgorithmState{Algorithm: simpleV1ID, SchemaVersion: 1, State: SimpleV1State{}}, nil
}
func (invalidSimpleTransitionAdapter) Predict(AlgorithmInput) (Prediction, error) {
	return Prediction{}, nil
}
func (invalidSimpleTransitionAdapter) Review(input AlgorithmInput) (AlgorithmCandidate, error) {
	dueAt := input.Review.ReviewAt.AddDate(0, 0, 1)
	return AlgorithmCandidate{
		Algorithm:          simpleV1ID,
		AlgorithmVersion:   "1",
		StateSchemaVersion: 1,
		NextIntervalDays:   1,
		NextDueAt:          dueAt,
		NextState:          VersionedAlgorithmState{Algorithm: simpleV1ID, SchemaVersion: 1, State: SimpleV1State{DueAt: &dueAt}},
	}, nil
}
func (invalidSimpleTransitionAdapter) Migrate(state VersionedAlgorithmState) (VersionedAlgorithmState, error) {
	return state, nil
}
