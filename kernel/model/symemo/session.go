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
	"sync"
)

type learningSession struct {
	config            Config
	scheduler         *Scheduler
	ledger            *SchedulingLedger
	refreshProjection func(context.Context) error
	mu                sync.Mutex
	state             SessionState
	currentAnswer     string
	remainingTargets  []ReviewTarget
	pendingErrorCode  ErrorCode
}

func newLearningSession(config Config, scheduler *Scheduler, ledger *SchedulingLedger, refreshProjection func(context.Context) error) *learningSession {
	return &learningSession{config: config, scheduler: scheduler, ledger: ledger, refreshProjection: refreshProjection, state: SessionState{Status: SessionCompleted, Phase: PhaseComplete}}
}

func (session *learningSession) Current() SessionState {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.publicState()
}

func (session *learningSession) Start() (SessionState, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state.Status == SessionActive {
		return session.publicState(), nil
	}
	targets, err := session.scheduler.BuildQueue()
	if err != nil {
		return SessionState{}, err
	}
	if len(targets) == 0 {
		session.state = SessionState{Status: SessionCompleted, Phase: PhaseComplete}
		session.currentAnswer = ""
		session.remainingTargets = nil
		session.pendingErrorCode = ""
		return session.publicState(), nil
	}
	target := targets[0]
	element, err := session.scheduler.element(target.ElementID)
	if err != nil {
		return SessionState{}, err
	}
	target.Answer = ""
	session.currentAnswer = element.Payload.Answer
	session.remainingTargets = append(session.remainingTargets[:0], targets[1:]...)
	session.state = SessionState{
		SessionID:           newSessionID(session.config),
		Status:              SessionActive,
		Phase:               PhaseQuestion,
		Current:             &target,
		RemainingElementIDs: remainingElementIDs(session.remainingTargets),
	}
	session.pendingErrorCode = ""
	return session.publicState(), nil
}

func (session *learningSession) ShowAnswer(elementID string) (SessionState, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state.Status != SessionActive || session.state.Phase != PhaseQuestion {
		return session.publicState(), domainErrorWithSession(ErrInvalidSessionPhase, "answer can only be shown during the question phase", session.publicState())
	}
	if session.state.Current == nil || session.state.Current.ElementID != elementID {
		return session.publicState(), domainErrorWithSession(ErrTargetMismatch, "answer target does not match current Item", session.publicState())
	}
	session.state.Current.Answer = session.currentAnswer
	session.state.Phase = PhaseAnswer
	return session.publicState(), nil
}

func (session *learningSession) Grade(ctx context.Context, elementID, eventID string, rawGrade int) (LearningResult, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state.PendingAcceptedEventID != "" {
		if session.state.PendingAcceptedEventID != eventID {
			return LearningResult{}, session.pendingAcceptedError("a previously accepted review requires recovery", nil)
		}
		existing, found, lookupErr := session.ledger.EventByID(eventID)
		if lookupErr != nil || !found {
			return LearningResult{}, session.pendingAcceptedError("accepted review event is unavailable for recovery", lookupErr)
		}
		if !eventMatchesGrade(existing, session.state.SessionID, elementID, rawGrade) {
			return LearningResult{}, domainErrorWithSession(ErrHistoryRequiresRepair, "event identity has conflicting review facts", session.publicState())
		}
		if err := session.refreshProjection(ctx); err != nil {
			return LearningResult{}, session.pendingAcceptedError("refresh accepted review", err)
		}
		return session.finishAccepted(ctx, eventID)
	}
	if eventID != "" {
		existing, found, lookupErr := session.ledger.EventByID(eventID)
		if lookupErr != nil {
			if domainErr, ok := AsDomainError(lookupErr); ok {
				domainErr.Session = cloneSession(session.publicState())
			}
			return LearningResult{}, lookupErr
		}
		if found {
			if !eventMatchesGrade(existing, session.state.SessionID, elementID, rawGrade) {
				return LearningResult{}, domainErrorWithSession(ErrHistoryRequiresRepair, "event identity has conflicting review facts", session.publicState())
			}
			projection, projectionErr := session.ledger.Snapshot(elementID)
			if projectionErr != nil {
				return LearningResult{}, projectionErr
			}
			result := LearningResult{ReviewAccepted: true, EventID: eventID, RawGrade: existing.RawGrade, Passed: existing.Passed, RatingLabel: existing.RatingLabel, RatingMapping: existing.RatingMapping, Decision: &existing.AlgorithmDecision, Candidates: existing.AlgorithmCandidates, Projection: &projection}
			state := session.publicState()
			result.Session = &state
			return result, nil
		}
	}
	if session.state.Status != SessionActive || session.state.Phase != PhaseAnswer {
		return LearningResult{}, domainErrorWithSession(ErrInvalidSessionPhase, "Item grade requires a visible answer", session.publicState())
	}
	if session.state.Current == nil || session.state.Current.ElementID != elementID {
		return LearningResult{}, domainErrorWithSession(ErrTargetMismatch, "grade target does not match current Item", session.publicState())
	}
	review, err := session.scheduler.normalizeGrade(elementID, session.state.SessionID, eventID, rawGrade, *session.state.Current)
	if err != nil {
		return LearningResult{}, domainErrorWithSession(ErrUnsupportedGrade, err.Error(), session.publicState())
	}
	applyResult, err := session.scheduler.ApplyGrade(ctx, *session.state.Current, review)
	if err != nil {
		if domainErr, ok := AsDomainError(err); ok {
			domainErr.Session = cloneSession(session.publicState())
			if domainErr.ReviewAccepted {
				session.setPendingAccepted(eventID, ErrProjectionRefreshFailed)
				domainErr.Session = cloneSession(session.publicState())
			}
		}
		return LearningResult{}, err
	}
	return session.finishApplied(ctx, applyResult, review)
}

