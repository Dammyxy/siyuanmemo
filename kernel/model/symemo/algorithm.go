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
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

type AlgorithmDescriptor struct {
	ID                   string   `json:"id"`
	Version              string   `json:"version"`
	StateSchemaVersion   int      `json:"stateSchemaVersion"`
	SupportedTargetKinds []string `json:"supportedTargetKinds"`
}

type AlgorithmInput struct {
	ElementID    string
	TargetKind   string
	Review       NormalizedReview
	Before       SchedulingProjection
	CurrentState VersionedAlgorithmState
}

type Prediction struct {
	Available      bool     `json:"available"`
	Retrievability *float64 `json:"retrievability,omitempty"`
}

type AlgorithmAdapter interface {
	Describe() AlgorithmDescriptor
	Initialize(AlgorithmInput) (VersionedAlgorithmState, error)
	Predict(AlgorithmInput) (Prediction, error)
	Review(AlgorithmInput) (AlgorithmCandidate, error)
	Migrate(VersionedAlgorithmState) (VersionedAlgorithmState, error)
}

func NormalizeGrade(raw int) (NormalizedReview, error) {
	review := NormalizedReview{RawGrade: raw, RatingMapping: "supermemo-grade-v1"}
	if raw < 0 || raw > 5 {
		return review, domainError(ErrUnsupportedGrade, "raw grade must be an integer from 0 through 5", nil)
	}
	review.Passed = raw >= 3
	switch raw {
	case 0, 1, 2:
		review.RatingLabel = RatingAgain
	case 3:
		review.RatingLabel = RatingHard
	case 4:
		review.RatingLabel = RatingGood
	case 5:
		review.RatingLabel = RatingEasy
	}
	return review, nil
}

func ValidateCandidate(candidate AlgorithmCandidate, descriptor AlgorithmDescriptor, reviewAt time.Time) error {
	if candidate.Algorithm != descriptor.ID || candidate.AlgorithmVersion != descriptor.Version || candidate.StateSchemaVersion != descriptor.StateSchemaVersion {
		return errors.New("candidate identity does not match descriptor")
	}
	if !supportsTarget(descriptor, "element.item") {
		return errors.New("adapter does not support element.item")
	}
	if candidate.NextDueAt.IsZero() || !candidate.NextDueAt.After(reviewAt) {
		return errors.New("candidate due time must be after review time")
	}
	if candidate.NextIntervalDays < 0 {
		return errors.New("candidate interval must be non-negative")
	}
	if err := validateFiniteRange(candidate.Difficulty, 1, 10, true); err != nil {
		return fmt.Errorf("difficulty: %w", err)
	}
	if candidate.Stability != nil {
		if math.IsNaN(*candidate.Stability) || math.IsInf(*candidate.Stability, 0) || *candidate.Stability <= 0 {
			return errors.New("stability must be finite and positive")
		}
	}
	if err := validateFiniteRange(candidate.Retrievability, 0, 1, true); err != nil {
		return fmt.Errorf("retrievability: %w", err)
	}
	if candidate.PredictedRecallBeforeGrade != nil {
		if err := validateFiniteRange(candidate.PredictedRecallBeforeGrade, 0, 1, false); err != nil {
			return fmt.Errorf("predicted recall: %w", err)
		}
	}
	if candidate.NextState.Algorithm != descriptor.ID || candidate.NextState.SchemaVersion != descriptor.StateSchemaVersion {
		return errors.New("candidate state identity does not match descriptor")
	}
	return nil
}

func supportsTarget(descriptor AlgorithmDescriptor, kind string) bool {
	for _, supported := range descriptor.SupportedTargetKinds {
		if supported == kind {
			return true
		}
	}
	return false
}

