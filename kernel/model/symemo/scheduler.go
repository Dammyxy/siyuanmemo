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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"time"
)

type Scheduler struct {
	config            Config
	index             *projectionIndex
	ledger            *SchedulingLedger
	refreshProjection func(context.Context) error
	arena             algorithmArena
	topic             *TopicAFactorV1Adapter
}

func newScheduler(config Config, index *projectionIndex, ledger *SchedulingLedger, refreshProjection func(context.Context) error, primary, fallback AlgorithmAdapter) *Scheduler {
	effective := config.LoadEffectiveSchedulerConfig()
	return &Scheduler{config: config, index: index, ledger: ledger, refreshProjection: refreshProjection, arena: algorithmArena{primary: primary, fallback: fallback}, topic: NewTopicAFactorV1Adapter(effective.Topic)}
}

func (scheduler *Scheduler) BuildQueue(ctx context.Context) ([]ReviewTarget, error) {
	plan, err := scheduler.BuildLearningPlan(ctx)
	if err != nil {
		return nil, err
	}
	return plan.Outstanding, nil
}

type learningPlan struct {
	LearningDayID string
	Outstanding   []ReviewTarget
	Pending       []ReviewTarget
	FinalDrill    []ReviewTarget
}

func (scheduler *Scheduler) BuildLearningPlan(ctx context.Context) (learningPlan, error) {
	effective := scheduler.config.LoadEffectiveSchedulerConfig()
	learningDayID := effective.ResolveLearningDayID(scheduler.config.Now())
	snapshot, err := scheduler.index.snapshot()
	if err != nil {
		return learningPlan{}, err
	}
	blockedElementIDs, err := scheduler.blockedElementIDs(snapshot)
	if err != nil {
		return learningPlan{}, err
	}
	if scheduler.refreshProjection != nil && finalDrillExpiryRefreshRequired(snapshot.FinalDrillProjections, learningDayID) {
		if err = scheduler.refreshProjection(ctx); err != nil {
			return learningPlan{}, err
		}
		if snapshot, err = scheduler.index.snapshot(); err != nil {
			return learningPlan{}, err
		}
		if blockedElementIDs, err = scheduler.blockedElementIDs(snapshot); err != nil {
			return learningPlan{}, err
		}
	}
	resolvedTree, err := overlayBlockReferences(ctx, scheduler.config.BlockReader, snapshot.Tree)
	if err != nil {
		return learningPlan{}, err
	}
	unavailableMaterial := unavailableLearningMaterialIDs(resolvedTree)
	plan := learningPlan{LearningDayID: learningDayID}
	for id, projection := range snapshot.Projections {
		element, ok := snapshot.Elements[id]
		if !ok || unavailableMaterial[id] || projection.LifecycleState != "memorized" || projection.LastLearningDate == learningDayID {
			continue
		}
		dueDay := projection.DueLearningDay
		if dueDay == "" {
			dueDay = effective.ResolveLearningDayID(projection.DueAt)
		}
		if dueDay > learningDayID {
			continue
		}
		target, ok := scheduler.reviewTargetForElement(element, projection, learningDayID, dueDay)
		if ok {
			plan.Outstanding = append(plan.Outstanding, target)
		}
	}
	sort.Slice(plan.Outstanding, func(i, j int) bool {
		if !plan.Outstanding[i].DueAt.Equal(plan.Outstanding[j].DueAt) {
			return plan.Outstanding[i].DueAt.Before(plan.Outstanding[j].DueAt)
		}
		if plan.Outstanding[i].PriorityPosition != plan.Outstanding[j].PriorityPosition {
			return plan.Outstanding[i].PriorityPosition < plan.Outstanding[j].PriorityPosition
		}
		return plan.Outstanding[i].ElementID < plan.Outstanding[j].ElementID
	})
	orderedIDs := treeElementOrder(snapshot.Tree)
	if len(orderedIDs) == 0 {
		for id := range snapshot.Elements {
			orderedIDs = append(orderedIDs, id)
		}
		sort.Strings(orderedIDs)
	}
	for _, id := range orderedIDs {
		if blockedElementIDs[id] || unavailableMaterial[id] {
			continue
		}
		element, ok := snapshot.Elements[id]
		if !ok {
			continue
		}
		projection, hasProjection := snapshot.Projections[id]
		if hasProjection && projection.LifecycleState != "pending" {
			finalDrill := snapshot.FinalDrillProjections[id]
			if finalDrill.Member && projection.LifecycleState == "memorized" {
				if target, ok := scheduler.drillTargetForElement(element, projection, finalDrill, learningDayID); ok {
					plan.FinalDrill = append(plan.FinalDrill, target)
				}
			}
			continue
		}
		if hasProjection && projection.LastLearningDate == learningDayID {
			continue
		}
		if target, ok := scheduler.pendingTargetForElement(element, projection, learningDayID); ok {
			plan.Pending = append(plan.Pending, target)
		}
	}
	sort.Slice(plan.FinalDrill, func(i, j int) bool {
		if plan.FinalDrill[i].PriorityPosition != plan.FinalDrill[j].PriorityPosition {
			return plan.FinalDrill[i].PriorityPosition < plan.FinalDrill[j].PriorityPosition
		}
		return plan.FinalDrill[i].ElementID < plan.FinalDrill[j].ElementID
	})
	return plan, nil
}

