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
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSessionStartCanRetryAfterLearningPlanFailure(t *testing.T) {
	config := copyFixtureWorkspace(t)
	const blockTopicID = "20260722010101-blocktp"
	writeBlockTopicElement(t, config, blockTopicID, "20260722010101-blockaa")
	planErr := errors.New("load learning material")
	reader := &fakeBlockReferenceReader{lookupErr: planErr}
	config.BlockReader = reader
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); !errors.Is(err, planErr) {
		t.Fatalf("first Start error = %v", err)
	}
	if current := engine.session.Current(); current.Status == SessionActive {
		t.Fatalf("failed Start published an active session: %#v", current)
	}

	reader.lookupErr = nil
	retried, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || retried.Session == nil || retried.Session.Status != SessionActive || retried.Session.Current == nil || retried.Session.Current.ElementID != fixtureElementID {
		t.Fatalf("retried Start = %#v, err=%v", retried.Session, err)
	}
}

func TestTopicTargetLoadUsesElementLevelUnavailableError(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	const missingTopicID = "20260722020701-missing"
	engine.session.mu.Lock()
	engine.session.state = SessionState{SessionID: "topic-load-session", Status: SessionActive, Stage: StageOutstanding, Phase: PhaseQuestion}
	engine.session.remainingTargets = []ReviewTarget{{Kind: "element.topic", ElementID: missingTopicID}}
	err := engine.session.advanceLocked(t.Context())
	state := engine.session.publicState()
	engine.session.mu.Unlock()
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrAuthoritativeElementUnavailable || domainErr.Message != "Element is unavailable" {
		t.Fatalf("Topic target-load error = %#v", domainErr)
	}
	if state.Current != nil || len(state.RemainingElementIDs) != 0 || len(engine.session.remainingTargets) != 1 || engine.session.remainingTargets[0].ElementID != missingTopicID {
		t.Fatalf("failed Topic target load mutated session: %#v", state)
	}
}

func TestPendingStopDiscardsOnlyLocalSessionCursor(t *testing.T) {
	session := &learningSession{
		state: SessionState{
			SessionID: "pending-stop-session",
			Status:    SessionActive,
			Stage:     StagePending,
			Phase:     PhaseQuestion,
			Current:   &ReviewTarget{Kind: "element.item", ElementID: "pending-current"},
			RemainingElementIDs: []string{
				"pending-next",
			},
		},
		currentAnswer:    "answer",
		remainingTargets: []ReviewTarget{{Kind: "element.topic", ElementID: "pending-next"}},
	}
	stopped := session.Stop()
	if stopped.Status != SessionCompleted || stopped.Stage != StageCompleted || stopped.Phase != PhaseComplete || stopped.Current != nil || len(stopped.RemainingElementIDs) != 0 {
		t.Fatalf("stopped Pending session = %#v", stopped)
	}
	if session.currentAnswer != "" || len(session.remainingTargets) != 0 || session.pendingErrorCode != "" || session.drillOrder != (finalDrillOrderState{}) {
		t.Fatalf("Stop retained disposable cursor state: %#v", session)
	}
}

func TestSessionStateReturnsDeepOwnedCopies(t *testing.T) {
	lastReviewAt := time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)
	lastGrade := 4
	projection := SchedulingProjection{
		ElementID:    "owned-target",
		LastReviewAt: &lastReviewAt,
		LastRawGrade: &lastGrade,
		AlgorithmStates: map[string]VersionedAlgorithmState{
			"owned": {Algorithm: "owned", SchemaVersion: 1, State: map[string]any{"nested": map[string]any{"value": "original"}}},
		},
	}
	session := &learningSession{state: SessionState{
		Status:              SessionActive,
		Stage:               StagePending,
		Phase:               PhaseQuestion,
		Current:             &ReviewTarget{ElementID: "owned-target", Answer: "hidden", ObservedProjection: projection},
		RemainingElementIDs: []string{"remaining-target"},
		Confirmation:        &StageConfirmation{Stage: StagePending},
		LastProjection:      &projection,
	}}

	returned := session.Current()
	returned.Current.ElementID = "mutated-current"
	returned.Current.ObservedProjection.AlgorithmStates["owned"] = VersionedAlgorithmState{Algorithm: "mutated"}
	returned.RemainingElementIDs[0] = "mutated-remaining"
	returned.Confirmation.Stage = StageFinalDrill
	*returned.LastProjection.LastRawGrade = 0
	returnedState := returned.LastProjection.AlgorithmStates["owned"]
	returnedState.State.(map[string]any)["nested"].(map[string]any)["value"] = "mutated"

	actual := session.Current()
	if actual.Current == nil || actual.Current.ElementID != "owned-target" || actual.RemainingElementIDs[0] != "remaining-target" || actual.Confirmation == nil || actual.Confirmation.Stage != StagePending {
		t.Fatalf("caller mutated SessionState ownership: %#v", actual)
	}
	actualState := actual.LastProjection.AlgorithmStates["owned"]
	actualNested := actualState.State.(map[string]any)["nested"].(map[string]any)
	if *actual.LastProjection.LastRawGrade != 4 || actualState.Algorithm != "owned" || actualNested["value"] != "original" || actual.Current.ObservedProjection.AlgorithmStates["owned"].Algorithm != "owned" {
		t.Fatalf("caller mutated scheduling projection ownership: %#v", actual)
	}

	domainErr := domainErrorWithSession(ErrInvalidSessionPhase, "owned state", actual)
	domainErr.Session.RemainingElementIDs[0] = "mutated-error"
	domainErr.Session.LastProjection.AlgorithmStates["owned"] = VersionedAlgorithmState{Algorithm: "mutated"}
	if current := session.Current(); current.RemainingElementIDs[0] != "remaining-target" || current.LastProjection.AlgorithmStates["owned"].Algorithm != "owned" {
		t.Fatalf("caller mutated DomainError Session ownership: %#v", current)
	}
}

