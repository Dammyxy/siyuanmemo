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
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const (
	dailyTopicID        = "20260719020101-topicaa"
	dailyPendingItemID  = "20260719020102-penditm"
	dailyPendingTopicID = "20260719020103-pendtop"
)

func TestLearningDayConfigResolvesShiftedBoundary(t *testing.T) {
	config := Config{SchedulerRoot: t.TempDir()}
	for name, schedulerConfig := range defaultSchedulerConfigs() {
		writeTestJSON(t, filepath.Join(config.SchedulerRoot, name+".json"), schedulerConfig)
	}
	writeTestJSON(t, filepath.Join(config.SchedulerRoot, "learning-day.json"), LearningDayConfigV1{Spec: 1, TimeZoneIANA: "Asia/Shanghai", MidnightShiftHours: 4})
	effective := config.LoadEffectiveSchedulerConfig()
	if !effective.PersistedComplete {
		t.Fatalf("learning day config diagnostics = %#v", effective.Diagnostics)
	}
	for shift := 0; shift <= 16; shift++ {
		effective.LearningDay.MidnightShiftHours = shift
		before := time.Date(2026, time.July, 22, shift, 0, 0, -1, effective.LearningDayLocation)
		at := time.Date(2026, time.July, 22, shift, 0, 0, 0, effective.LearningDayLocation)
		after := time.Date(2026, time.July, 22, shift, 0, 0, 1, effective.LearningDayLocation)
		if got := effective.ResolveLearningDayID(before); got != "2026-07-21" {
			t.Fatalf("shift %d before boundary day = %s", shift, got)
		}
		if got := effective.ResolveLearningDayID(at); got != "2026-07-22" {
			t.Fatalf("shift %d at boundary day = %s", shift, got)
		}
		if got := effective.ResolveLearningDayID(after); got != "2026-07-22" {
			t.Fatalf("shift %d after boundary day = %s", shift, got)
		}
	}
}

func TestOutstandingTopicNextUsesLearningDayAndDoesNotGrade(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	addRootElement(t, config, Element{
		Spec:            1,
		ID:              dailyTopicID,
		Type:            "topic",
		Title:           "Due Topic",
		ProcessingState: "reading",
		PayloadSpec:     1,
		Payload:         ElementPayload{Material: &TopicMaterial{Kind: "html", HTML: "<p>Topic body</p>"}},
	})
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	topicIntroEventID := topicIntroductionEventID(t, "20260716081000-topic-intro", dailyTopicID, 2)
	topicIntro := SchedulingEvent{
		Spec:                     1,
		EventID:                  topicIntroEventID,
		OccurredAt:               time.Date(2026, time.July, 17, 8, 10, 0, 0, config.Location),
		Type:                     "introduceElement",
		ElementID:                dailyTopicID,
		ReviewKind:               "introduceTopic",
		LearningDate:             "2026-07-17",
		LearningDayID:            "2026-07-17",
		TopicPolicyVersion:       "siyuanmemo-topic-initial-v1",
		TopicInitialIntervalDays: 2,
		TopicSeed:                topicInitialSeed(topicIntroEventID, dailyTopicID),
		TopicEffectiveAFactor:    2.5,
		Before:                   SchedulingProjection{ElementID: dailyTopicID, LifecycleState: "pending", PriorityPosition: 1},
		After: SchedulingProjection{
			ElementID:            dailyTopicID,
			ScheduleProfile:      topicAFactorV1ID,
			AcceptedReviewAction: "NextTopic",
			LifecycleState:       "memorized",
			AdoptedTerminalID:    topicIntroEventID,
			DueAt:                time.Date(2026, time.July, 19, 10, 0, 0, 0, config.Location),
			DueLearningDay:       "2026-07-19",
			IntervalDays:         2,
			Repetitions:          1,
			LastReviewAt:         timePointer(time.Date(2026, time.July, 17, 8, 10, 0, 0, config.Location)),
			LastLearningDate:     "2026-07-17",
			ActiveAlgorithm:      topicAFactorV1ID,
			AlgorithmStates:      map[string]VersionedAlgorithmState{topicAFactorV1ID: {Algorithm: topicAFactorV1ID, SchemaVersion: 1, State: TopicAFactorV1State{IntervalDays: 2, Repetitions: 1, LastLearningDay: "2026-07-17", DueLearningDay: "2026-07-19", EffectiveAFactor: 2.5}}},
			PriorityPosition:     1,
		},
		TopicNextIntervalDays: 2,
	}
	file.Events = append([]SchedulingEvent{topicIntro}, file.Events...)
	writeTestJSON(t, reviewPath, file)

	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	start, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	if err != nil || start.Session == nil || start.Session.Current == nil || start.Session.Current.ElementID != fixtureElementID {
		t.Fatalf("start = %#v err=%v", start, err)
	}
	grade := 4
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: dailyTopicID, RawGrade: &grade, EventID: "topic-grade-forbidden"}); !hasCode(err, ErrInvalidSessionPhase) {
		t.Fatalf("topic grade error = %v", err)
	}
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: "20260719090000-item-before-topic"}); err != nil {
		t.Fatal(err)
	}
	next, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionNextTopic, ElementID: dailyTopicID, EventID: "20260719090100-topic-next"})
	if err != nil || !next.ReviewAccepted || next.Projection == nil || next.Projection.IntervalDays != 5 || next.Projection.DueLearningDay != "2026-07-24" {
		if next.Projection != nil {
			_, diagnostics := projectSchedulingEvents(mustEvents(t, config), "2026-07-19")
			t.Fatalf("topic next projection = %#v diagnostics=%#v err=%v", *next.Projection, diagnostics, err)
		}
		t.Fatalf("topic next = %#v err=%v", next, err)
	}
	events, err := config.LoadEventFiles()
	if err != nil {
		t.Fatal(err)
	}
	event := eventByID(t, events, "20260719090100-topic-next")
	if event.ReviewKind != "nextTopic" || event.RawGrade != nil || event.Passed != nil || event.LearningDayID != "2026-07-19" || event.TopicNextIntervalDays != 5 {
		t.Fatalf("topic event = %#v", event)
	}
	if len(event.AlgorithmCandidates) != 1 || event.AlgorithmCandidates[0].Algorithm != topicAFactorV1ID || event.AlgorithmCandidates[0].Status != "valid" || event.AlgorithmCandidates[0].NextIntervalDays != event.TopicNextIntervalDays {
		t.Fatalf("topic candidate facts = %#v", event.AlgorithmCandidates)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err = json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["topicMinimumIntervalDays"] != float64(1) || persisted["topicMaximumIntervalDays"] != float64(36500) {
		t.Fatalf("topic formula bounds were not persisted: %#v", persisted)
	}
}

