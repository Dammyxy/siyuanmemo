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
	"errors"
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
	if started.Session == nil || started.Session.Phase != PhaseConfirmation || started.Session.Confirmation == nil || started.Session.Confirmation.Stage != StagePending || started.Session.Current != nil {
		t.Fatalf("non-Items started an Outstanding item session: %#v", started.Session)
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
	_, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &otherGrade, EventID: eventID})
	if !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("conflicting accepted retry error = %v", err)
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

func TestDrillAcceptedGradeRetriesQueueAdvancementWithoutDuplicateTargets(t *testing.T) {
	config, ids := finalDrillFixtureConfig(t, 5, nil)
	const blockTopicID = "20260722020501-blocktp"
	const blockID = "20260722020501-blockaa"
	addDueDailyTopic(t, config, blockTopicID, "Future Block Topic", "2026-07-30", 20)
	topic := mustElementFile(t, filepath.Join(config.ElementsRoot(), blockTopicID+".sme"))
	topic.Payload.Material = &TopicMaterial{Kind: "siyuanBlock", BlockID: blockID}
	addRootElement(t, config, topic)
	reader := &fakeBlockReferenceReader{lookupResults: map[string]BlockReferenceResolution{
		blockID: {BlockID: blockID, Status: MaterialSourceAvailable},
	}}
	config.BlockReader = reader
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil || started.Session.Confirmation == nil || started.Session.Confirmation.Stage != StageFinalDrill {
		t.Fatalf("start Final Drill = %#v, err=%v", started, err)
	}
	accepted, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill})
	if err != nil || accepted.Session == nil || accepted.Session.Current == nil || accepted.Session.Current.ElementID != ids[0] {
		t.Fatalf("accept Final Drill = %#v, err=%v", accepted, err)
	}
	beforeOrder := append([]string{accepted.Session.Current.ElementID}, accepted.Session.RemainingElementIDs...)
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: ids[0]}); err != nil {
		t.Fatal(err)
	}
	reader.lookupErr = errors.New("reconcile Final Drill membership")

	eventID := "accepted-drill-before-queue-failure"
	low := 2
	_, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: ids[0], RawGrade: &low, EventID: eventID})
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrQueueAdvanceFailed || !domainErr.ReviewAccepted || domainErr.AcceptedEventID != eventID || domainErr.Session == nil || domainErr.Session.Current == nil {
		t.Fatalf("Drill queue advancement error = %#v", domainErr)
	}
	failedOrder := append([]string{domainErr.Session.Current.ElementID}, domainErr.Session.RemainingElementIDs...)
	if !reflect.DeepEqual(failedOrder, beforeOrder) {
		t.Fatalf("failed Drill advancement mutated order\nbefore=%#v\nafter=%#v", beforeOrder, failedOrder)
	}
	_, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: ids[0], RawGrade: &low, EventID: eventID})
	if !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("Item action recovered accepted Drill event: %v", err)
	}
	reader.lookupErr = nil
	recovered, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: ids[0], RawGrade: &low, EventID: eventID})
	if err != nil || !recovered.ReviewAccepted || recovered.Session == nil || recovered.Session.Current == nil {
		t.Fatalf("recover Drill advancement = %#v, err=%v", recovered, err)
	}
	recoveredOrder := append([]string{recovered.Session.Current.ElementID}, recovered.Session.RemainingElementIDs...)
	if !sameStringSet(recoveredOrder, ids) || len(recoveredOrder) != len(ids) {
		t.Fatalf("recovered Drill queue lost or duplicated targets: %#v", recoveredOrder)
	}
	if countEventsByID(t, config, eventID) != 1 {
		t.Fatal("Drill queue recovery wrote a duplicate event")
	}
}