func TestFinalDrillFlipIsDeterministicAcrossSeedsAndQueueSizes(t *testing.T) {
	for size := 1; size <= 20; size++ {
		targets := makeDrillTargets(size)
		for seedNumber := 0; seedNumber < 20; seedNumber++ {
			seed := fmt.Sprintf("seed-%02d", seedNumber)
			leftState := finalDrillOrderState{Version: finalDrillOrderVersion, Seed: seed}
			rightState := leftState
			left := append([]ReviewTarget(nil), targets...)
			right := append([]ReviewTarget(nil), targets...)
			for step := 0; step < 100; step++ {
				leftBefore := append([]ReviewTarget(nil), left...)
				left = flipFinalDrillTargets(left, &leftState)
				right = flipFinalDrillTargets(right, &rightState)
				if !reflect.DeepEqual(left, right) || leftState != rightState {
					t.Fatalf("size=%d seed=%q step=%d is not deterministic", size, seed, step)
				}
				if !sameTargetIDs(left, targets) {
					t.Fatalf("size=%d seed=%q step=%d lost or duplicated a target", size, seed, step)
				}
				assertFinalDrillFlipBounds(t, leftBefore, left)
			}
			if size < 5 && leftState.Cursor != 0 {
				t.Fatalf("size=%d consumed random selections for tail rotation", size)
			}
		}
	}
}

func TestFinalDrillFlipSeedsProduceDifferentValidSequences(t *testing.T) {
	targets := makeDrillTargets(20)
	sequences := map[string]struct{}{}
	for seedNumber := 0; seedNumber < 20; seedNumber++ {
		state := finalDrillOrderState{Version: finalDrillOrderVersion, Seed: fmt.Sprintf("seed-%02d", seedNumber)}
		queue := append([]ReviewTarget(nil), targets...)
		sequence := ""
		for step := 0; step < 20; step++ {
			queue = flipFinalDrillTargets(queue, &state)
			for _, target := range queue {
				sequence += target.ElementID + ","
			}
			sequence += ";"
		}
		sequences[sequence] = struct{}{}
	}
	if len(sequences) < 10 {
		t.Fatalf("20 seeds produced only %d distinct sequences", len(sequences))
	}
}

func TestFinalDrillRandomSelectionSupportsLargePopulations(t *testing.T) {
	state := finalDrillOrderState{Version: finalDrillOrderVersion, Seed: "large-population"}
	selected := make(chan int, 1)
	go func() {
		selected <- state.intN(300)
	}()
	select {
	case value := <-selected:
		if value < 0 || value >= 300 {
			t.Fatalf("large-population selection = %d", value)
		}
	case <-time.After(time.Second):
		t.Fatal("large-population selection did not terminate")
	}
}

func TestFinalDrillFailedMembersRemainReachableWithinEightQueueCycles(t *testing.T) {
	for size := 1; size <= 20; size++ {
		for seedNumber := 0; seedNumber < 20; seedNumber++ {
			queue := makeDrillTargets(size)
			state := finalDrillOrderState{Version: finalDrillOrderVersion, Seed: fmt.Sprintf("reachability-%02d", seedNumber)}
			visits := map[string]int{queue[0].ElementID: 1}
			bound := size * 8
			for step := 0; step < bound && !allDrillTargetsVisitedTwice(queue, visits); step++ {
				queue = append(append([]ReviewTarget(nil), queue[1:]...), queue[0])
				queue = flipFinalDrillTargets(queue, &state)
				visits[queue[0].ElementID]++
			}
			if !allDrillTargetsVisitedTwice(queue, visits) {
				t.Fatalf("size=%d seed=%d exceeded %d-step reachability bound: visits=%#v", size, seedNumber, bound, visits)
			}
		}
	}
}