func TestOutstandingExcludesUnavailableBlockBackedTopics(t *testing.T) {
	const blockElementID = "20260719020105-blocktp"
	const blockID = "20260719020105-blockaa"
	tests := []struct {
		name       string
		resolution BlockReferenceResolution
	}{
		{name: "unavailable", resolution: BlockReferenceResolution{BlockID: blockID, Status: MaterialSourceUnavailable}},
		{name: "unresolved", resolution: BlockReferenceResolution{BlockID: blockID, Status: MaterialSourceUnresolved}},
		{name: "encrypted", resolution: BlockReferenceResolution{BlockID: blockID, Status: MaterialSourceAvailable, Encrypted: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := copyFixtureWorkspace(t)
			installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
			addDueDailyTopic(t, config, blockElementID, "Block Topic", "2026-07-19", 1)
			writeBlockTopicElement(t, config, blockElementID, blockID)
			reader := &fakeBlockReferenceReader{lookupResults: map[string]BlockReferenceResolution{blockID: test.resolution}}
			config.BlockReader = reader
			engine, err := NewEngine(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Close() })

			due, err := engine.Query(t.Context(), Query{Kind: QueryElementSubset, Subset: "due"})
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range due.Items {
				if target.ElementID == blockElementID {
					t.Fatalf("%s Block-backed Topic entered Outstanding: %#v", test.name, due.Items)
				}
			}
		})
	}
}

func TestLegacyOpaqueTopicProfileRemainsReadOnly(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	elementID := "20260719020109-legacyt"
	addRootElement(t, config, pendingTopicElement(elementID, "Legacy opaque Topic"))
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	event := cloneSchedulingEvent(t, file.Events[0])
	event.EventID = "20260716080000-legacy-topic"
	event.ElementID = elementID
	event.After.ScheduleProfile = topicAFactorV1ID
	event.After.AcceptedReviewAction = "NextTopic"
	alignLegacyItemIntroductionState(t, &event)
	file.Events = []SchedulingEvent{event}
	writeTestJSON(t, reviewPath, file)

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
		t.Fatalf("legacy opaque Topic entered Outstanding: %#v", due.Items)
	}
}

func TestNextTopicRechecksBlockMaterialBeforeWrite(t *testing.T) {
	const blockElementID = "20260719020105-blocktp"
	const blockID = "20260719020105-blockaa"
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	addDueDailyTopic(t, config, blockElementID, "Block Topic", "2026-07-19", 1)
	writeBlockTopicElement(t, config, blockElementID, blockID)
	reader := &fakeBlockReferenceReader{
		lookupResults: map[string]BlockReferenceResolution{blockID: {BlockID: blockID, Status: MaterialSourceAvailable}},
		loadResults:   map[string]BlockReferenceResolution{blockID: {BlockID: blockID, Status: MaterialSourceUnavailable}},
	}
	config.BlockReader = reader
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil || started.Session.Current == nil || started.Session.Current.ElementID != blockElementID {
		t.Fatalf("start = %#v, err=%v", started, err)
	}
	before := len(mustEvents(t, config))
	_, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionNextTopic, ElementID: blockElementID, EventID: "blocked-topic-next"})
	if !hasCode(err, ErrElementSourceUnavailable) {
		t.Fatalf("unavailable Block-backed Topic error = %v", err)
	}
	if after := len(mustEvents(t, config)); after != before {
		t.Fatalf("unavailable Block-backed Topic wrote an event: before=%d after=%d", before, after)
	}
	current, queryErr := engine.Query(t.Context(), Query{Kind: QueryCurrentSession})
	if queryErr != nil || current.Session == nil || current.Session.Current == nil || current.Session.Current.ElementID != blockElementID {
		t.Fatalf("current session after rejected Topic Next = %#v, err=%v", current.Session, queryErr)
	}
}

func TestActiveSessionCrossingMidnightShiftKeepsOrderButUsesNewLearningDay(t *testing.T) {
	config, setNow := mutableDailyConfig(t, time.Date(2026, time.July, 20, 3, 59, 59, 0, time.FixedZone("CST", 8*60*60)))
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	addDueDailyTopic(t, config, dailyTopicID, "Due Topic", "2026-07-19", 1)
	addDueDailyTopic(t, config, "20260719020104-topicbb", "Second Topic", "2026-07-19", 2)

	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	start, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	if err != nil || start.Session == nil || start.Session.Current == nil {
		t.Fatalf("start = %#v err=%v", start, err)
	}
	wantRemaining := append([]string(nil), start.Session.RemainingElementIDs...)
	setNow(time.Date(2026, time.July, 20, 4, 0, 1, 0, config.Location))
	next, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionNextTopic, ElementID: start.Session.Current.ElementID, EventID: "20260720040001-cross-boundary"})
	if err != nil || next.Session == nil {
		t.Fatalf("cross-boundary next = %#v err=%v", next, err)
	}
	if !reflect.DeepEqual(next.Session.RemainingElementIDs, wantRemaining[1:]) {
		t.Fatalf("remaining order changed before=%#v after=%#v", wantRemaining, next.Session.RemainingElementIDs)
	}
	event := eventByID(t, mustEvents(t, config), "20260720040001-cross-boundary")
	if event.LearningDayID != "2026-07-20" {
		t.Fatalf("cross-boundary learning day = %s", event.LearningDayID)
	}
}

func TestItemShortTermDueCrossingMidnightShiftReplays(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	reviewAt := time.Date(2026, time.July, 20, 3, 59, 59, 0, config.Location)
	config.Now = func() time.Time { return reviewAt }
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil || started.Session.Current == nil || started.Session.Current.ElementID != fixtureElementID {
		t.Fatalf("start = %#v, err=%v", started.Session, err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	again := 0
	graded, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &again, EventID: "20260720035959-short-term"})
	if err != nil || !graded.ReviewAccepted || graded.Projection == nil {
		t.Fatalf("short-term grade = %#v, err=%v", graded, err)
	}
	if graded.Projection.IntervalDays != 0 || !graded.Projection.DueAt.After(reviewAt) || graded.Projection.DueLearningDay != "2026-07-19" {
		t.Fatalf("short-term projection = %#v", graded.Projection)
	}
	if err = engine.refreshProjection(t.Context()); err != nil {
		t.Fatalf("short-term replay = %v", err)
	}
}

func TestDecliningPendingStillOffersFinalDrill(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	addRootElement(t, config, pendingItemElement(dailyPendingItemID, "Pending item"))
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	file.Events = []SchedulingEvent{modernItemIntroductionEvent(t, config, fixtureElementID, "20260719090000-drill-admit", 0, 3)}
	writeTestJSON(t, reviewPath, file)

	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	start, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	if err != nil || start.Session == nil || start.Session.Phase != PhaseConfirmation || start.Session.Confirmation == nil || start.Session.Confirmation.Stage != StagePending {
		t.Fatalf("pending confirmation = %#v err=%v", start, err)
	}
	declined, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionDeclineStageTransition, Stage: StagePending})
	if err != nil || declined.Session == nil || declined.Session.Phase != PhaseConfirmation || declined.Session.Confirmation == nil || declined.Session.Confirmation.Stage != StageFinalDrill {
		t.Fatalf("decline pending = %#v err=%v", declined, err)
	}
	events := mustEvents(t, config)
	if len(events) != 1 {
		t.Fatalf("declining pending wrote events: %#v", events)
	}
}

