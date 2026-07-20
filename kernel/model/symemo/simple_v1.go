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
	"time"
)

const simpleV1ID = "simple-v1"

type SimpleV1State struct {
	IntervalDays int        `json:"intervalDays"`
	Repetitions  int        `json:"repetitions"`
	Lapses       int        `json:"lapses"`
	LastReviewAt *time.Time `json:"lastReviewAt,omitempty"`
	DueAt        *time.Time `json:"dueAt,omitempty"`
}

type SimpleV1Adapter struct{}

func NewSimpleV1Adapter() *SimpleV1Adapter { return &SimpleV1Adapter{} }

func (a *SimpleV1Adapter) Describe() AlgorithmDescriptor {
	return AlgorithmDescriptor{ID: simpleV1ID, Version: "1", StateSchemaVersion: 1, SupportedTargetKinds: []string{"element.item"}}
}

func (a *SimpleV1Adapter) Initialize(AlgorithmInput) (VersionedAlgorithmState, error) {
	return VersionedAlgorithmState{Algorithm: simpleV1ID, SchemaVersion: 1, State: SimpleV1State{}}, nil
}

func (a *SimpleV1Adapter) Predict(AlgorithmInput) (Prediction, error) {
	return Prediction{Available: false}, nil
}

func (a *SimpleV1Adapter) Review(input AlgorithmInput) (AlgorithmCandidate, error) {
	state, err := decodeAlgorithmState[SimpleV1State](input.CurrentState, simpleV1ID, 1)
	if err != nil {
		return AlgorithmCandidate{}, err
	}
	interval := 1
	if input.Review.RawGrade >= 3 {
		previous := state.IntervalDays
		if previous < 1 {
			previous = 1
		}
		interval = previous * (input.Review.RawGrade - 2)
	}
	dueAt := input.Review.ReviewAt.AddDate(0, 0, interval)
	state.IntervalDays = interval
	state.Repetitions++
	if input.Review.RawGrade <= 2 {
		state.Lapses++
	}
	state.LastReviewAt = timePointer(input.Review.ReviewAt)
	state.DueAt = timePointer(dueAt)
	descriptor := a.Describe()
	return AlgorithmCandidate{
		Algorithm:          descriptor.ID,
		AlgorithmVersion:   descriptor.Version,
		StateSchemaVersion: descriptor.StateSchemaVersion,
		NextIntervalDays:   interval,
		NextDueAt:          dueAt,
		NextState:          VersionedAlgorithmState{Algorithm: simpleV1ID, SchemaVersion: 1, State: state},
	}, nil
}

func (a *SimpleV1Adapter) Migrate(state VersionedAlgorithmState) (VersionedAlgorithmState, error) {
	if state.Algorithm != simpleV1ID || state.SchemaVersion != 1 {
		return VersionedAlgorithmState{}, domainError(ErrUnsupportedAlgorithmState, fmt.Sprintf("unsupported %s state version %d", state.Algorithm, state.SchemaVersion), nil)
	}
	if _, err := decodeAlgorithmState[SimpleV1State](state, simpleV1ID, 1); err != nil {
		return VersionedAlgorithmState{}, err
	}
	return state, nil
}

func timePointer(value time.Time) *time.Time { return &value }