func allDrillTargetsVisitedTwice(targets []ReviewTarget, visits map[string]int) bool {
	for _, target := range targets {
		if visits[target.ElementID] < 2 {
			return false
		}
	}
	return true
}

func TestFinalDrillReselectionIncludesNewGlobalAdmission(t *testing.T) {
	config, ids := finalDrillFixtureConfig(t, 5, nil)
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	state := enterFinalDrillSession(t, engine)
	initialOrder := engine.session.drillOrder

	const addedID = "20260722020101-newdril"
	element := mustElementFile(t, filepath.Join(config.ElementsRoot(), ids[0]+".sme"))
	element.ID = addedID
	element.Payload.Prompt = "Newly admitted Drill Item"
	addRootElement(t, config, element)
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	file.Events = append(file.Events, modernItemIntroductionEvent(t, config, addedID, "20260722020101-new-admission", 10, 3))
	writeTestJSON(t, reviewPath, file)
	if err = engine.refreshProjection(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: state.Current.ElementID}); err != nil {
		t.Fatal(err)
	}
	grade := 2
	graded, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: state.Current.ElementID, RawGrade: &grade, EventID: "20260722020102-current-retained"})
	if err != nil || graded.Session == nil || graded.Session.Current == nil {
		t.Fatalf("grade after new admission = %#v, err=%v", graded, err)
	}
	got := append([]string{graded.Session.Current.ElementID}, graded.Session.RemainingElementIDs...)
	want := append(append([]string(nil), ids...), addedID)
	if !sameStringSet(got, want) {
		t.Fatalf("reselected Drill membership = %#v, want set %#v", got, want)
	}
	if engine.session.drillOrder.Seed != initialOrder.Seed || engine.session.drillOrder.Cursor <= initialOrder.Cursor {
		t.Fatalf("Drill order state was reset: before=%#v after=%#v", initialOrder, engine.session.drillOrder)
	}
}

func TestFinalDrillReselectionExcludesRemovedGlobalMember(t *testing.T) {
	config, ids := finalDrillFixtureConfig(t, 5, nil)
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	state := enterFinalDrillSession(t, engine)
	initialOrder := engine.session.drillOrder
	removedID := ids[2]
	plan, err := engine.scheduler.BuildLearningPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	removedTarget := reviewTargetByID(t, plan.FinalDrill, removedID)
	if _, err = engine.scheduler.ApplyDrillGrade(t.Context(), removedTarget, "20260722020201-external-remove", "external-session", 4); err != nil {
		t.Fatal(err)
	}

	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: state.Current.ElementID}); err != nil {
		t.Fatal(err)
	}
	grade := 2
	graded, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: state.Current.ElementID, RawGrade: &grade, EventID: "20260722020202-current-retained"})
	if err != nil || graded.Session == nil || graded.Session.Current == nil {
		t.Fatalf("grade after external removal = %#v, err=%v", graded, err)
	}
	got := append([]string{graded.Session.Current.ElementID}, graded.Session.RemainingElementIDs...)
	want := make([]string, 0, len(ids)-1)
	for _, id := range ids {
		if id != removedID {
			want = append(want, id)
		}
	}
	if !sameStringSet(got, want) {
		t.Fatalf("removed Drill member remained in reselection: got=%#v want=%#v", got, want)
	}
	if engine.session.drillOrder.Seed != initialOrder.Seed || engine.session.drillOrder.Cursor != initialOrder.Cursor {
		t.Fatalf("short reconciled queue changed local order state: before=%#v after=%#v", initialOrder, engine.session.drillOrder)
	}
}