func TestPendingAcceptanceIntroducesItemsAndTopicsInGlobalOrder(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	addRootElement(t, config, pendingItemElement(dailyPendingItemID, "Pending item"))
	addRootElement(t, config, pendingTopicElement(dailyPendingTopicID, "Pending topic"))
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	moveLegacyItemIntroductionDue(t, &file.Events[0], time.Date(2026, time.July, 30, 8, 0, 0, 0, config.Location))
	writeTestJSON(t, reviewPath, file)
	beforeEvents := len(mustEvents(t, config))

	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	start, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	if err != nil || start.Session == nil || start.Session.Phase != PhaseConfirmation || start.Session.Confirmation == nil || start.Session.Confirmation.Stage != StagePending {
		t.Fatalf("pending confirmation = %#v err=%v", start, err)
	}
	if got := len(mustEvents(t, config)); got != beforeEvents {
		t.Fatalf("pending confirmation wrote events: before=%d after=%d", beforeEvents, got)
	}
	accepted, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StagePending})
	if err != nil || accepted.Session == nil || accepted.Session.Current == nil || accepted.Session.Current.ElementID != dailyPendingItemID || accepted.Session.RemainingElementIDs[0] != dailyPendingTopicID {
		t.Fatalf("accepted pending order = %#v err=%v", accepted, err)
	}
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionNextTopic, ElementID: dailyPendingItemID, EventID: "pending-item-next-forbidden"}); !hasCode(err, ErrInvalidSessionPhase) {
		t.Fatalf("pending item next error = %v", err)
	}
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: dailyPendingItemID}); err != nil {
		t.Fatal(err)
	}
	low := 2
	itemResult, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: dailyPendingItemID, RawGrade: &low, EventID: "20260719092000-pending-item"})
	if err != nil || itemResult.Projection == nil || itemResult.Projection.LifecycleState != "memorized" || itemResult.FinalDrillProjection == nil || !itemResult.FinalDrillProjection.Member || itemResult.Session == nil || itemResult.Session.Current == nil || itemResult.Session.Current.ElementID != dailyPendingTopicID {
		t.Fatalf("pending item grade = %#v err=%v", itemResult, err)
	}
	itemEvent := eventByID(t, mustEvents(t, config), "20260719092000-pending-item")
	if itemEvent.Type != "introduceElement" || itemEvent.ReviewKind != "introduceItem" || itemEvent.BaseEventID != "" || itemEvent.DrillEffect != "admit" {
		t.Fatalf("pending item event = %#v", itemEvent)
	}
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: dailyPendingTopicID, RawGrade: &low, EventID: "pending-topic-grade-forbidden"}); !hasCode(err, ErrInvalidSessionPhase) {
		t.Fatalf("pending topic grade error = %v", err)
	}
	topicResult, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionNextTopic, ElementID: dailyPendingTopicID, EventID: "20260719092100-pending-topic"})
	if err != nil || topicResult.Projection == nil || topicResult.Projection.LifecycleState != "memorized" || topicResult.Projection.ScheduleProfile != topicAFactorV1ID || topicResult.Session == nil || topicResult.Session.Phase != PhaseConfirmation || topicResult.Session.Confirmation == nil || topicResult.Session.Confirmation.Stage != StageFinalDrill {
		t.Fatalf("pending topic next = %#v err=%v", topicResult, err)
	}
	topicEvent := eventByID(t, mustEvents(t, config), "20260719092100-pending-topic")
	if topicEvent.Type != "introduceElement" || topicEvent.ReviewKind != "introduceTopic" || topicEvent.RawGrade != nil || topicEvent.TopicPolicyVersion != "siyuanmemo-topic-initial-v1" || topicEvent.TopicInitialIntervalDays < 1 || topicEvent.TopicInitialIntervalDays > 15 {
		t.Fatalf("pending topic event = %#v", topicEvent)
	}
}

func TestLedgerRejectsIncompleteTopicIntroductionFacts(t *testing.T) {
	valid := acceptedPendingTopicIntroductionEvent(t)
	tests := []struct {
		name   string
		reason string
		mutate func(*SchedulingEvent)
	}{
		{name: "policy", reason: "topic-initial-policy-invalid", mutate: func(event *SchedulingEvent) { event.TopicPolicyVersion = "" }},
		{name: "seed", reason: "topic-initial-policy-invalid", mutate: func(event *SchedulingEvent) { event.TopicSeed = "" }},
		{name: "seed identity", reason: "topic-initial-policy-invalid", mutate: func(event *SchedulingEvent) { event.TopicSeed = "forged-seed" }},
		{name: "initial interval", reason: "topic-initial-policy-invalid", mutate: func(event *SchedulingEvent) { event.TopicInitialIntervalDays = 0 }},
		{name: "initial interval formula", reason: "topic-initial-policy-invalid", mutate: func(event *SchedulingEvent) {
			forged := event.TopicInitialIntervalDays%15 + 1
			event.TopicInitialIntervalDays = forged
			event.TopicNextIntervalDays = forged
			event.After.IntervalDays = forged
			event.After.DueLearningDay = addLearningDays(eventLearningDayID(*event), forged)
			state, err := decodeAlgorithmState[TopicAFactorV1State](event.After.AlgorithmStates[topicAFactorV1ID], topicAFactorV1ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			state.IntervalDays = forged
			state.DueLearningDay = event.After.DueLearningDay
			versioned := event.After.AlgorithmStates[topicAFactorV1ID]
			versioned.State = state
			event.After.AlgorithmStates[topicAFactorV1ID] = versioned
		}},
		{name: "repetitions", reason: "topic-repetition-invalid", mutate: func(event *SchedulingEvent) {
			state, err := decodeAlgorithmState[TopicAFactorV1State](event.After.AlgorithmStates[topicAFactorV1ID], topicAFactorV1ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			event.After.Repetitions++
			state.Repetitions = event.After.Repetitions
			versioned := event.After.AlgorithmStates[topicAFactorV1ID]
			versioned.State = state
			event.After.AlgorithmStates[topicAFactorV1ID] = versioned
		}},
		{name: "lapse", reason: "topic-item-memory-state-invalid", mutate: func(event *SchedulingEvent) { event.After.Lapses++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			malformed := cloneSchedulingEvent(t, valid)
			test.mutate(&malformed)
			projections, diagnostics := projectSchedulingEvents([]SchedulingEvent{malformed})
			if projection := projections[malformed.ElementID]; projection.ElementID != "" {
				t.Fatalf("malformed topic introduction drove projection: %#v", projection)
			}
			if diagnostic := diagnosticByID(diagnostics, malformed.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != test.reason {
				t.Fatalf("malformed topic diagnostic = %#v", diagnostic)
			}
		})
	}
}

func TestTopicAndDrillEventIdentityIsIdempotent(t *testing.T) {
	t.Run("topic", func(t *testing.T) {
		engine, config := newPendingTopicOnlyEngine(t)
		if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart}); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StagePending}); err != nil {
			t.Fatal(err)
		}
		eventID := "20260719092300-topic-idempotent"
		first, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionNextTopic, ElementID: dailyPendingTopicID, EventID: eventID})
		if err != nil || !first.ReviewAccepted {
			t.Fatalf("first topic next = %#v err=%v", first, err)
		}
		second, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionNextTopic, ElementID: dailyPendingTopicID, EventID: eventID})
		if err != nil || !second.ReviewAccepted || second.EventID != eventID {
			t.Fatalf("second topic next = %#v err=%v", second, err)
		}
		if countEventsByID(t, config, eventID) != 1 {
			t.Fatalf("topic idempotent retry wrote duplicate events")
		}
	})

	t.Run("drill", func(t *testing.T) {
		engine, config := newFixtureEngine(t)
		if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart}); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
			t.Fatal(err)
		}
		low := 2
		if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &low, EventID: "20260719092400-admit-drill"}); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill}); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
			t.Fatal(err)
		}
		good := 4
		eventID := "20260719092500-drill-idempotent"
		first, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeDrill, ElementID: fixtureElementID, RawGrade: &good, EventID: eventID})
		if err != nil || !first.ReviewAccepted {
			t.Fatalf("first drill grade = %#v err=%v", first, err)
		}
		second, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeDrill, ElementID: fixtureElementID, RawGrade: &good, EventID: eventID})
		if err != nil || !second.ReviewAccepted || second.EventID != eventID {
			t.Fatalf("second drill grade = %#v err=%v", second, err)
		}
		if countEventsByID(t, config, eventID) != 1 {
			t.Fatalf("drill idempotent retry wrote duplicate events")
		}
	})
}