func (scheduler *Scheduler) blockedElementIDs(snapshot projectionSnapshot) (map[string]bool, error) {
	blocked := map[string]bool{}
	for id := range snapshot.BlockedElementIDs {
		blocked[id] = true
	}
	diagnostics, err := scheduler.index.sourceDiagnostics()
	if err != nil {
		return nil, err
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == missingTopicInitializationCode && diagnostic.ElementID != "" {
			blocked[diagnostic.ElementID] = true
		}
	}
	return blocked, nil
}

func finalDrillExpiryRefreshRequired(projections map[string]FinalDrillProjection, learningDayID string) bool {
	for _, projection := range projections {
		if projection.Member && completeLearningDayGap(projection.LastActivityDay, learningDayID) > 3 {
			return true
		}
	}
	return false
}

func isSchedulableItem(element Element, projection SchedulingProjection) bool {
	if element.Type != "item" || element.Payload.Kind != "qa" || element.Payload.Prompt == "" || element.Payload.Answer == "" {
		return false
	}
	if projection.ScheduleProfile != "" && projection.ScheduleProfile != fsrsV1ID {
		return false
	}
	return projection.AcceptedReviewAction == "" || projection.AcceptedReviewAction == "GradeItem"
}

func isSchedulableTopic(element Element, projection SchedulingProjection) bool {
	if element.Type != "topic" || element.Payload.Material == nil || materialDiagnostic(element) != nil {
		return false
	}
	if projection.LifecycleState != "memorized" {
		return true
	}
	if projection.ScheduleProfile != topicAFactorV1ID {
		return false
	}
	state, hasState := projection.AlgorithmStates[topicAFactorV1ID]
	return projection.AcceptedReviewAction == "NextTopic" && projection.ActiveAlgorithm == topicAFactorV1ID && hasState && state.Algorithm == topicAFactorV1ID && state.SchemaVersion == 1
}

type scheduleApplyResult struct {
	Projection           SchedulingProjection
	FinalDrillProjection FinalDrillProjection
	Event                SchedulingEvent
	Decision             AlgorithmDecision
	Candidates           []AlgorithmCandidate
	AlreadyAccepted      bool
}