func TestFinalDrillReselectionDropsExpiredGeneration(t *testing.T) {
	current := time.Date(2026, time.July, 19, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	config, _ := finalDrillFixtureConfig(t, 5, func() time.Time { return current })
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	state := enterFinalDrillSession(t, engine)
	current = time.Date(2026, time.July, 20, 9, 0, 0, 0, current.Location())

	engine.session.mu.Lock()
	lastProjection := state.Current.ObservedProjection
	lastFinalDrill := state.Current.ObservedFinalDrillProjection
	engine.session.state.LastProjection = &lastProjection
	engine.session.state.LastFinalDrillProjection = &lastFinalDrill
	err = engine.session.advanceLocked(t.Context())
	result := engine.session.publicState()
	engine.session.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SessionCompleted || result.Current != nil || len(result.RemainingElementIDs) != 0 {
		t.Fatalf("expired generation remained selectable: %#v", result)
	}
}

func TestFinalDrillReconciliationFailurePreservesLocalOrderAndAcceptedRetry(t *testing.T) {
	config, ids := finalDrillFixtureConfig(t, 5, nil)
	const blockTopicID = "20260722020401-blocktp"
	const blockID = "20260722020401-blockaa"
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
	state := enterFinalDrillSession(t, engine)
	beforeOrder := append([]string{state.Current.ElementID}, state.RemainingElementIDs...)
	beforeDrillOrder := engine.session.drillOrder
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: state.Current.ElementID}); err != nil {
		t.Fatal(err)
	}

	reconcileErr := errors.New("reconcile Final Drill membership")
	reader.lookupErr = reconcileErr
	grade := 2
	eventID := "20260722020402-accepted-before-reconcile-failure"
	_, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: state.Current.ElementID, RawGrade: &grade, EventID: eventID})
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrQueueAdvanceFailed || !domainErr.ReviewAccepted || domainErr.AcceptedEventID != eventID || !errors.Is(domainErr.Cause, reconcileErr) {
		t.Fatalf("reconciliation failure = %#v", domainErr)
	}
	failed := engine.session.Current()
	failedOrder := append([]string{failed.Current.ElementID}, failed.RemainingElementIDs...)
	if !reflect.DeepEqual(failedOrder, beforeOrder) || engine.session.drillOrder != beforeDrillOrder {
		t.Fatalf("failed reconciliation mutated local order: before=%#v/%#v after=%#v/%#v", beforeOrder, beforeDrillOrder, failedOrder, engine.session.drillOrder)
	}
	different := eventID + "-different"
	if _, retryErr := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: state.Current.ElementID, RawGrade: &grade, EventID: different}); !hasCode(retryErr, ErrQueueAdvanceFailed) {
		t.Fatalf("different accepted-action retry = %v", retryErr)
	}
	reader.lookupErr = nil
	recovered, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: state.Current.ElementID, RawGrade: &grade, EventID: eventID})
	if err != nil || recovered.Session == nil || recovered.Session.Current == nil {
		t.Fatalf("same accepted-action retry = %#v, err=%v", recovered, err)
	}
	got := append([]string{recovered.Session.Current.ElementID}, recovered.Session.RemainingElementIDs...)
	if !sameStringSet(got, ids) || countEventsByID(t, config, eventID) != 1 {
		t.Fatalf("recovered Drill state = %#v, event count=%d", got, countEventsByID(t, config, eventID))
	}
}

func enterFinalDrillSession(t *testing.T, engine *Engine) SessionState {
	t.Helper()
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil || started.Session.Confirmation == nil || started.Session.Confirmation.Stage != StageFinalDrill {
		t.Fatalf("start Final Drill = %#v, err=%v", started, err)
	}
	accepted, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill})
	if err != nil || accepted.Session == nil || accepted.Session.Current == nil {
		t.Fatalf("accept Final Drill = %#v, err=%v", accepted, err)
	}
	return *accepted.Session
}

func reviewTargetByID(t *testing.T, targets []ReviewTarget, elementID string) ReviewTarget {
	t.Helper()
	for _, target := range targets {
		if target.ElementID == elementID {
			return target
		}
	}
	t.Fatalf("ReviewTarget %q not found", elementID)
	return ReviewTarget{}
}

func makeDrillTargets(size int) []ReviewTarget {
	targets := make([]ReviewTarget, size)
	for i := range targets {
		targets[i].ElementID = fmt.Sprintf("target-%02d", i)
	}
	return targets
}

func sameTargetIDs(left, right []ReviewTarget) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	for _, target := range left {
		counts[target.ElementID]++
	}
	for _, target := range right {
		counts[target.ElementID]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func assertFinalDrillFlipBounds(t *testing.T, before, after []ReviewTarget) {
	t.Helper()
	if len(before) < 5 {
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("queue shorter than five was flipped: before=%#v after=%#v", before, after)
		}
		return
	}
	if reflect.DeepEqual(before, after) {
		return
	}
	for source := 4; source < len(before); source++ {
		for destination := 2; destination < len(before) && destination < 6; destination++ {
			candidate := moveDrillTarget(before, source, destination)
			if reflect.DeepEqual(candidate, after) {
				return
			}
		}
	}
	t.Fatalf("flip moved outside [5,n] -> [3,min(6,n)]: before=%#v after=%#v", before, after)
}

func moveDrillTarget(targets []ReviewTarget, source, destination int) []ReviewTarget {
	target := targets[source]
	result := append([]ReviewTarget(nil), targets[:source]...)
	result = append(result, targets[source+1:]...)
	result = append(result, ReviewTarget{})
	copy(result[destination+1:], result[destination:])
	result[destination] = target
	return result
}