func TestFinalDrillGradeIsScheduleNeutralAndRemovesMembership(t *testing.T) {
	engine, config := newFixtureEngine(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	low := 2
	graded, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &low, EventID: "20260719091000-admit-drill"})
	if err != nil || graded.Session == nil || graded.Session.Phase != PhaseConfirmation || graded.Session.Confirmation == nil || graded.Session.Confirmation.Stage != StageFinalDrill {
		t.Fatalf("admit drill = %#v err=%v", graded, err)
	}
	formalProjection := *graded.Projection
	accepted, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill})
	if err != nil || accepted.Session == nil || accepted.Session.Current == nil || accepted.Session.Stage != StageFinalDrill {
		t.Fatalf("accept drill = %#v err=%v", accepted, err)
	}
	if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	good := 4
	drilled, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeDrill, ElementID: fixtureElementID, RawGrade: &good, EventID: "20260719091100-drill-good"})
	if err != nil || !drilled.ReviewAccepted || drilled.Projection == nil || drilled.FinalDrillProjection == nil || drilled.FinalDrillProjection.Member {
		t.Fatalf("drill grade = %#v err=%v", drilled, err)
	}
	if drilled.Projection.DueAt != formalProjection.DueAt || drilled.Projection.IntervalDays != formalProjection.IntervalDays || drilled.Projection.Repetitions != formalProjection.Repetitions || drilled.Projection.Lapses != formalProjection.Lapses || drilled.Projection.AdoptedTerminalID != formalProjection.AdoptedTerminalID || drilled.Projection.ActiveAlgorithm != formalProjection.ActiveAlgorithm {
		t.Fatalf("drill changed formal schedule\nbefore=%#v\nafter=%#v", formalProjection, drilled.Projection)
	}
	event := eventByID(t, mustEvents(t, config), "20260719091100-drill-good")
	if event.Type != "drillElement" || event.ReviewKind != "drillGrade" || event.BaseEventID != "" || event.DrillEffect != "remove" {
		t.Fatalf("drill event = %#v", event)
	}
}

func TestFinalDrillMembershipGradeMatrix(t *testing.T) {
	for raw := 0; raw <= 5; raw++ {
		t.Run(fmt.Sprintf("formal_%d", raw), func(t *testing.T) {
			engine, _ := newFixtureEngine(t)
			if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
				t.Fatal(err)
			}
			grade := raw
			result, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: fmt.Sprintf("formal-drill-membership-%d", raw)})
			member := result.FinalDrillProjection != nil && result.FinalDrillProjection.Member
			if err != nil || result.Projection == nil || member != (raw <= 3) {
				t.Fatalf("formal grade %d projections = formal %#v, Drill %#v, err=%v", raw, result.Projection, result.FinalDrillProjection, err)
			}
		})

		t.Run(fmt.Sprintf("pending_%d", raw), func(t *testing.T) {
			config, elementID, _ := projectionFailurePendingItemFixture(t)
			engine, err := NewEngine(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Close() })
			if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
				t.Fatal(err)
			}
			if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StagePending}); err != nil {
				t.Fatal(err)
			}
			if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: elementID}); err != nil {
				t.Fatal(err)
			}
			grade := raw
			result, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: elementID, RawGrade: &grade, EventID: fmt.Sprintf("pending-drill-membership-%d", raw)})
			member := result.FinalDrillProjection != nil && result.FinalDrillProjection.Member
			if err != nil || result.Projection == nil || member != (raw <= 3) {
				t.Fatalf("Pending grade %d projections = formal %#v, Drill %#v, err=%v", raw, result.Projection, result.FinalDrillProjection, err)
			}
		})

		t.Run(fmt.Sprintf("drill_%d", raw), func(t *testing.T) {
			config, ids := finalDrillFixtureConfig(t, 1, nil)
			engine, err := NewEngine(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Close() })
			before, err := engine.ledger.Snapshot(ids[0])
			if err != nil {
				t.Fatal(err)
			}
			if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
				t.Fatal(err)
			}
			if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill}); err != nil {
				t.Fatal(err)
			}
			if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: ids[0]}); err != nil {
				t.Fatal(err)
			}
			grade := raw
			result, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: ids[0], RawGrade: &grade, EventID: fmt.Sprintf("drill-membership-%d", raw)})
			if err != nil || result.Projection == nil || result.FinalDrillProjection == nil || result.FinalDrillProjection.Member != (raw <= 3) {
				t.Fatalf("Drill grade %d projections = formal %#v, Drill %#v, err=%v", raw, result.Projection, result.FinalDrillProjection, err)
			}
			after := *result.Projection
			if after.DueAt != before.DueAt || after.IntervalDays != before.IntervalDays || after.Repetitions != before.Repetitions || after.Lapses != before.Lapses || after.AdoptedTerminalID != before.AdoptedTerminalID || after.ActiveAlgorithm != before.ActiveAlgorithm {
				t.Fatalf("Drill grade %d changed formal schedule\nbefore=%#v\nafter=%#v", raw, before, after)
			}
		})
	}
}