func (session *learningSession) finishAccepted(ctx context.Context, eventID string) (LearningResult, error) {
	event, found, err := session.ledger.EventByID(eventID)
	if err != nil || !found {
		return LearningResult{}, domainErrorWithSession(ErrProjectionRefreshFailed, "accepted review event is unavailable after refresh", session.publicState())
	}
	projection, err := session.ledger.Snapshot(event.ElementID)
	if err != nil {
		return LearningResult{}, err
	}
	result := LearningResult{ReviewAccepted: true, EventID: event.EventID, RawGrade: event.RawGrade, Passed: event.Passed, RatingLabel: event.RatingLabel, RatingMapping: event.RatingMapping, Decision: &event.AlgorithmDecision, Candidates: event.AlgorithmCandidates, Projection: &projection}
	session.state.LastProjection = &projection
	if err = session.advanceLocked(); err != nil {
		session.setPendingAccepted(eventID, ErrQueueAdvanceFailed)
		return LearningResult{}, session.pendingAcceptedError("advance accepted review queue", err)
	}
	session.clearPendingAccepted()
	state := session.publicState()
	result.Session = &state
	return result, nil
}

func (session *learningSession) finishApplied(ctx context.Context, apply scheduleApplyResult, review NormalizedReview) (LearningResult, error) {
	projection := apply.Projection
	result := LearningResult{ReviewAccepted: true, EventID: review.EventID, RawGrade: &review.RawGrade, Passed: &review.Passed, RatingLabel: review.RatingLabel, RatingMapping: review.RatingMapping, Decision: &apply.Decision, Candidates: apply.Candidates, Projection: &projection}
	session.state.LastProjection = &projection
	if err := session.advanceLocked(); err != nil {
		session.setPendingAccepted(review.EventID, ErrQueueAdvanceFailed)
		return LearningResult{}, session.pendingAcceptedError("advance accepted review queue", err)
	}
	session.clearPendingAccepted()
	state := session.publicState()
	result.Session = &state
	return result, nil
}

func (session *learningSession) advanceLocked() error {
	if len(session.remainingTargets) == 0 {
		session.state = SessionState{SessionID: session.state.SessionID, Status: SessionCompleted, Phase: PhaseComplete, LastProjection: session.state.LastProjection}
		session.currentAnswer = ""
		return nil
	}
	next := session.remainingTargets[0]
	element, err := session.scheduler.element(next.ElementID)
	if err != nil {
		return err
	}
	session.remainingTargets = session.remainingTargets[1:]
	next.Answer = ""
	session.currentAnswer = element.Payload.Answer
	session.state.Status = SessionActive
	session.state.Phase = PhaseQuestion
	session.state.Current = &next
	session.state.RemainingElementIDs = remainingElementIDs(session.remainingTargets)
	return nil
}

func (session *learningSession) setPendingAccepted(eventID string, code ErrorCode) {
	session.state.PendingAcceptedEventID = eventID
	session.pendingErrorCode = code
}

func (session *learningSession) clearPendingAccepted() {
	session.state.PendingAcceptedEventID = ""
	session.pendingErrorCode = ""
}

func (session *learningSession) pendingAcceptedError(message string, cause error) *DomainError {
	code := session.pendingErrorCode
	if code == "" {
		code = ErrProjectionRefreshFailed
	}
	err := domainErrorWithSession(code, message, session.publicState())
	err.Cause = cause
	err.Retryable = true
	err.ReviewAccepted = true
	err.AcceptedEventID = session.state.PendingAcceptedEventID
	return err
}

func remainingElementIDs(targets []ReviewTarget) []string {
	remaining := make([]string, 0, len(targets))
	for _, target := range targets {
		remaining = append(remaining, target.ElementID)
	}
	return remaining
}

func (session *learningSession) publicState() SessionState {
	state := session.state
	if state.Current != nil {
		target := *state.Current
		if state.Phase == PhaseQuestion {
			target.Answer = ""
		}
		state.Current = &target
	}
	return state
}

func cloneSession(state SessionState) *SessionState { return &state }

func eventMatchesGrade(event SchedulingEvent, sessionID, elementID string, rawGrade int) bool {
	return event.ElementID == elementID && event.SessionID == sessionID && event.RawGrade != nil && *event.RawGrade == rawGrade
}

func domainErrorWithSession(code ErrorCode, message string, state SessionState) *DomainError {
	err := domainError(code, message, nil)
	err.Session = cloneSession(state)
	return err
}