func (scheduler *Scheduler) ApplyGrade(ctx context.Context, target ReviewTarget, review NormalizedReview) (scheduleApplyResult, error) {
	if review.EventID == "" {
		return scheduleApplyResult{}, domainError(ErrHistoryRequiresRepair, "grade requires event identity", nil)
	}
	if review.ElementID != target.ElementID {
		return scheduleApplyResult{}, domainError(ErrTargetMismatch, "grade target does not match current target", nil)
	}
	before := target.ObservedProjection
	if before.ElementID == "" {
		before.ElementID = target.ElementID
	}
	state := before.AlgorithmStates[fsrsV1ID]
	input := AlgorithmInput{ElementID: target.ElementID, TargetKind: target.Kind, Review: review, Before: before, CurrentState: state}
	candidates, winner, decision, err := scheduler.arena.review(input)
	if err != nil {
		return scheduleApplyResult{Candidates: candidates, Decision: decision}, err
	}
	after, err := projectionFromCandidate(before, winner, review)
	if err != nil {
		return scheduleApplyResult{Candidates: candidates, Decision: decision}, err
	}
	after.AcceptedReviewAction = "GradeItem"
	after.ScheduleProfile = fsrsV1ID
	after.DueLearningDay = addLearningDays(review.LearningDayID, winner.NextIntervalDays)
	for _, candidate := range candidates {
		if candidate.Status == "valid" {
			after.AlgorithmStates[candidate.Algorithm] = candidate.NextState
		}
	}
	event := SchedulingEvent{
		Spec:                SupportedEventSpec,
		EventID:             review.EventID,
		OccurredAt:          review.ReviewAt,
		Type:                "reviewElement",
		ElementID:           review.ElementID,
		SessionID:           review.SessionID,
		BaseEventID:         review.ObservedBaseSchedulingEvent,
		ReviewKind:          "gradeItem",
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
	if target.ObservedProjection.LifecycleState == "pending" {
		event.Type = "introduceElement"
		event.BaseEventID = ""
		event.ReviewKind = "introduceItem"
		event.Before.LifecycleState = "pending"
	}
	if review.RawGrade <= 3 {
		event.DrillEffect = "admit"
	}
	projection, alreadyAccepted, err := scheduler.ledger.Commit(event)
	if err != nil {
		if domainErr, ok := AsDomainError(err); ok {
			domainErr.AcceptedEventID = event.EventID
		}
		return scheduleApplyResult{Event: event, Decision: decision, Candidates: candidates}, err
	}
	if !alreadyAccepted {
		if err = scheduler.refreshProjection(ctx); err != nil {
			domainErr := wrapDomainError(ErrProjectionRefreshFailed, "refresh scheduling projection: %v", err)
			domainErr.Retryable = true
			domainErr.ReviewAccepted = true
			domainErr.AcceptedEventID = event.EventID
			return scheduleApplyResult{Event: event, Decision: decision, Candidates: candidates}, domainErr
		}
		projection, err = scheduler.ledger.Snapshot(event.ElementID)
		if err != nil {
			domainErr := wrapDomainError(ErrProjectionRefreshFailed, "read refreshed scheduling projection: %v", err)
			domainErr.Retryable = true
			domainErr.ReviewAccepted = true
			domainErr.AcceptedEventID = event.EventID
			return scheduleApplyResult{Event: event, Decision: decision, Candidates: candidates}, domainErr
		}
	}
	if alreadyAccepted {
		event, _, _ = scheduler.ledger.EventByID(review.EventID)
		return scheduleApplyResult{Projection: projection, Event: event, Decision: event.AlgorithmDecision, Candidates: event.AlgorithmCandidates, AlreadyAccepted: true}, nil
	}
	return scheduleApplyResult{Projection: projection, Event: event, Decision: decision, Candidates: candidates}, nil
}

func (scheduler *Scheduler) ApplyTopicNext(ctx context.Context, target ReviewTarget, eventID, sessionID string) (scheduleApplyResult, error) {
	if eventID == "" {
		return scheduleApplyResult{}, domainError(ErrHistoryRequiresRepair, "Topic Next requires event identity", nil)
	}
	if target.Kind != "element.topic" {
		return scheduleApplyResult{}, domainError(ErrInvalidSessionPhase, "Topic Next requires a Topic target", nil)
	}
	if err := scheduler.ensureTopicMaterialAvailable(ctx, target.ElementID); err != nil {
		return scheduleApplyResult{}, err
	}
	effective := scheduler.config.LoadEffectiveSchedulerConfig()
	now := scheduler.config.Now()
	learningDayID := effective.ResolveLearningDayID(now)
	before := target.ObservedProjection
	if before.ElementID == "" {
		before = SchedulingProjection{ElementID: target.ElementID, LifecycleState: "pending", PriorityPosition: target.PriorityPosition}
	}
	review := NormalizedReview{ElementID: target.ElementID, TargetKind: target.Kind, ActionKind: string(ActionNextTopic), ReviewAt: now, LearningDate: learningDayID, LearningDayID: learningDayID, SessionID: sessionID, EventID: eventID, ObservedBaseSchedulingEvent: target.ObservedBaseSchedulingEvent}
	previousInterval := before.IntervalDays
	var interval int
	var seed string
	var topicCandidate *AlgorithmCandidate
	if before.LifecycleState == "pending" || before.AdoptedTerminalID == "" {
		seed = topicInitialSeed(eventID, target.ElementID)
		interval = topicInitialInterval(seed)
	} else {
		state := before.AlgorithmStates[topicAFactorV1ID]
		input := AlgorithmInput{ElementID: target.ElementID, TargetKind: target.Kind, Review: review, Before: before, CurrentState: state}
		candidate := evaluateAlgorithmAdapter(scheduler.topic, input)
		if candidate.Status != "valid" {
			return scheduleApplyResult{Candidates: []AlgorithmCandidate{candidate}}, domainError(ErrInvalidAlgorithmOutput, "topic scheduling candidate is invalid", nil)
		}
		interval = candidate.NextIntervalDays
		topicCandidate = &candidate
	}
	if interval < 1 {
		interval = 1
	}
	nextDay := addLearningDays(learningDayID, interval)
	dueAt, err := effective.TimeForLearningDayID(nextDay)
	if err != nil {
		return scheduleApplyResult{}, err
	}
	after, err := cloneSchedulingProjection(before)
	if err != nil {
		return scheduleApplyResult{}, err
	}
	after.ElementID = target.ElementID
	after.ScheduleProfile = topicAFactorV1ID
	after.AcceptedReviewAction = "NextTopic"
	after.LifecycleState = "memorized"
	after.AdoptedTerminalID = eventID
	after.DueAt = dueAt
	after.DueLearningDay = nextDay
	after.IntervalDays = interval
	after.Repetitions++
	after.LastReviewAt = timePointer(now)
	after.LastLearningDate = learningDayID
	after.ActiveAlgorithm = topicAFactorV1ID
	if after.AlgorithmStates == nil {
		after.AlgorithmStates = map[string]VersionedAlgorithmState{}
	}
	nextState := VersionedAlgorithmState{Algorithm: topicAFactorV1ID, SchemaVersion: 1, State: TopicAFactorV1State{IntervalDays: interval, Repetitions: after.Repetitions, LastLearningDay: learningDayID, DueLearningDay: nextDay, EffectiveAFactor: scheduler.topic.config.AFactor}}
	if topicCandidate != nil {
		topicCandidate.NextDueAt = dueAt
		nextState = topicCandidate.NextState
	}
	after.AlgorithmStates[topicAFactorV1ID] = nextState
	eventType := "reviewElement"
	reviewKind := "nextTopic"
	if before.LifecycleState == "pending" || before.AdoptedTerminalID == "" {
		eventType = "introduceElement"
		reviewKind = "introduceTopic"
	}
	event := SchedulingEvent{
		Spec:                      SupportedEventSpec,
		EventID:                   eventID,
		OccurredAt:                now,
		Type:                      eventType,
		ElementID:                 target.ElementID,
		SessionID:                 sessionID,
		BaseEventID:               review.ObservedBaseSchedulingEvent,
		ReviewKind:                reviewKind,
		LearningDate:              learningDayID,
		LearningDayID:             learningDayID,
		TopicPolicyVersion:        "siyuanmemo-topic-afactor-v1",
		TopicInitialIntervalDays:  0,
		TopicPreviousIntervalDays: previousInterval,
		TopicEffectiveAFactor:     scheduler.topic.config.AFactor,
		TopicNextIntervalDays:     interval,
		TopicMinimumIntervalDays:  scheduler.topic.config.MinimumIntervalDays,
		TopicMaximumIntervalDays:  scheduler.topic.config.MaximumIntervalDays,
		TopicSkipPolicy:           scheduler.topic.config.SkipPolicy,
		TopicSeed:                 seed,
		Before:                    before,
		After:                     after,
	}
	if eventType == "introduceElement" {
		event.BaseEventID = ""
		event.TopicPolicyVersion = "siyuanmemo-topic-initial-v1"
		event.TopicInitialIntervalDays = interval
	}
	if topicCandidate != nil {
		event.AlgorithmCandidates = []AlgorithmCandidate{*topicCandidate}
	}
	projection, alreadyAccepted, err := scheduler.ledger.Commit(event)
	if err != nil {
		if domainErr, ok := AsDomainError(err); ok {
			domainErr.AcceptedEventID = event.EventID
		}
		return scheduleApplyResult{Event: event}, err
	}
	if !alreadyAccepted {
		if err = scheduler.refreshProjection(ctx); err != nil {
			domainErr := wrapDomainError(ErrProjectionRefreshFailed, "refresh scheduling projection: %v", err)
			domainErr.Retryable = true
			domainErr.ReviewAccepted = true
			domainErr.AcceptedEventID = event.EventID
			return scheduleApplyResult{Event: event}, domainErr
		}
		projection, err = scheduler.ledger.Snapshot(event.ElementID)
		if err != nil {
			domainErr := wrapDomainError(ErrProjectionRefreshFailed, "read refreshed scheduling projection: %v", err)
			domainErr.Retryable = true
			domainErr.ReviewAccepted = true
			domainErr.AcceptedEventID = event.EventID
			return scheduleApplyResult{Event: event}, domainErr
		}
	}
	return scheduleApplyResult{Projection: projection, Event: event, AlreadyAccepted: alreadyAccepted}, nil
}

func (scheduler *Scheduler) planTopicIntroduction(elementID, eventID string) (SchedulingEvent, error) {
	if elementID == "" || eventID == "" {
		return SchedulingEvent{}, domainError(ErrInvalidCreateCommand, "Topic creation requires generated identities", nil)
	}
	effective := scheduler.config.LoadEffectiveSchedulerConfig()
	now := scheduler.config.Now()
	learningDayID := effective.ResolveLearningDayID(now)
	seed := topicInitialSeed(eventID, elementID)
	interval := topicInitialInterval(seed)
	nextDay := addLearningDays(learningDayID, interval)
	dueAt, err := effective.TimeForLearningDayID(nextDay)
	if err != nil {
		return SchedulingEvent{}, err
	}
	before := SchedulingProjection{ElementID: elementID, LifecycleState: "pending", PriorityPosition: 0}
	after := SchedulingProjection{
		ElementID:            elementID,
		ScheduleProfile:      topicAFactorV1ID,
		AcceptedReviewAction: "NextTopic",
		LifecycleState:       "memorized",
		AdoptedTerminalID:    eventID,
		DueAt:                dueAt,
		DueLearningDay:       nextDay,
		IntervalDays:         interval,
		Repetitions:          1,
		LastReviewAt:         timePointer(now),
		LastLearningDate:     learningDayID,
		ActiveAlgorithm:      topicAFactorV1ID,
		AlgorithmStates: map[string]VersionedAlgorithmState{
			topicAFactorV1ID: {
				Algorithm:     topicAFactorV1ID,
				SchemaVersion: 1,
				State: TopicAFactorV1State{
					IntervalDays:     interval,
					Repetitions:      1,
					LastLearningDay:  learningDayID,
					DueLearningDay:   nextDay,
					EffectiveAFactor: scheduler.topic.config.AFactor,
				},
			},
		},
		PriorityPosition: 0,
	}
	return SchedulingEvent{
		Spec:                      SupportedEventSpec,
		EventID:                   eventID,
		OccurredAt:                now,
		Type:                      "introduceElement",
		ElementID:                 elementID,
		ReviewKind:                "introduceTopic",
		LearningDate:              learningDayID,
		LearningDayID:             learningDayID,
		TopicPolicyVersion:        "siyuanmemo-topic-initial-v1",
		TopicInitialIntervalDays:  interval,
		TopicPreviousIntervalDays: 0,
		TopicEffectiveAFactor:     scheduler.topic.config.AFactor,
		TopicNextIntervalDays:     interval,
		TopicMinimumIntervalDays:  scheduler.topic.config.MinimumIntervalDays,
		TopicMaximumIntervalDays:  scheduler.topic.config.MaximumIntervalDays,
		TopicSkipPolicy:           scheduler.topic.config.SkipPolicy,
		TopicSeed:                 seed,
		Before:                    before,
		After:                     after,
	}, nil
}

func (scheduler *Scheduler) ApplyDrillGrade(ctx context.Context, target ReviewTarget, eventID, sessionID string, rawGrade int) (scheduleApplyResult, error) {
	if eventID == "" {
		return scheduleApplyResult{}, domainError(ErrHistoryRequiresRepair, "Drill grade requires event identity", nil)
	}
	review, err := scheduler.normalizeGrade(target.ElementID, sessionID, eventID, rawGrade, target)
	if err != nil {
		return scheduleApplyResult{}, err
	}
	before := target.ObservedProjection
	after, err := cloneSchedulingProjection(before)
	if err != nil {
		return scheduleApplyResult{}, err
	}
	effect := "retain"
	if rawGrade >= 4 {
		effect = "remove"
	}
	finalDrillBefore := target.ObservedFinalDrillProjection
	if !finalDrillBefore.Member || finalDrillBefore.AdmissionEventID == "" {
		return scheduleApplyResult{}, domainError(ErrHistoryRequiresRepair, "Final Drill membership is unavailable", nil)
	}
	event := SchedulingEvent{
		Spec:                  SupportedEventSpec,
		EventID:               eventID,
		OccurredAt:            review.ReviewAt,
		Type:                  "drillElement",
		ElementID:             review.ElementID,
		SessionID:             review.SessionID,
		ReviewKind:            "drillGrade",
		RawGrade:              intPointer(review.RawGrade),
		Passed:                boolPointer(review.Passed),
		RatingLabel:           review.RatingLabel,
		RatingMapping:         review.RatingMapping,
		LearningDate:          review.LearningDate,
		LearningDayID:         review.LearningDayID,
		DrillEffect:           effect,
		DrillAdmissionEventID: finalDrillBefore.AdmissionEventID,
		BaseDrillEventID:      finalDrillBefore.AdoptedTerminalEventID,
		Before:                before,
		After:                 after,
	}
	projection, alreadyAccepted, err := scheduler.ledger.Commit(event)
	if err != nil {
		if domainErr, ok := AsDomainError(err); ok {
			domainErr.AcceptedEventID = event.EventID
		}
		return scheduleApplyResult{Event: event}, err
	}
	if !alreadyAccepted {
		if err = scheduler.refreshProjection(ctx); err != nil {
			domainErr := wrapDomainError(ErrProjectionRefreshFailed, "refresh scheduling projection: %v", err)
			domainErr.Retryable = true
			domainErr.ReviewAccepted = true
			domainErr.AcceptedEventID = event.EventID
			return scheduleApplyResult{Event: event}, domainErr
		}
		projection, err = scheduler.ledger.Snapshot(event.ElementID)
		if err != nil {
			domainErr := wrapDomainError(ErrProjectionRefreshFailed, "read refreshed scheduling projection: %v", err)
			domainErr.Retryable = true
			domainErr.ReviewAccepted = true
			domainErr.AcceptedEventID = event.EventID
			return scheduleApplyResult{Event: event}, domainErr
		}
	}
	finalDrillProjection, err := scheduler.ledger.FinalDrillSnapshot(event.ElementID)
	if err != nil {
		domainErr := wrapDomainError(ErrProjectionRefreshFailed, "read refreshed Final Drill projection: %v", err)
		domainErr.Retryable = true
		domainErr.ReviewAccepted = true
		domainErr.AcceptedEventID = event.EventID
		return scheduleApplyResult{Event: event}, domainErr
	}
	return scheduleApplyResult{Projection: projection, FinalDrillProjection: finalDrillProjection, Event: event, AlreadyAccepted: alreadyAccepted}, nil
}

func projectionFromCandidate(before SchedulingProjection, candidate AlgorithmCandidate, review NormalizedReview) (SchedulingProjection, error) {
	after, err := cloneSchedulingProjection(before)
	if err != nil {
		return SchedulingProjection{}, err
	}
	if after.AlgorithmStates == nil {
		after.AlgorithmStates = make(map[string]VersionedAlgorithmState, len(before.AlgorithmStates)+1)
	}
	after.ElementID = review.ElementID
	after.LifecycleState = "memorized"
	after.AdoptedTerminalID = review.EventID
	after.DueAt = candidate.NextDueAt
	after.IntervalDays = candidate.NextIntervalDays
	after.ActiveAlgorithm = candidate.Algorithm
	after.LastRawGrade = intPointer(review.RawGrade)
	after.LastPassed = boolPointer(review.Passed)
	after.LastLearningDate = review.LearningDate
	after.LastReviewAt = timePointer(review.ReviewAt)
	after.AlgorithmStates[candidate.Algorithm] = candidate.NextState
	switch candidate.Algorithm {
	case fsrsV1ID:
		if state, err := decodeAlgorithmState[FSRSV1State](candidate.NextState, fsrsV1ID, 1); err == nil {
			after.Repetitions = int(state.Repetitions)
			after.Lapses = int(state.Lapses)
		}
	case simpleV1ID:
		if state, err := decodeAlgorithmState[SimpleV1State](candidate.NextState, simpleV1ID, 1); err == nil {
			after.Repetitions = state.Repetitions
			after.Lapses = state.Lapses
		}
	}
	return after, nil
}

func intPointer(value int) *int    { return &value }
func boolPointer(value bool) *bool { return &value }

func (scheduler *Scheduler) normalizeGrade(elementID, sessionID, eventID string, raw int, target ReviewTarget) (NormalizedReview, error) {
	review, err := NormalizeGrade(raw)
	if err != nil {
		return review, err
	}
	now := scheduler.config.Now().In(scheduler.config.Location)
	learningDayID := scheduler.config.LoadEffectiveSchedulerConfig().ResolveLearningDayID(now)
	review.ElementID = elementID
	review.TargetKind = target.Kind
	review.ActionKind = string(ActionGradeItem)
	review.SessionID = sessionID
	review.EventID = eventID
	review.ReviewAt = now
	review.LearningDate = learningDayID
	review.LearningDayID = learningDayID
	review.ObservedBaseSchedulingEvent = target.ObservedBaseSchedulingEvent
	return review, nil
}

func (scheduler *Scheduler) element(elementID string) (Element, error) {
	element, err := scheduler.index.element(elementID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return Element{}, err
		}
		return Element{}, domainError(ErrAuthoritativeElementUnavailable, "Element is unavailable", err)
	}
	return element, nil
}