func TestFinalDrillDynamicFlipOrderForQueueSizes(t *testing.T) {
	for size := 1; size <= 20; size++ {
		t.Run(fmt.Sprintf("size_%02d", size), func(t *testing.T) {
			engine, ids := newFinalDrillFixtureEngine(t, size)
			start, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
			if err != nil || start.Session == nil || start.Session.Phase != PhaseConfirmation || start.Session.Confirmation == nil || start.Session.Confirmation.Stage != StageFinalDrill {
				t.Fatalf("final drill confirmation = %#v err=%v", start, err)
			}
			accepted, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill})
			if err != nil || accepted.Session == nil || accepted.Session.Current == nil || accepted.Session.Current.ElementID != ids[0] {
				t.Fatalf("accept final drill = %#v err=%v", accepted, err)
			}
			if _, err = engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionShowAnswer, ElementID: ids[0]}); err != nil {
				t.Fatal(err)
			}
			low := 2
			graded, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionGradeDrill, ElementID: ids[0], RawGrade: &low, EventID: fmt.Sprintf("202607190930%02d-drill-low", size)})
			if err != nil || graded.Session == nil || graded.Session.Current == nil {
				t.Fatalf("drill low grade = %#v err=%v", graded, err)
			}
			got := append([]string{graded.Session.Current.ElementID}, graded.Session.RemainingElementIDs...)
			if !sameStringSet(got, ids) {
				t.Fatalf("drill order lost or duplicated members: got=%#v ids=%#v", got, ids)
			}
			beforeFlip := append(append([]string(nil), ids[1:]...), ids[0])
			assertFinalDrillFlipBounds(t, reviewTargetsForIDs(beforeFlip), reviewTargetsForIDs(got))
		})
	}
}

func TestFinalDrillRepeatedFailuresReachEveryMemberAgainWithinBound(t *testing.T) {
	for size := 1; size <= 20; size++ {
		t.Run(fmt.Sprintf("size_%02d", size), func(t *testing.T) {
			engine, ids := newFinalDrillFixtureEngine(t, size)
			state := enterFinalDrillSession(t, engine)
			visits := map[string]int{state.Current.ElementID: 1}
			bound := size * 8
			for step := 0; step < bound; step++ {
				complete := true
				for _, id := range ids {
					if visits[id] < 2 {
						complete = false
						break
					}
				}
				if complete {
					return
				}
				if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: state.Current.ElementID}); err != nil {
					t.Fatal(err)
				}
				grade := 2
				graded, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: state.Current.ElementID, RawGrade: &grade, EventID: fmt.Sprintf("2026072205%02d%04d-reach", size, step)})
				if err != nil || graded.Session == nil || graded.Session.Current == nil {
					t.Fatalf("step %d grade = %#v, err=%v", step, graded, err)
				}
				state = *graded.Session
				visits[state.Current.ElementID]++
			}
			t.Fatalf("not every failed member returned within %d steps: visits=%#v", bound, visits)
		})
	}
}

func TestFinalDrillGenerationExpiresAfterThreeCompleteLearningDays(t *testing.T) {
	current := time.Date(2026, time.July, 19, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	config, _ := finalDrillFixtureConfig(t, 1, func() time.Time { return current })
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	present, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	if err != nil || present.Session == nil || present.Session.Phase != PhaseConfirmation || present.Session.Confirmation == nil || present.Session.Confirmation.Stage != StageFinalDrill {
		t.Fatalf("final drill before expiry = %#v err=%v", present, err)
	}
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}

	current = time.Date(2026, time.July, 20, 9, 0, 0, 0, current.Location())
	reopened, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	expired, err := reopened.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart})
	if err != nil || expired.Session == nil || expired.Session.Status != SessionCompleted {
		t.Fatalf("final drill after expiry = %#v err=%v", expired, err)
	}
	projection, err := reopened.ledger.FinalDrillSnapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Member {
		t.Fatalf("expired drill member persisted in projection: %#v", projection)
	}
}

func TestLongLivedEngineExpiresFinalDrillBeforeStopToStartPlanBuild(t *testing.T) {
	current := time.Date(2026, time.July, 19, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	config, _ := finalDrillFixtureConfig(t, 1, func() time.Time { return current })
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	present, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || present.Session == nil || present.Session.Confirmation == nil || present.Session.Confirmation.Stage != StageFinalDrill {
		t.Fatalf("Final Drill before fourth day = %#v, err=%v", present.Session, err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStop}); err != nil {
		t.Fatal(err)
	}
	beforeEvents := len(mustEvents(t, config))
	current = time.Date(2026, time.July, 20, 9, 0, 0, 0, current.Location())
	expired, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || expired.Session == nil || expired.Session.Status != SessionCompleted {
		t.Fatalf("long-lived Final Drill after expiry = %#v, err=%v", expired.Session, err)
	}
	if afterEvents := len(mustEvents(t, config)); afterEvents != beforeEvents {
		t.Fatalf("plan-build expiry wrote authority: before=%d after=%d", beforeEvents, afterEvents)
	}
	projection, err := engine.ledger.FinalDrillSnapshot(fixtureElementID)
	if err != nil || projection.Member || !projection.Expired {
		t.Fatalf("expired long-lived Drill projection = %#v, err=%v", projection, err)
	}
}

func TestLongLivedExpiredFinalDrillDoesNotReviveAndNewAdmissionStartsFresh(t *testing.T) {
	current := time.Date(2026, time.July, 19, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	config, _ := finalDrillFixtureConfig(t, 1, func() time.Time { return current })
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	formal, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	drill, err := engine.ledger.FinalDrillSnapshot(fixtureElementID)
	if err != nil || !drill.Member {
		t.Fatalf("initial Drill projection = %#v, err=%v", drill, err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStop}); err != nil {
		t.Fatal(err)
	}
	current = time.Date(2026, time.July, 20, 9, 0, 0, 0, current.Location())
	expired, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || expired.Session == nil || expired.Session.Status != SessionCompleted {
		t.Fatalf("expire long-lived generation = %#v, err=%v", expired, err)
	}

	normalized, err := NormalizeGrade(2)
	if err != nil {
		t.Fatal(err)
	}
	delayed := SchedulingEvent{
		Spec:                  SupportedEventSpec,
		EventID:               "20260720090000-delayed-old-generation",
		OccurredAt:            current,
		Type:                  "drillElement",
		ElementID:             fixtureElementID,
		SessionID:             "delayed-session",
		ReviewKind:            "drillGrade",
		RawGrade:              intPointer(2),
		Passed:                boolPointer(normalized.Passed),
		RatingLabel:           normalized.RatingLabel,
		RatingMapping:         normalized.RatingMapping,
		LearningDate:          "2026-07-20",
		LearningDayID:         "2026-07-20",
		DrillEffect:           "retain",
		DrillAdmissionEventID: drill.AdmissionEventID,
		Before:                formal,
		After:                 formal,
	}
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	file.Events = append(file.Events, delayed)
	writeTestJSON(t, reviewPath, file)
	if err = engine.refreshProjection(t.Context()); err != nil {
		t.Fatal(err)
	}
	afterDelayed, err := engine.ledger.FinalDrillSnapshot(fixtureElementID)
	if err != nil || afterDelayed.Member || !afterDelayed.Expired || afterDelayed.AdoptedTerminalEventID != "" {
		t.Fatalf("delayed activity revived old generation: %#v, err=%v", afterDelayed, err)
	}

	const newID = "20260722050201-newgene"
	addRootElement(t, config, pendingItemElement(newID, "New generation Item"))
	if err = engine.refreshProjection(t.Context()); err != nil {
		t.Fatal(err)
	}
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil || started.Session.Confirmation == nil || started.Session.Confirmation.Stage != StagePending {
		t.Fatalf("new generation Pending confirmation = %#v, err=%v", started, err)
	}
	accepted, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StagePending})
	if err != nil || accepted.Session == nil || accepted.Session.Current == nil || accepted.Session.Current.ElementID != newID {
		t.Fatalf("enter new generation Pending = %#v, err=%v", accepted, err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: newID}); err != nil {
		t.Fatal(err)
	}
	grade := 3
	const newEventID = "20260720090100-new-generation-admission"
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: newID, RawGrade: &grade, EventID: newEventID}); err != nil {
		t.Fatal(err)
	}
	oldProjection, err := engine.ledger.FinalDrillSnapshot(fixtureElementID)
	if err != nil || oldProjection.Member || !oldProjection.Expired {
		t.Fatalf("new generation restored former member: %#v, err=%v", oldProjection, err)
	}
	newProjection, err := engine.ledger.FinalDrillSnapshot(newID)
	if err != nil || !newProjection.Member || newProjection.Expired || newProjection.AdmissionEventID != newEventID {
		t.Fatalf("new generation projection = %#v, err=%v", newProjection, err)
	}
}

