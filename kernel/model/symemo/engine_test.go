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
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestEngineDueItemReview(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	query, err := engine.Query(context.Background(), Query{Kind: QueryElementSubset, Subset: "due"})
	if err != nil || len(query.Items) != 1 || query.Items[0].ElementID != fixtureElementID {
		t.Fatalf("due query = %#v, err=%v", query, err)
	}
	if query.Items[0].Prompt == "" {
		t.Fatal("due prompt is empty")
	}
	start, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	if err != nil || start.Session == nil || start.Session.Phase != PhaseQuestion || start.Session.Current == nil || start.Session.Current.Answer != "" {
		t.Fatalf("start = %#v, err=%v", start, err)
	}
	show, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID})
	if err != nil || show.Session == nil || show.Session.Phase != PhaseAnswer || show.Session.Current.Answer == "" {
		t.Fatalf("show = %#v, err=%v", show, err)
	}
	grade := 4
	review, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "20260719090000-review001"})
	if err != nil || !review.ReviewAccepted || review.Projection == nil || review.EventID == "" {
		t.Fatalf("grade = %#v, err=%v", review, err)
	}
	if review.Session == nil || review.Session.Phase != PhaseComplete {
		t.Fatalf("session after grade = %#v", review.Session)
	}
	query, err = engine.Query(context.Background(), Query{Kind: QueryElementSubset, Subset: "due"})
	if err != nil || len(query.Items) != 0 {
		t.Fatalf("same-day due query = %#v, err=%v", query, err)
	}
}

func TestEngineItemQueueRejectsNonItemElements(t *testing.T) {
	config := copyElementTreeFixtureWorkspace(t)
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	reviewData, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture EventFile
	if err = json.Unmarshal(reviewData, &fixture); err != nil {
		t.Fatal(err)
	}
	base := fixture.Events[0]
	fixture.Events = nil
	for i, elementID := range []string{treeTopicID, treeConceptID, treeInvalidID, treeFutureID} {
		event := base
		event.EventID = "non-item-introduction-" + elementID
		event.OccurredAt = base.OccurredAt.Add(time.Duration(i) * time.Minute)
		event.ElementID = elementID
		event.Before.ElementID = elementID
		event.After.ElementID = elementID
		event.After.AdoptedTerminalID = event.EventID
		fixture.Events = append(fixture.Events, event)
	}
	writeTestJSON(t, reviewPath, fixture)

	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	due, err := engine.Query(t.Context(), Query{Kind: QueryElementSubset, Subset: "due"})
	if err != nil {
		t.Fatal(err)
	}
	if len(due.Items) != 0 {
		t.Fatalf("non-Items entered Item queue: %#v", due.Items)
	}
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil {
		t.Fatal(err)
	}
	if started.Session == nil || started.Session.Status != SessionCompleted {
		t.Fatalf("non-Items started an Item session: %#v", started.Session)
	}
	grade := 4
	for _, elementID := range []string{treeTopicID, treeConceptID, treeInvalidID, treeFutureID} {
		if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: elementID, RawGrade: &grade, EventID: "non-item-grade-" + elementID}); !hasCode(err, ErrInvalidSessionPhase) {
			t.Fatalf("direct non-Item grade error [%s] = %v", elementID, err)
		}
	}
	after, err := config.LoadEventFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(fixture.Events) {
		t.Fatalf("non-Item grading wrote an event: before=%d after=%d", len(fixture.Events), len(after))
	}
}

func TestEngineItemQueueRejectsIncompatibleScheduleCapability(t *testing.T) {
	config := copyFixtureWorkspace(t)
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	reviewData, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	var reviewFile EventFile
	if err = json.Unmarshal(reviewData, &reviewFile); err != nil {
		t.Fatal(err)
	}
	reviewFile.Events[0].After.ScheduleProfile = "topic-afactor-v1"
	reviewFile.Events[0].After.AcceptedReviewAction = "NextTopic"
	writeTestJSON(t, reviewPath, reviewFile)

	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	due, err := engine.Query(t.Context(), Query{Kind: QueryElementSubset, Subset: "due"})
	if err != nil {
		t.Fatal(err)
	}
	if len(due.Items) != 0 {
		t.Fatalf("Item with incompatible schedule capability entered grading queue: %#v", due.Items)
	}
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil {
		t.Fatal(err)
	}
	if started.Session == nil || started.Session.Status != SessionCompleted {
		t.Fatalf("incompatible Item started a grading session: %#v", started.Session)
	}
}