func TestLearningEventIdentityCannotCrossActionFamilies(t *testing.T) {
	t.Run("Drill event as Item grade", func(t *testing.T) {
		config, ids := finalDrillFixtureConfig(t, 1, nil)
		engine, err := NewEngine(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = engine.Close() })
		if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
			t.Fatal(err)
		}
		if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill}); err != nil {
			t.Fatal(err)
		}
		if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: ids[0]}); err != nil {
			t.Fatal(err)
		}
		grade := 4
		eventID := "cross-action-drill-event"
		if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: ids[0], RawGrade: &grade, EventID: eventID}); err != nil {
			t.Fatal(err)
		}
		if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: ids[0], RawGrade: &grade, EventID: eventID}); !hasCode(err, ErrHistoryRequiresRepair) {
			t.Fatalf("Item grade accepted Drill event identity: %v", err)
		}
	})

	t.Run("Item event as Drill grade", func(t *testing.T) {
		engine, _ := newFixtureEngine(t)
		if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
			t.Fatal(err)
		}
		grade := 3
		eventID := "cross-action-item-event"
		if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: eventID}); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: fixtureElementID, RawGrade: &grade, EventID: eventID}); !hasCode(err, ErrHistoryRequiresRepair) {
			t.Fatalf("Drill grade accepted Item event identity: %v", err)
		}
	})
}

func TestAcceptedTopicRetriesTransactionalQueueAdvancement(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	moveLegacyItemIntroductionDue(t, &file.Events[0], time.Date(2026, time.July, 30, 8, 0, 0, 0, config.Location))
	writeTestJSON(t, reviewPath, file)
	firstID := "20260719040101-duetopc"
	secondID := "20260719040102-duetopc"
	addDueDailyTopic(t, config, firstID, "First due Topic", "2026-07-18", 0)
	addDueDailyTopic(t, config, secondID, "Second due Topic", "2026-07-19", 1)
	assertAcceptedQueueAdvanceRetry(t, config, StageOutstanding, firstID, secondID, LearningAction{Kind: ActionNextTopic, EventID: "accepted-topic-before-queue-failure"})
}

func TestAcceptedPendingActionsRetryTransactionalQueueAdvancement(t *testing.T) {
	tests := []struct {
		name       string
		first      Element
		second     Element
		actionKind LearningActionKind
	}{
		{name: "Item", first: pendingItemElement("20260719040201-pnditem", "Pending Item"), second: pendingTopicElement("20260719040202-pndtopc", "Pending Topic"), actionKind: ActionGradeItem},
		{name: "Topic", first: pendingTopicElement("20260719040301-pndtopc", "Pending Topic"), second: pendingItemElement("20260719040302-pnditem", "Pending Item"), actionKind: ActionNextTopic},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := copyFixtureWorkspace(t)
			installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
			reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
			file := readEventFile(t, reviewPath)
			moveLegacyItemIntroductionDue(t, &file.Events[0], time.Date(2026, time.July, 30, 8, 0, 0, 0, config.Location))
			writeTestJSON(t, reviewPath, file)
			addRootElement(t, config, test.first)
			addRootElement(t, config, test.second)
			action := LearningAction{Kind: test.actionKind, EventID: "accepted-pending-" + test.name + "-before-queue-failure"}
			assertAcceptedQueueAdvanceRetry(t, config, StagePending, test.first.ID, test.second.ID, action)
		})
	}
}

func TestAcceptedPassingDrillRetriesTransactionalQueueAdvancement(t *testing.T) {
	config, ids := finalDrillFixtureConfig(t, 2, nil)
	grade := 4
	assertAcceptedQueueAdvanceRetry(t, config, StageFinalDrill, ids[0], ids[1], LearningAction{Kind: ActionGradeDrill, RawGrade: &grade, EventID: "accepted-passing-drill-before-queue-failure"})
}