func TestLearningDayConfigurationChangePreservesHistoryAndChangesFutureActions(t *testing.T) {
	tests := []struct {
		name     string
		oldZone  string
		oldShift int
		newZone  string
		newShift int
		oldDay   string
		newDay   string
	}{
		{name: "time zone", oldZone: "UTC", oldShift: 4, newZone: "Asia/Shanghai", newShift: 4, oldDay: "2026-07-19", newDay: "2026-07-20"},
		{name: "Midnight Shift", oldZone: "UTC", oldShift: 0, newZone: "UTC", newShift: 4, oldDay: "2026-07-20", newDay: "2026-07-19"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := time.Date(2026, time.July, 20, 2, 0, 0, 0, time.UTC)
			config := copyFixtureWorkspace(t)
			config.Location = time.UTC
			config.Now = func() time.Time { return current }
			installDailyLearningConfig(t, config, test.oldZone, test.oldShift)
			reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
			file := readEventFile(t, reviewPath)
			moveLegacyItemIntroductionDue(t, &file.Events[0], time.Date(2026, time.July, 30, 8, 0, 0, 0, time.UTC))
			writeTestJSON(t, reviewPath, file)
			const firstID = "20260722030101-firstpd"
			const secondID = "20260722030102-secondp"
			addRootElement(t, config, pendingItemElement(firstID, "First Pending Item"))
			addRootElement(t, config, pendingItemElement(secondID, "Second Pending Item"))

			engine, err := NewEngine(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
			if err != nil || started.Session == nil || started.Session.Confirmation == nil || started.Session.Confirmation.Stage != StagePending {
				t.Fatalf("start Pending = %#v, err=%v", started, err)
			}
			accepted, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StagePending})
			if err != nil || accepted.Session == nil || accepted.Session.Current == nil || accepted.Session.Current.ElementID != firstID {
				t.Fatalf("accept Pending = %#v, err=%v", accepted, err)
			}
			if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: firstID}); err != nil {
				t.Fatal(err)
			}
			grade := 5
			const firstEventID = "20260722030103-first-event"
			if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: firstID, RawGrade: &grade, EventID: firstEventID}); err != nil {
				t.Fatal(err)
			}
			if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStop}); err != nil {
				t.Fatal(err)
			}
			firstEvent := eventByID(t, mustEvents(t, config), firstEventID)
			if firstEvent.LearningDayID != test.oldDay {
				t.Fatalf("first Learning Day = %s, want %s", firstEvent.LearningDayID, test.oldDay)
			}
			if err = engine.Close(); err != nil {
				t.Fatal(err)
			}
			beforeRebuild, err := os.ReadFile(reviewPath)
			if err != nil {
				t.Fatal(err)
			}

			writeTestJSON(t, filepath.Join(config.SchedulerRoot, "learning-day.json"), LearningDayConfigV1{Spec: 1, TimeZoneIANA: test.newZone, MidnightShiftHours: test.newShift})
			removeSQLiteFiles(config.IndexPath())
			rebuilt, err := NewEngine(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rebuilt.Close() })
			afterRebuild, err := os.ReadFile(reviewPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(afterRebuild) != string(beforeRebuild) {
				t.Fatal("rebuild rewrote accepted Learning Day authority")
			}
			replayed := eventByID(t, mustEvents(t, config), firstEventID)
			if replayed.LearningDayID != test.oldDay || replayed.LearningDate != test.oldDay {
				t.Fatalf("historical Learning Day changed: %#v", replayed)
			}
			started, err = rebuilt.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
			if err != nil || started.Session == nil || started.Session.Confirmation == nil || started.Session.Confirmation.Stage != StagePending {
				t.Fatalf("restart Pending = %#v, err=%v", started, err)
			}
			accepted, err = rebuilt.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StagePending})
			if err != nil || accepted.Session == nil || accepted.Session.Current == nil || accepted.Session.Current.ElementID != secondID {
				t.Fatalf("restart accepts remaining Pending = %#v, err=%v", accepted, err)
			}
			if _, err = rebuilt.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: secondID}); err != nil {
				t.Fatal(err)
			}
			const secondEventID = "20260722030104-second-event"
			if _, err = rebuilt.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: secondID, RawGrade: &grade, EventID: secondEventID}); err != nil {
				t.Fatal(err)
			}
			secondEvent := eventByID(t, mustEvents(t, config), secondEventID)
			if secondEvent.LearningDayID != test.newDay || eventByID(t, mustEvents(t, config), firstEventID).LearningDayID != test.oldDay {
				t.Fatalf("Learning Day transition first=%s second=%s", firstEvent.LearningDayID, secondEvent.LearningDayID)
			}
		})
	}
}

