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
	"os"
	"path/filepath"
	"testing"
)

func TestSessionUnreadableHistoryFailure(t *testing.T) {
	engine, config := newFixtureEngine(t)
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(path)
		_ = os.WriteFile(path, data, 0644)
	})
	grade := 4
	_, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "durable-failure"})
	if !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("error = %v", err)
	}
	stateResult, stateErr := engine.Query(context.Background(), Query{Kind: QueryCurrentSession})
	if stateErr != nil || stateResult.Session == nil || stateResult.Session.Phase != PhaseAnswer {
		t.Fatalf("session after durable failure = %#v, err=%v", stateResult, stateErr)
	}
}

func TestProjectionFailureLocksEngineUntilReplacement(t *testing.T) {
	engine, config := newFixtureEngine(t)
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	restoreProjection := installProjectionRefreshFailure(t, engine, config)
	grade := 4
	_, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "projection-failure"})
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrProjectionRefreshFailed || !domainErr.ReviewAccepted || domainErr.AcceptedEventID != "projection-failure" {
		t.Fatalf("accepted trigger error = %#v", err)
	}
	restoreProjection()
	if _, err = engine.Query(context.Background(), Query{Kind: QueryCurrentSession}); !hasCode(err, ErrProjectionRebuildFailed) {
		t.Fatalf("query after projection failure = %v", err)
	}
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart}); !hasCode(err, ErrProjectionRebuildFailed) {
		t.Fatalf("learning action after projection failure = %v", err)
	}
}

func TestLedgerCommitPersistsCompleteEvent(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	_, _ = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	_, _ = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID})
	grade := 4
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "complete-event"}); err != nil {
		t.Fatal(err)
	}
	event, found, err := engine.ledger.EventByID("complete-event")
	if err != nil || !found {
		t.Fatalf("event found=%v err=%v", found, err)
	}
	if event.BaseEventID != "20260716080000-intro001" || event.RawGrade == nil || *event.RawGrade != 4 || len(event.AlgorithmCandidates) != 2 || event.SessionID == "" || event.After.AdoptedTerminalID != event.EventID {
		t.Fatalf("incomplete event: %#v", event)
	}
	for _, candidate := range event.AlgorithmCandidates {
		if candidate.Status != "valid" {
			t.Fatalf("shadow candidate is not valid: %#v", candidate)
		}
	}
}