func (scheduler *Scheduler) ensureTopicMaterialAvailable(ctx context.Context, elementID string) error {
	element, err := scheduler.element(elementID)
	if err != nil {
		return err
	}
	if !isSchedulableTopic(element, SchedulingProjection{}) {
		return domainError(ErrElementSourceUnavailable, "Topic material is unavailable", nil)
	}
	material := element.Payload.Material
	if material.Kind != "siyuanBlock" {
		return nil
	}
	resolution, err := scheduler.config.BlockReader.Load(ctx, material.BlockID)
	if err != nil {
		return wrapDomainError(ErrElementSourceUnavailable, "load Topic material: %v", err)
	}
	if resolution.Status != MaterialSourceAvailable || resolution.Encrypted {
		return domainError(ErrElementSourceUnavailable, "Topic material is unavailable", nil)
	}
	return nil
}

func unavailableLearningMaterialIDs(nodes []ElementTreeNode) map[string]bool {
	unavailable := make(map[string]bool)
	var walk func([]ElementTreeNode)
	walk = func(nodes []ElementTreeNode) {
		for _, node := range nodes {
			if node.SourceMode == SourceModeBlock && (node.MaterialSourceDiagnostic != nil || node.MaterialSourceStatus == nil || *node.MaterialSourceStatus != MaterialSourceAvailable) {
				unavailable[node.ElementID] = true
			}
			walk(node.Children)
		}
	}
	walk(nodes)
	return unavailable
}