func TestPendingStopLeavesUntouchedTargetsForNextSessionInGlobalOrder(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	moveLegacyItemIntroductionDue(t, &file.Events[0], time.Date(2026, time.July, 30, 8, 0, 0, 0, config.Location))
	writeTestJSON(t, reviewPath, file)
	ids := []string{"20260722040101-pendone", "20260722040102-pendtwo", "20260722040103-pendthr"}
	for _, id := range ids {
		addRootElement(t, config, pendingItemElement(id, id))
	}
	beforeEvents := len(mustEvents(t, config))
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil || started.Session.Confirmation == nil || started.Session.Confirmation.Stage != StagePending {
		t.Fatalf("start Pending = %#v, err=%v", started, err)
	}
	accepted, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StagePending})
	if err != nil || accepted.Session == nil || accepted.Session.Current == nil || accepted.Session.Current.ElementID != ids[0] {
		t.Fatalf("accept Pending = %#v, err=%v", accepted, err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: ids[0]}); err != nil {
		t.Fatal(err)
	}
	grade := 5
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: ids[0], RawGrade: &grade, EventID: "20260722040104-introduced"}); err != nil {
		t.Fatal(err)
	}
	stopped, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStop})
	if err != nil || stopped.Session == nil || stopped.Session.Status != SessionCompleted {
		t.Fatalf("stop Pending = %#v, err=%v", stopped, err)
	}
	if got := len(mustEvents(t, config)); got != beforeEvents+1 {
		t.Fatalf("Pending Stop wrote untouched events: got=%d want=%d", got, beforeEvents+1)
	}
	for _, event := range mustEvents(t, config) {
		if event.ElementID == ids[1] || event.ElementID == ids[2] {
			t.Fatalf("untouched Pending target wrote event: %#v", event)
		}
	}
	plan, err := engine.scheduler.BuildLearningPlan(t.Context())
	if err != nil || len(plan.Pending) != 2 || plan.Pending[0].ElementID != ids[1] || plan.Pending[1].ElementID != ids[2] || plan.Pending[0].ObservedProjection.LifecycleState != "pending" || plan.Pending[1].ObservedProjection.LifecycleState != "pending" {
		t.Fatalf("remaining Pending plan = %#v, err=%v", plan.Pending, err)
	}
	restarted, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || restarted.Session == nil || restarted.Session.Confirmation == nil || restarted.Session.Confirmation.Stage != StagePending {
		t.Fatalf("restart Pending = %#v, err=%v", restarted, err)
	}
	reentered, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StagePending})
	if err != nil || reentered.Session == nil || reentered.Session.Current == nil || reentered.Session.Current.ElementID != ids[1] || !reflect.DeepEqual(reentered.Session.RemainingElementIDs, ids[2:]) {
		t.Fatalf("reentered Pending order = %#v, err=%v", reentered, err)
	}
}

func installDailyLearningConfig(t *testing.T, config Config, zone string, shift int) {
	t.Helper()
	writeTestJSON(t, filepath.Join(config.SchedulerRoot, "learning-day.json"), LearningDayConfigV1{Spec: 1, TimeZoneIANA: zone, MidnightShiftHours: shift})
	writeTestJSON(t, filepath.Join(config.SchedulerRoot, "topic-afactor-v1.json"), SchedulerConfig{Spec: 1, Algorithm: topicAFactorV1ID, AFactor: 2.5, MinimumIntervalDays: 1, MaximumIntervalDays: 36500, SkipPolicy: "none"})
}

func mutableDailyConfig(t *testing.T, now time.Time) (Config, func(time.Time)) {
	t.Helper()
	config := copyFixtureWorkspace(t)
	current := now
	config.Location = now.Location()
	config.Now = func() time.Time { return current }
	return config, func(next time.Time) { current = next }
}

func addDueDailyTopic(t *testing.T, config Config, id, title, dueDay string, priority float64) {
	t.Helper()
	addRootElement(t, config, Element{Spec: 1, ID: id, Type: "topic", Title: title, ProcessingState: "reading", PayloadSpec: 1, Payload: ElementPayload{Material: &TopicMaterial{Kind: "html", HTML: "<p>" + title + "</p>"}}})
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	eventID := topicIntroductionEventID(t, id+"-intro", id, 2)
	dueAt := mustLearningDayBoundary(t, config, dueDay)
	learningAt := dueAt.AddDate(0, 0, -2)
	learningDay := config.LoadEffectiveSchedulerConfig().ResolveLearningDayID(learningAt)
	file.Events = append(file.Events, SchedulingEvent{
		Spec:                     1,
		EventID:                  eventID,
		OccurredAt:               learningAt,
		Type:                     "introduceElement",
		ElementID:                id,
		ReviewKind:               "introduceTopic",
		LearningDate:             learningDay,
		LearningDayID:            learningDay,
		TopicPolicyVersion:       "siyuanmemo-topic-initial-v1",
		TopicInitialIntervalDays: 2,
		TopicSeed:                topicInitialSeed(eventID, id),
		TopicEffectiveAFactor:    2.5,
		TopicNextIntervalDays:    2,
		Before:                   SchedulingProjection{ElementID: id, LifecycleState: "pending", PriorityPosition: priority},
		After: SchedulingProjection{
			ElementID:            id,
			ScheduleProfile:      topicAFactorV1ID,
			AcceptedReviewAction: "NextTopic",
			LifecycleState:       "memorized",
			AdoptedTerminalID:    eventID,
			DueAt:                dueAt,
			DueLearningDay:       dueDay,
			IntervalDays:         2,
			Repetitions:          1,
			LastReviewAt:         timePointer(learningAt),
			LastLearningDate:     learningDay,
			ActiveAlgorithm:      topicAFactorV1ID,
			AlgorithmStates:      map[string]VersionedAlgorithmState{topicAFactorV1ID: {Algorithm: topicAFactorV1ID, SchemaVersion: 1, State: TopicAFactorV1State{IntervalDays: 2, Repetitions: 1, LastLearningDay: learningDay, DueLearningDay: dueDay, EffectiveAFactor: 2.5}}},
			PriorityPosition:     priority,
		},
	})
	writeTestJSON(t, reviewPath, file)
}

func topicIntroductionEventID(t *testing.T, prefix, elementID string, interval int) string {
	t.Helper()
	for suffix := 0; suffix < 1000; suffix++ {
		candidate := fmt.Sprintf("%s-%03d", prefix, suffix)
		if topicInitialInterval(topicInitialSeed(candidate, elementID)) == interval {
			return candidate
		}
	}
	t.Fatalf("no Topic introduction Event ID produced interval %d", interval)
	return ""
}

func addRootElement(t *testing.T, config Config, element Element) {
	t.Helper()
	data, err := json.MarshalIndent(element, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config.ElementsRoot(), element.ID+".sme"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}

func pendingItemElement(id, prompt string) Element {
	return Element{Spec: 1, ID: id, Type: "item", Title: prompt, ProcessingState: "processed", PayloadSpec: 1, Payload: ItemPayload{Kind: "qa", Prompt: prompt, Answer: "answer"}}
}

func pendingTopicElement(id, title string) Element {
	return Element{Spec: 1, ID: id, Type: "topic", Title: title, ProcessingState: "reading", PayloadSpec: 1, Payload: ElementPayload{Material: &TopicMaterial{Kind: "html", HTML: "<p>" + title + "</p>"}}}
}

func acceptedPendingTopicIntroductionEvent(t *testing.T) SchedulingEvent {
	t.Helper()
	engine, config := newPendingTopicOnlyEngine(t)
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StagePending}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: ActionNextTopic, ElementID: dailyPendingTopicID, EventID: "20260719092200-topic-valid"}); err != nil {
		t.Fatal(err)
	}
	return eventByID(t, mustEvents(t, config), "20260719092200-topic-valid")
}

