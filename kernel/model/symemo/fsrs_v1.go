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
	"math"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v3"
)

const fsrsV1ID = "fsrs-v1"

type FSRSV1State struct {
	CardState     string    `json:"cardState"`
	DueAt         time.Time `json:"dueAt"`
	Stability     float64   `json:"stability"`
	Difficulty    float64   `json:"difficulty"`
	ElapsedDays   uint64    `json:"elapsedDays"`
	ScheduledDays uint64    `json:"scheduledDays"`
	Repetitions   uint64    `json:"repetitions"`
	Lapses        uint64    `json:"lapses"`
	LastReviewAt  time.Time `json:"lastReviewAt"`
}

type FSRSV1Adapter struct {
	parameters fsrs.Parameters
}

func NewFSRSV1Adapter(config SchedulerConfig) *FSRSV1Adapter {
	parameters := fsrs.DefaultParam()
	if config.RequestRetention > 0 {
		parameters.RequestRetention = config.RequestRetention
	}
	if config.MaximumIntervalDays > 0 {
		parameters.MaximumInterval = float64(config.MaximumIntervalDays)
	}
	if len(config.Weights) == len(parameters.W) {
		copy(parameters.W[:], config.Weights)
	}
	parameters.EnableShortTerm = config.EnableShortTerm
	parameters.EnableFuzz = config.EnableFuzz
	return &FSRSV1Adapter{parameters: parameters}
}

func defaultFSRSV1SchedulerConfig() SchedulerConfig {
	parameters := fsrs.DefaultParam()
	return SchedulerConfig{
		Spec:                SupportedConfigSpec,
		Algorithm:           fsrsV1ID,
		RequestRetention:    parameters.RequestRetention,
		MaximumIntervalDays: int(parameters.MaximumInterval),
		Weights:             append([]float64(nil), parameters.W[:]...),
		EnableShortTerm:     parameters.EnableShortTerm,
		EnableFuzz:          false,
	}
}

func (a *FSRSV1Adapter) Describe() AlgorithmDescriptor {
	return AlgorithmDescriptor{ID: fsrsV1ID, Version: "1", StateSchemaVersion: 1, SupportedTargetKinds: []string{"element.item"}}
}

func (a *FSRSV1Adapter) Initialize(input AlgorithmInput) (VersionedAlgorithmState, error) {
	card := fsrs.NewCard()
	card.Due = input.Before.DueAt
	if card.Due.IsZero() {
		card.Due = input.Review.ReviewAt
	}
	return VersionedAlgorithmState{Algorithm: fsrsV1ID, SchemaVersion: 1, State: stateFromFSRS(card)}, nil
}

func (a *FSRSV1Adapter) Predict(input AlgorithmInput) (Prediction, error) {
	state, err := decodeAlgorithmState[FSRSV1State](input.CurrentState, fsrsV1ID, 1)
	if err != nil {
		return Prediction{}, err
	}
	retrievability := fsrs.NewFSRS(a.parameters).GetRetrievability(state.toFSRS(), input.Review.ReviewAt)
	if math.IsNaN(retrievability) || math.IsInf(retrievability, 0) {
		return Prediction{}, fmt.Errorf("invalid retrievability")
	}
	return Prediction{Available: true, Retrievability: &retrievability}, nil
}

func (a *FSRSV1Adapter) Review(input AlgorithmInput) (AlgorithmCandidate, error) {
	state, err := decodeAlgorithmState[FSRSV1State](input.CurrentState, fsrsV1ID, 1)
	if err != nil {
		return AlgorithmCandidate{}, err
	}
	info := fsrs.NewFSRS(a.parameters).Next(state.toFSRS(), input.Review.ReviewAt, ratingToFSRS(input.Review.RatingLabel))
	nextState := stateFromFSRS(info.Card)
	intervalDays := int(info.Card.ScheduledDays)
	difficulty := info.Card.Difficulty
	stability := info.Card.Stability
	retrievability := fsrs.NewFSRS(a.parameters).GetRetrievability(info.Card, input.Review.ReviewAt)
	descriptor := a.Describe()
	return AlgorithmCandidate{
		Algorithm:          descriptor.ID,
		AlgorithmVersion:   descriptor.Version,
		StateSchemaVersion: descriptor.StateSchemaVersion,
		NextIntervalDays:   intervalDays,
		NextDueAt:          info.Card.Due,
		Difficulty:         &difficulty,
		Stability:          &stability,
		Retrievability:     &retrievability,
		NextState:          VersionedAlgorithmState{Algorithm: fsrsV1ID, SchemaVersion: 1, State: nextState},
	}, nil
}

func (a *FSRSV1Adapter) Migrate(state VersionedAlgorithmState) (VersionedAlgorithmState, error) {
	if state.Algorithm != fsrsV1ID || state.SchemaVersion != 1 {
		return VersionedAlgorithmState{}, domainError(ErrUnsupportedAlgorithmState, fmt.Sprintf("unsupported %s state version %d", state.Algorithm, state.SchemaVersion), nil)
	}
	decoded, err := decodeAlgorithmState[FSRSV1State](state, fsrsV1ID, 1)
	if err != nil {
		return VersionedAlgorithmState{}, err
	}
	if !isSupportedFSRSCardState(decoded.CardState) {
		return VersionedAlgorithmState{}, domainError(ErrUnsupportedAlgorithmState, "unsupported fsrs-v1 card state", nil)
	}
	return state, nil
}

func stateFromFSRS(card fsrs.Card) FSRSV1State {
	return FSRSV1State{
		CardState:     fsrsStateName(card.State),
		DueAt:         card.Due,
		Stability:     card.Stability,
		Difficulty:    card.Difficulty,
		ElapsedDays:   card.ElapsedDays,
		ScheduledDays: card.ScheduledDays,
		Repetitions:   card.Reps,
		Lapses:        card.Lapses,
		LastReviewAt:  card.LastReview,
	}
}

func (state FSRSV1State) toFSRS() fsrs.Card {
	return fsrs.Card{
		Due:           state.DueAt,
		Stability:     state.Stability,
		Difficulty:    state.Difficulty,
		ElapsedDays:   state.ElapsedDays,
		ScheduledDays: state.ScheduledDays,
		Reps:          state.Repetitions,
		Lapses:        state.Lapses,
		State:         parseFSRSState(state.CardState),
		LastReview:    state.LastReviewAt,
	}
}

func fsrsStateName(state fsrs.State) string {
	switch state {
	case fsrs.Learning:
		return "learning"
	case fsrs.Review:
		return "review"
	case fsrs.Relearning:
		return "relearning"
	default:
		return "new"
	}
}

func parseFSRSState(state string) fsrs.State {
	switch state {
	case "learning":
		return fsrs.Learning
	case "review":
		return fsrs.Review
	case "relearning":
		return fsrs.Relearning
	default:
		return fsrs.New
	}
}

func isSupportedFSRSCardState(state string) bool {
	switch state {
	case "new", "learning", "review", "relearning":
		return true
	default:
		return false
	}
}

func ratingToFSRS(label GradeLabel) fsrs.Rating {
	switch label {
	case RatingHard:
		return fsrs.Hard
	case RatingGood:
		return fsrs.Good
	case RatingEasy:
		return fsrs.Easy
	default:
		return fsrs.Again
	}
}