func TestEngineAllRawGrades(t *testing.T) {
	for raw := 0; raw <= 5; raw++ {
		t.Run(string(rune('0'+raw)), func(t *testing.T) {
			engine, _ := newFixtureEngine(t)
			if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart}); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
				t.Fatal(err)
			}
			grade := raw
			result, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "20260719090000-review-" + string(rune('0'+raw))})
			if err != nil || result.RawGrade == nil || *result.RawGrade != raw {
				t.Fatalf("grade %d result=%#v err=%v", raw, result, err)
			}
			if result.Passed == nil || *result.Passed != (raw >= 3) {
				t.Fatalf("grade %d passed=%v", raw, result.Passed)
			}
		})
	}
}

func TestReadOnlyEngineRejectsGradeBeforeStateMutation(t *testing.T) {
	config := copyFixtureWorkspace(t)
	config.ReadOnly = true
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
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	beforeEvents, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSession, err := engine.Query(t.Context(), Query{Kind: QueryCurrentSession})
	if err != nil {
		t.Fatal(err)
	}
	beforeProjection, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}

	grade := 4
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "read-only-grade"}); !hasCode(err, ErrUnsupportedOperation) {
		t.Fatalf("read-only grade error = %v", err)
	}
	afterEvents, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	afterSession, err := engine.Query(t.Context(), Query{Kind: QueryCurrentSession})
	if err != nil {
		t.Fatal(err)
	}
	afterProjection, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterEvents) != string(beforeEvents) {
		t.Fatal("read-only grade appended or rewrote review history")
	}
	if !reflect.DeepEqual(afterSession, beforeSession) {
		t.Fatalf("read-only grade mutated session\nbefore=%#v\nafter=%#v", beforeSession, afterSession)
	}
	if !reflect.DeepEqual(afterProjection, beforeProjection) {
		t.Fatalf("read-only grade mutated projection\nbefore=%#v\nafter=%#v", beforeProjection, afterProjection)
	}
}

func TestShowAnswerAndGradeErrors(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	grade := 4
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "bad-phase"}); !hasCode(err, ErrInvalidSessionPhase) {
		t.Fatalf("grade before start = %v", err)
	}
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: "other"}); !hasCode(err, ErrTargetMismatch) {
		t.Fatalf("wrong show target = %v", err)
	}
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	bad := 6
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &bad, EventID: "bad-grade"}); !hasCode(err, ErrUnsupportedGrade) {
		t.Fatalf("bad grade = %v", err)
	}
}

func TestGradeEventIdentityIsIdempotent(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	_, _ = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	_, _ = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID})
	grade := 4
	first, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "same-review"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "same-review"})
	if err != nil || !second.ReviewAccepted || second.EventID != first.EventID {
		t.Fatalf("retry = %#v, err=%v", second, err)
	}
	conflictingGrade := 5
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &conflictingGrade, EventID: "same-review"}); !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("conflicting retry error = %v", err)
	}
}

func TestEngineEmptyQueueAndCurrentSession(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	start, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	if err != nil || start.Session == nil || start.Session.Phase != PhaseQuestion {
		t.Fatalf("start = %#v, err=%v", start, err)
	}
	current, err := engine.Query(context.Background(), Query{Kind: QueryCurrentSession})
	if err != nil || current.Session == nil || current.Session.Phase != PhaseQuestion || current.Session.Current == nil || current.Session.Current.Answer != "" {
		t.Fatalf("current = %#v, err=%v", current, err)
	}
	_, _ = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID})
	grade := 4
	_, _ = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "empty-queue"})
	restarted, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	if err != nil || restarted.Session == nil || restarted.Session.Status != SessionCompleted || restarted.Session.Current != nil {
		t.Fatalf("empty start = %#v, err=%v", restarted, err)
	}
}