func (scheduler *Scheduler) reviewTargetForElement(element Element, projection SchedulingProjection, learningDayID, dueDay string) (ReviewTarget, bool) {
	switch {
	case isSchedulableItem(element, projection):
		return ReviewTarget{Kind: "element.item", ElementID: element.ID, Prompt: element.Payload.Prompt, DueAt: projection.DueAt, DueLearningDay: dueDay, PriorityPosition: projection.PriorityPosition, ObservedBaseSchedulingEvent: projection.AdoptedTerminalID, ObservedProjection: projection, LearningDate: learningDayID, LearningDayID: learningDayID}, true
	case isSchedulableTopic(element, projection):
		return ReviewTarget{Kind: "element.topic", ElementID: element.ID, Prompt: topicPrompt(element), DueAt: projection.DueAt, DueLearningDay: dueDay, PriorityPosition: projection.PriorityPosition, ObservedBaseSchedulingEvent: projection.AdoptedTerminalID, ObservedProjection: projection, LearningDate: learningDayID, LearningDayID: learningDayID}, true
	default:
		return ReviewTarget{}, false
	}
}

func (scheduler *Scheduler) pendingTargetForElement(element Element, projection SchedulingProjection, learningDayID string) (ReviewTarget, bool) {
	if projection.ElementID == "" {
		projection = SchedulingProjection{ElementID: element.ID, LifecycleState: "pending"}
	}
	switch {
	case isSchedulableItem(element, SchedulingProjection{}):
		return ReviewTarget{Kind: "element.item", ElementID: element.ID, Prompt: element.Payload.Prompt, DueAt: projection.DueAt, PriorityPosition: projection.PriorityPosition, ObservedProjection: projection, LearningDate: learningDayID, LearningDayID: learningDayID}, true
	case isSchedulableTopic(element, SchedulingProjection{}):
		return ReviewTarget{Kind: "element.topic", ElementID: element.ID, Prompt: topicPrompt(element), DueAt: projection.DueAt, PriorityPosition: projection.PriorityPosition, ObservedProjection: projection, LearningDate: learningDayID, LearningDayID: learningDayID}, true
	default:
		return ReviewTarget{}, false
	}
}