func assertAcceptedQueueAdvanceRetry(t *testing.T, config Config, stage LearningStage, currentID, nextID string, action LearningAction) {
	t.Helper()
	var reader *fakeBlockReferenceReader
	if stage == StageFinalDrill {
		const blockTopicID = "20260722020601-blocktp"
		const blockID = "20260722020601-blockaa"
		addDueDailyTopic(t, config, blockTopicID, "Future Block Topic", "2026-07-30", 20)
		topic := mustElementFile(t, filepath.Join(config.ElementsRoot(), blockTopicID+".sme"))
		topic.Payload.Material = &TopicMaterial{Kind: "siyuanBlock", BlockID: blockID}
		addRootElement(t, config, topic)
		reader = &fakeBlockReferenceReader{lookupResults: map[string]BlockReferenceResolution{
			blockID: {BlockID: blockID, Status: MaterialSourceAvailable},
		}}
		config.BlockReader = reader
	}
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil {
		t.Fatalf("start = %#v, err=%v", started, err)
	}
	if stage != StageOutstanding {
		if started.Session.Confirmation == nil || started.Session.Confirmation.Stage != stage {
			t.Fatalf("stage confirmation = %#v", started.Session)
		}
		started, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: stage})
		if err != nil || started.Session == nil {
			t.Fatalf("accept stage = %#v, err=%v", started, err)
		}
	}
	if started.Session.Current == nil || started.Session.Current.ElementID != currentID || len(started.Session.RemainingElementIDs) == 0 || started.Session.RemainingElementIDs[0] != nextID {
		t.Fatalf("initial queue = %#v", started.Session)
	}
	beforeOrder := append([]string{currentID}, started.Session.RemainingElementIDs...)
	if action.Kind == ActionGradeItem || action.Kind == ActionGradeDrill {
		if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: currentID}); err != nil {
			t.Fatal(err)
		}
		if action.RawGrade == nil {
			grade := 4
			action.RawGrade = &grade
		}
	}
	action.ElementID = currentID
	var nextPath string
	var nextData []byte
	if stage == StageFinalDrill {
		reader.lookupErr = errors.New("reconcile Final Drill membership")
	} else {
		nextPath = filepath.Join(config.ElementsRoot(), nextID+".sme")
		nextData, err = os.ReadFile(nextPath)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(nextPath); err != nil {
			t.Fatal(err)
		}
	}
	_, err = engine.RunLearningAction(t.Context(), action)
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrQueueAdvanceFailed || !domainErr.ReviewAccepted || domainErr.AcceptedEventID != action.EventID || domainErr.Session == nil || domainErr.Session.Current == nil {
		t.Fatalf("queue advancement error = %#v", domainErr)
	}
	failedOrder := append([]string{domainErr.Session.Current.ElementID}, domainErr.Session.RemainingElementIDs...)
	if !reflect.DeepEqual(failedOrder, beforeOrder) {
		t.Fatalf("failed advancement mutated queue\nbefore=%#v\nafter=%#v", beforeOrder, failedOrder)
	}
	different := action
	different.EventID += "-different"
	if _, differentErr := engine.RunLearningAction(t.Context(), different); !hasCode(differentErr, ErrQueueAdvanceFailed) {
		t.Fatalf("different retry identity error = %v", differentErr)
	}
	if stage == StageFinalDrill {
		reader.lookupErr = nil
	} else if err = os.WriteFile(nextPath, nextData, 0644); err != nil {
		t.Fatal(err)
	}
	recovered, err := engine.RunLearningAction(t.Context(), action)
	if err != nil || !recovered.ReviewAccepted || recovered.Session == nil || recovered.Session.Current == nil || recovered.Session.Current.ElementID != nextID {
		t.Fatalf("queue recovery = %#v, err=%v", recovered, err)
	}
	if countEventsByID(t, config, action.EventID) != 1 {
		t.Fatalf("queue recovery duplicated event %q", action.EventID)
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
	secondIntroduction := cloneSchedulingEvent(t, file.Events[0])
	secondIntroduction.EventID = "20260716080500-intro002"
	secondIntroduction.OccurredAt = time.Date(2026, time.July, 16, 8, 5, 0, 0, config.Location)
	secondIntroduction.ElementID = secondFixtureElementID
	secondIntroduction.Before.ElementID = secondFixtureElementID
	secondIntroduction.After.ElementID = secondFixtureElementID
	secondIntroduction.After.AdoptedTerminalID = secondIntroduction.EventID
	secondIntroduction.After.DueAt = time.Date(2026, time.July, 19, 8, 30, 0, 0, config.Location)
	alignLegacyItemIntroductionState(t, &secondIntroduction)
	file.Events = append(file.Events, secondIntroduction)
	projections, diagnostics := projectSchedulingEvents(file.Events)
	if len(projections) != 2 {
		t.Fatalf("two-Item fixture projections = %#v, diagnostics=%#v", projections, diagnostics)
	}
	writeTestJSON(t, reviewPath, file)

	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, config, secondElementPath, secondElementData
}