func newPendingTopicOnlyEngine(t *testing.T) (*Engine, Config) {
	t.Helper()
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	addRootElement(t, config, pendingTopicElement(dailyPendingTopicID, "Pending topic"))
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	moveLegacyItemIntroductionDue(t, &file.Events[0], time.Date(2026, time.July, 30, 8, 0, 0, 0, config.Location))
	writeTestJSON(t, reviewPath, file)
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, config
}

func countEventsByID(t *testing.T, config Config, eventID string) int {
	t.Helper()
	count := 0
	for _, event := range mustEvents(t, config) {
		if event.EventID == eventID {
			count++
		}
	}
	return count
}

func readEventFile(t *testing.T, path string) EventFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file EventFile
	if err = json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func mustEvents(t *testing.T, config Config) []SchedulingEvent {
	t.Helper()
	events, err := config.LoadEventFiles()
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func eventByID(t *testing.T, events []SchedulingEvent, id string) SchedulingEvent {
	t.Helper()
	for _, event := range events {
		if event.EventID == id {
			return event
		}
	}
	t.Fatalf("event %s not found in %#v", id, events)
	return SchedulingEvent{}
}

func mustLearningDayBoundary(t *testing.T, config Config, day string) time.Time {
	t.Helper()
	effective := config.LoadEffectiveSchedulerConfig()
	boundary, err := effective.TimeForLearningDayID(day)
	if err != nil {
		t.Fatal(err)
	}
	return boundary
}

func newFinalDrillFixtureEngine(t *testing.T, size int) (*Engine, []string) {
	t.Helper()
	config, ids := finalDrillFixtureConfig(t, size, nil)
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, ids
}

func finalDrillFixtureConfig(t *testing.T, size int, now func() time.Time) (Config, []string) {
	t.Helper()
	config := copyFixtureWorkspace(t)
	if now != nil {
		current := now()
		config.Location = current.Location()
		config.Now = now
	}
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	baseElement := mustElementFile(t, filepath.Join(config.ElementsRoot(), fixtureElementID+".sme"))
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	file.Events = nil
	ids := make([]string, 0, size)
	for i := 0; i < size; i++ {
		id := fixtureElementID
		if i > 0 {
			id = fmt.Sprintf("2026071903%04d-drillit", i)
			element := baseElement
			element.ID = id
			element.Payload.Prompt = fmt.Sprintf("Drill prompt %02d", i)
			element.Payload.Answer = fmt.Sprintf("Drill answer %02d", i)
			addRootElement(t, config, element)
		}
		ids = append(ids, id)
		event := modernItemIntroductionEvent(t, config, id, fmt.Sprintf("2026071909%04d-drill-admit", i), float64(i), 3)
		file.Events = append(file.Events, event)
	}
	writeTestJSON(t, reviewPath, file)
	return config, ids
}

func modernItemIntroductionEvent(t *testing.T, config Config, elementID, eventID string, priority float64, rawGrade int) SchedulingEvent {
	t.Helper()
	review, err := NormalizeGrade(rawGrade)
	if err != nil {
		t.Fatal(err)
	}
	review.ElementID = elementID
	review.TargetKind = "element.item"
	review.ActionKind = string(ActionGradeItem)
	review.ReviewAt = config.Now().AddDate(0, 0, -3)
	review.LearningDate = config.LoadEffectiveSchedulerConfig().ResolveLearningDayID(review.ReviewAt)
	review.LearningDayID = review.LearningDate
	review.SessionID = "fixture-session"
	review.EventID = eventID
	before := SchedulingProjection{ElementID: elementID, LifecycleState: "pending", PriorityPosition: priority}
	arena := algorithmArena{primary: NewFSRSV1Adapter(defaultFSRSV1SchedulerConfig()), fallback: NewSimpleV1Adapter()}
	candidates, winner, decision, err := arena.review(AlgorithmInput{ElementID: elementID, TargetKind: review.TargetKind, Review: review, Before: before})
	if err != nil {
		t.Fatal(err)
	}
	if winner.Algorithm != fsrsV1ID {
		t.Fatalf("fixture winner = %s", winner.Algorithm)
	}
	const fixtureIntervalDays = 14
	fixtureDueAt := review.ReviewAt.AddDate(0, 0, fixtureIntervalDays)
	fsrsState, err := decodeAlgorithmState[FSRSV1State](winner.NextState, fsrsV1ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	fsrsState.DueAt = fixtureDueAt
	fsrsState.ScheduledDays = fixtureIntervalDays
	winner.NextIntervalDays = fixtureIntervalDays
	winner.NextDueAt = fixtureDueAt
	winner.NextState.State = fsrsState
	for i := range candidates {
		if candidates[i].Algorithm == winner.Algorithm {
			candidates[i] = winner
		}
	}
	after, err := projectionFromCandidate(before, winner, review)
	if err != nil {
		t.Fatal(err)
	}
	after.ScheduleProfile = fsrsV1ID
	after.AcceptedReviewAction = "GradeItem"
	after.DueLearningDay = addLearningDays(review.LearningDayID, winner.NextIntervalDays)
	for _, candidate := range candidates {
		if candidate.Status == "valid" {
			after.AlgorithmStates[candidate.Algorithm] = candidate.NextState
		}
	}
	event := SchedulingEvent{
		Spec:                SupportedEventSpec,
		EventID:             eventID,
		OccurredAt:          review.ReviewAt,
		Type:                "introduceElement",
		ElementID:           elementID,
		SessionID:           review.SessionID,
		ReviewKind:          "introduceItem",
		RawGrade:            intPointer(review.RawGrade),
		Passed:              boolPointer(review.Passed),
		RatingLabel:         review.RatingLabel,
		RatingMapping:       review.RatingMapping,
		LearningDate:        review.LearningDate,
		LearningDayID:       review.LearningDayID,
		AlgorithmDecision:   decision,
		AlgorithmCandidates: candidates,
		Before:              before,
		After:               after,
	}
	if rawGrade <= 3 {
		event.DrillEffect = "admit"
	}
	return event
}

func mustElementFile(t *testing.T, path string) Element {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var element Element
	if err = json.Unmarshal(data, &element); err != nil {
		t.Fatal(err)
	}
	return element
}

func expectedFirstFailedDrillOrder(ids []string) []string {
	order := append([]string(nil), ids[1:]...)
	order = append(order, ids[0])
	if len(order) >= 5 {
		order = moveString(order, 4, 2)
	}
	return order
}

func reviewTargetsForIDs(ids []string) []ReviewTarget {
	targets := make([]ReviewTarget, len(ids))
	for i, id := range ids {
		targets[i].ElementID = id
	}
	return targets
}

func moveString(values []string, source, destination int) []string {
	if source < 0 || source >= len(values) || destination < 0 || destination >= len(values) || source == destination {
		return values
	}
	value := values[source]
	without := append([]string{}, values[:source]...)
	without = append(without, values[source+1:]...)
	if destination > len(without) {
		destination = len(without)
	}
	result := append([]string{}, without[:destination]...)
	result = append(result, value)
	result = append(result, without[destination:]...)
	return result
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	return true
}
