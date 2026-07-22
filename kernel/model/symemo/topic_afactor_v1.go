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
)

const topicAFactorV1ID = "topic-afactor-v1"

type TopicAFactorV1State struct {
	IntervalDays     int     `json:"intervalDays"`
	Repetitions      int     `json:"repetitions"`
	LastLearningDay  string  `json:"lastLearningDay,omitempty"`
	DueLearningDay   string  `json:"dueLearningDay,omitempty"`
	EffectiveAFactor float64 `json:"effectiveAFactor"`
}

type TopicAFactorV1Adapter struct {
	config SchedulerConfig
}

func NewTopicAFactorV1Adapter(config SchedulerConfig) *TopicAFactorV1Adapter {
	if config.AFactor <= 0 {
		config.AFactor = 2.5
	}
	if config.MinimumIntervalDays < 1 {
		config.MinimumIntervalDays = 1
	}
	if config.MaximumIntervalDays < config.MinimumIntervalDays {
		config.MaximumIntervalDays = 36500
	}
	if config.SkipPolicy == "" {
		config.SkipPolicy = "none"
	}
	return &TopicAFactorV1Adapter{config: config}
}

func (a *TopicAFactorV1Adapter) Describe() AlgorithmDescriptor {
	return AlgorithmDescriptor{ID: topicAFactorV1ID, Version: "1", StateSchemaVersion: 1, SupportedTargetKinds: []string{"element.topic"}}
}

func (a *TopicAFactorV1Adapter) Initialize(AlgorithmInput) (VersionedAlgorithmState, error) {
	return VersionedAlgorithmState{Algorithm: topicAFactorV1ID, SchemaVersion: 1, State: TopicAFactorV1State{EffectiveAFactor: a.config.AFactor}}, nil
}

func (a *TopicAFactorV1Adapter) Predict(AlgorithmInput) (Prediction, error) {
	return Prediction{Available: false}, nil
}

func (a *TopicAFactorV1Adapter) Review(input AlgorithmInput) (AlgorithmCandidate, error) {
	if input.Review.ActionKind != string(ActionNextTopic) {
		return AlgorithmCandidate{}, domainError(ErrUnsupportedOperation, "topic-afactor-v1 accepts only NextTopic", nil)
	}
	state, err := decodeAlgorithmState[TopicAFactorV1State](input.CurrentState, topicAFactorV1ID, 1)
	if err != nil {
		return AlgorithmCandidate{}, err
	}
	previous := input.Before.IntervalDays
	if state.IntervalDays > 0 {
		previous = state.IntervalDays
	}
	if previous < 1 {
		previous = 1
	}
	factor := a.config.AFactor
	nextInterval := int(math.Ceil(float64(previous) * factor))
	if nextInterval < a.config.MinimumIntervalDays {
		nextInterval = a.config.MinimumIntervalDays
	}
	if a.config.MaximumIntervalDays > 0 && nextInterval > a.config.MaximumIntervalDays {
		nextInterval = a.config.MaximumIntervalDays
	}
	nextDueAt := input.Review.ReviewAt.AddDate(0, 0, nextInterval)
	state.IntervalDays = nextInterval
	state.Repetitions++
	state.LastLearningDay = input.Review.LearningDayID
	state.DueLearningDay = addLearningDays(input.Review.LearningDayID, nextInterval)
	state.EffectiveAFactor = factor
	descriptor := a.Describe()
	return AlgorithmCandidate{
		Algorithm:          descriptor.ID,
		AlgorithmVersion:   descriptor.Version,
		StateSchemaVersion: descriptor.StateSchemaVersion,
		NextIntervalDays:   nextInterval,
		NextDueAt:          nextDueAt,
		NextState:          VersionedAlgorithmState{Algorithm: topicAFactorV1ID, SchemaVersion: 1, State: state},
	}, nil
}

func (a *TopicAFactorV1Adapter) Migrate(state VersionedAlgorithmState) (VersionedAlgorithmState, error) {
	if state.Algorithm != topicAFactorV1ID || state.SchemaVersion != 1 {
		return VersionedAlgorithmState{}, domainError(ErrUnsupportedAlgorithmState, fmt.Sprintf("unsupported %s state version %d", state.Algorithm, state.SchemaVersion), nil)
	}
	if _, err := decodeAlgorithmState[TopicAFactorV1State](state, topicAFactorV1ID, 1); err != nil {
		return VersionedAlgorithmState{}, err
	}
	return state, nil
}
