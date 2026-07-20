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
			if err = ValidateCandidate(candidate, descriptor, review.ReviewAt); err != nil {
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