func (scheduler *Scheduler) drillTargetForElement(element Element, projection SchedulingProjection, finalDrill FinalDrillProjection, learningDayID string) (ReviewTarget, bool) {
	if !isSchedulableItem(element, projection) {
		return ReviewTarget{}, false
	}
	return ReviewTarget{Kind: "element.item", ElementID: element.ID, Prompt: element.Payload.Prompt, DueAt: projection.DueAt, DueLearningDay: projection.DueLearningDay, PriorityPosition: projection.PriorityPosition, ObservedBaseSchedulingEvent: projection.AdoptedTerminalID, ObservedProjection: projection, ObservedFinalDrillProjection: finalDrill, LearningDate: learningDayID, LearningDayID: learningDayID}, true
}

func topicPrompt(element Element) string {
	if element.Title != "" {
		return element.Title
	}
	if element.Payload.Material != nil {
		return element.Payload.Material.HTML
	}
	return element.ID
}

func treeElementOrder(nodes []ElementTreeNode) []string {
	var ids []string
	var walk func([]ElementTreeNode)
	walk = func(nodes []ElementTreeNode) {
		for _, node := range nodes {
			ids = append(ids, node.ElementID)
			walk(node.Children)
		}
	}
	walk(nodes)
	return ids
}

func addLearningDays(day string, days int) string {
	date, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return date.AddDate(0, 0, days).Format("2006-01-02")
}

func topicInitialSeed(eventID, elementID string) string {
	sum := sha256.Sum256([]byte(eventID + "\x00" + elementID))
	return hex.EncodeToString(sum[:])
}

func topicInitialInterval(seed string) int {
	seedBytes, err := hex.DecodeString(seed)
	if err != nil || len(seedBytes) == 0 {
		return 1
	}
	candidates := seedBytes
	for counter := uint64(0); ; counter++ {
		for _, candidate := range candidates {
			if candidate < 255 {
				return int(candidate%15) + 1
			}
		}
		var encodedCounter [8]byte
		binary.BigEndian.PutUint64(encodedCounter[:], counter+1)
		payload := make([]byte, 0, len(seedBytes)+len(encodedCounter))
		payload = append(payload, seedBytes...)
		payload = append(payload, encodedCounter[:]...)
		digest := sha256.Sum256(payload)
		candidates = digest[:]
	}
}
