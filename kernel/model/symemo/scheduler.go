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
	"errors"
)

type Scheduler struct {
	config Config
	index  *projectionIndex
	ledger *SchedulingLedger
	arena  algorithmArena
}

func newScheduler(config Config, index *projectionIndex, ledger *SchedulingLedger, primary, fallback AlgorithmAdapter) *Scheduler {
	return &Scheduler{config: config, index: index, ledger: ledger, arena: algorithmArena{primary: primary, fallback: fallback}}
}

func (scheduler *Scheduler) BuildQueue() ([]ReviewTarget, error) {
	now := scheduler.config.Now().In(scheduler.config.Location)
	return scheduler.index.dueTargets(now, now.Format("2006-01-02"))
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

type scheduleApplyResult struct {
	Projection      SchedulingProjection
	Event           SchedulingEvent
	Decision        AlgorithmDecision
	Candidates      []AlgorithmCandidate
	AlreadyAccepted bool
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
	after := projectionFromCandidate(before, winner, review)
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
		ReviewKind:          "scheduled",
		RawGrade:            intPointer(review.RawGrade),
		Passed:              boolPointer(review.Passed),
		RatingLabel:         review.RatingLabel,
		RatingMapping:       review.RatingMapping,
		LearningDate:        review.LearningDate,
		AlgorithmDecision:   decision,
		AlgorithmCandidates: candidates,
		Before:              before,
		After:               after,
	}
	projection, alreadyAccepted, err := scheduler.ledger.Commit(ctx, event)
	if err != nil {
		if domainErr, ok := AsDomainError(err); ok {
			domainErr.AcceptedEventID = event.EventID
		}
		return scheduleApplyResult{Event: event, Decision: decision, Candidates: candidates}, err
	}
	if alreadyAccepted {
		event, _, _ = scheduler.ledger.EventByID(review.EventID)
		return scheduleApplyResult{Projection: projection, Event: event, Decision: event.AlgorithmDecision, Candidates: event.AlgorithmCandidates, AlreadyAccepted: true}, nil
	}
	return scheduleApplyResult{Projection: projection, Event: event, Decision: decision, Candidates: candidates}, nil
}

func projectionFromCandidate(before SchedulingProjection, candidate AlgorithmCandidate, review NormalizedReview) SchedulingProjection {
	after := before
	after.AlgorithmStates = make(map[string]VersionedAlgorithmState, len(before.AlgorithmStates)+1)
	for algorithm, state := range before.AlgorithmStates {
		after.AlgorithmStates[algorithm] = state
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
	return after
}

func intPointer(value int) *int    { return &value }
func boolPointer(value bool) *bool { return &value }

func (scheduler *Scheduler) normalizeGrade(elementID, sessionID, eventID string, raw int, target ReviewTarget) (NormalizedReview, error) {
	review, err := NormalizeGrade(raw)
	if err != nil {
		return review, err
	}
	now := scheduler.config.Now().In(scheduler.config.Location)
	review.ElementID = elementID
	review.SessionID = sessionID
	review.EventID = eventID
	review.ReviewAt = now
	review.LearningDate = now.Format("2006-01-02")
	review.ObservedBaseSchedulingEvent = target.ObservedBaseSchedulingEvent
	return review, nil
}

func (scheduler *Scheduler) element(elementID string) (Element, error) {
	element, err := scheduler.index.element(elementID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return Element{}, err
		}
		return Element{}, domainError(ErrAuthoritativeItemUnavailable, "item is unavailable", err)
	}
	return element, nil
}