func validateFiniteRange(value *float64, min, max float64, allowNil bool) error {
	if value == nil {
		if allowNil {
			return nil
		}
		return errors.New("value is required")
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < min || *value > max {
		return fmt.Errorf("value must be finite and in [%g,%g]", min, max)
	}
	return nil
}

func decodeAlgorithmState[T any](state VersionedAlgorithmState, algorithm string, schema int) (T, error) {
	var decoded T
	if state.Algorithm != algorithm || state.SchemaVersion != schema {
		return decoded, domainError(ErrUnsupportedAlgorithmState, "unsupported algorithm state", nil)
	}
	data, err := json.Marshal(state.State)
	if err != nil {
		return decoded, err
	}
	if err = json.Unmarshal(data, &decoded); err != nil {
		return decoded, err
	}
	return decoded, nil
}

type algorithmArena struct {
	primary  AlgorithmAdapter
	fallback AlgorithmAdapter
}

func (a algorithmArena) review(input AlgorithmInput) ([]AlgorithmCandidate, AlgorithmCandidate, AlgorithmDecision, error) {
	primary := a.run(a.primary, input)
	fallback := a.run(a.fallback, input)
	candidates := []AlgorithmCandidate{primary, fallback}
	decision := AlgorithmDecision{
		Policy:            "primary",
		Winner:            a.primary.Describe().ID,
		EnabledAlgorithms: []string{a.primary.Describe().ID, a.fallback.Describe().ID},
	}
	if primary.Status == "valid" {
		return candidates, primary, decision, nil
	}
	if fallback.Status == "valid" {
		decision.Policy = "fallback"
		decision.Winner = a.fallback.Describe().ID
		decision.FallbackReason = primary.ValidationReason
		return candidates, fallback, decision, nil
	}
	return candidates, AlgorithmCandidate{}, AlgorithmDecision{}, domainError(ErrInvalidAlgorithmOutput, "primary and fallback algorithm candidates are invalid", nil)
}

func (a algorithmArena) run(adapter AlgorithmAdapter, input AlgorithmInput) AlgorithmCandidate {
	descriptor := adapter.Describe()
	if !supportsTarget(descriptor, input.TargetKind) {
		return AlgorithmCandidate{Algorithm: descriptor.ID, AlgorithmVersion: descriptor.Version, StateSchemaVersion: descriptor.StateSchemaVersion, Status: "unsupported", ValidationReason: "primary-unsupported"}
	}
	state := input.Before.AlgorithmStates[descriptor.ID]
	if state.Algorithm == "" && input.CurrentState.Algorithm == descriptor.ID {
		state = input.CurrentState
	}
	if state.Algorithm == "" {
		initialized, err := adapter.Initialize(input)
		if err != nil {
			return AlgorithmCandidate{Algorithm: descriptor.ID, AlgorithmVersion: descriptor.Version, StateSchemaVersion: descriptor.StateSchemaVersion, Status: "error", ValidationReason: "primary-error"}
		}
		state = initialized
	}
	migrated, err := adapter.Migrate(state)
	if err != nil {
		return AlgorithmCandidate{Algorithm: descriptor.ID, AlgorithmVersion: descriptor.Version, StateSchemaVersion: descriptor.StateSchemaVersion, Status: "error", ValidationReason: "primary-error"}
	}
	input.CurrentState = migrated
	prediction, predictionErr := adapter.Predict(input)
	candidate, reviewErr := adapter.Review(input)
	if reviewErr != nil {
		return AlgorithmCandidate{Algorithm: descriptor.ID, AlgorithmVersion: descriptor.Version, StateSchemaVersion: descriptor.StateSchemaVersion, Status: "error", ValidationReason: "primary-error"}
	}
	if predictionErr == nil && prediction.Available {
		candidate.PredictedRecallBeforeGrade = prediction.Retrievability
	}
	if err = ValidateCandidate(candidate, descriptor, input.Review.ReviewAt); err != nil {
		candidate.Status = "invalid"
		candidate.ValidationReason = "primary-invalid-output"
		return candidate
	}
	if err = validateCandidateTransition(candidate, input); err != nil {
		candidate.Status = "invalid"
		candidate.ValidationReason = "primary-invalid-output"
		return candidate
	}
	candidate.Status = "valid"
	return candidate
}

func validateCandidateTransition(candidate AlgorithmCandidate, input AlgorithmInput) error {
	switch candidate.Algorithm {
	case fsrsV1ID:
		next, err := decodeAlgorithmState[FSRSV1State](candidate.NextState, fsrsV1ID, 1)
		if err != nil {
			return err
		}
		previous, previousErr := decodeAlgorithmState[FSRSV1State](input.CurrentState, fsrsV1ID, 1)
		if previousErr != nil {
			previous = FSRSV1State{}
		}
		if !next.DueAt.Equal(candidate.NextDueAt) || next.Repetitions != previous.Repetitions+1 || next.Lapses < previous.Lapses || next.Lapses > next.Repetitions {
			return errors.New("fsrs-v1 state transition is inconsistent")
		}
	case simpleV1ID:
		next, err := decodeAlgorithmState[SimpleV1State](candidate.NextState, simpleV1ID, 1)
		if err != nil {
			return err
		}
		previous, previousErr := decodeAlgorithmState[SimpleV1State](input.CurrentState, simpleV1ID, 1)
		if previousErr != nil {
			previous = SimpleV1State{}
		}
		if next.DueAt == nil || !next.DueAt.Equal(candidate.NextDueAt) || next.Repetitions != previous.Repetitions+1 || next.Lapses < previous.Lapses || next.Lapses > next.Repetitions {
			return errors.New("simple-v1 state transition is inconsistent")
		}
	}
	return nil
}