func TestEngineAcceptedGradeRetriesQueueAdvancement(t *testing.T) {
	engine, config, secondElementPath, secondElementData := newTwoItemFixtureEngine(t)
	start, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	if err != nil || start.Session == nil || start.Session.Current == nil || start.Session.Current.ElementID != fixtureElementID {
		t.Fatalf("start = %#v, err=%v", start, err)
	}
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(secondElementPath); err != nil {
		t.Fatal(err)
	}

	eventID := "accepted-before-queue-failure"
	grade := 4
	_, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: eventID})
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrQueueAdvanceFailed || !domainErr.Retryable || !domainErr.ReviewAccepted || domainErr.AcceptedEventID != eventID {
		t.Fatalf("queue advancement error = %#v", domainErr)
	}
	if domainErr.Session == nil || domainErr.Session.Status != SessionActive || domainErr.Session.Phase != PhaseAnswer || domainErr.Session.Current == nil || domainErr.Session.Current.ElementID != fixtureElementID || domainErr.Session.PendingAcceptedEventID != eventID {
		t.Fatalf("recoverable session = %#v", domainErr.Session)
	}

	otherGrade := 5
	_, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &otherGrade, EventID: "different-event"})
	if !hasCode(err, ErrQueueAdvanceFailed) {
		t.Fatalf("different retry error = %v", err)
	}
	if err = os.WriteFile(secondElementPath, secondElementData, 0644); err != nil {
		t.Fatal(err)
	}
	recovered, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: eventID})
	if err != nil || !recovered.ReviewAccepted || recovered.Session == nil || recovered.Session.Phase != PhaseQuestion || recovered.Session.Current == nil || recovered.Session.Current.ElementID != secondFixtureElementID {
		t.Fatalf("recovered = %#v, err=%v", recovered, err)
	}
	events, err := config.LoadEventFiles()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.EventID == eventID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("accepted event count = %d", count)
	}
}

const secondFixtureElementID = "20260719010102-hijklmn"

func newTwoItemFixtureEngine(t *testing.T) (*Engine, Config, string, []byte) {
	t.Helper()
	config := copyFixtureWorkspace(t)
	firstElementData, err := os.ReadFile(filepath.Join(config.ElementsRoot(), fixtureElementID+".sme"))
	if err != nil {
		t.Fatal(err)
	}
	var second Element
	if err = json.Unmarshal(firstElementData, &second); err != nil {
		t.Fatal(err)
	}
	second.ID = secondFixtureElementID
	second.Payload.Prompt = "Second prompt"
	second.Payload.Answer = "Second answer"
	secondElementData, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	secondElementPath := filepath.Join(config.ElementsRoot(), secondFixtureElementID+".sme")
	if err = os.WriteFile(secondElementPath, secondElementData, 0644); err != nil {
		t.Fatal(err)
	}

	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	reviewData, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	var file EventFile
	if err = json.Unmarshal(reviewData, &file); err != nil {
		t.Fatal(err)
	}
	secondIntroduction := file.Events[0]
	secondIntroduction.EventID = "20260716080500-intro002"
	secondIntroduction.OccurredAt = time.Date(2026, time.July, 16, 8, 5, 0, 0, config.Location)
	secondIntroduction.ElementID = secondFixtureElementID
	secondIntroduction.Before.ElementID = secondFixtureElementID
	secondIntroduction.After.ElementID = secondFixtureElementID
	secondIntroduction.After.AdoptedTerminalID = secondIntroduction.EventID
	secondIntroduction.After.DueAt = time.Date(2026, time.July, 19, 8, 30, 0, 0, config.Location)
	file.Events = append(file.Events, secondIntroduction)
	writeTestJSON(t, reviewPath, file)

	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, config, secondElementPath, secondElementData
}
