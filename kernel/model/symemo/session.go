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
	"encoding/json"
	"sync"
)

const finalDrillOrderVersion = "siyuanmemo-final-drill-flip-v1"

type finalDrillOrderState struct {
	Version string
	Seed    string
	Cursor  uint64
}

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
	drillOrder        finalDrillOrderState
}

func newLearningSession(config Config, scheduler *Scheduler, ledger *SchedulingLedger, refreshProjection func(context.Context) error) *learningSession {
	return &learningSession{config: config, scheduler: scheduler, ledger: ledger, refreshProjection: refreshProjection, state: SessionState{Status: SessionCompleted, Stage: StageCompleted, Phase: PhaseComplete}}
}

func (session *learningSession) Current() SessionState {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.publicState()
}

func (session *learningSession) Start(ctx context.Context) (SessionState, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state.Status == SessionActive {
		return session.publicState(), nil
	}
	plan, err := session.scheduler.BuildLearningPlan(ctx)
	if err != nil {
		return SessionState{}, err
	}
	previousState := *cloneSession(session.state)
	previousAnswer := session.currentAnswer
	previousTargets := append([]ReviewTarget(nil), session.remainingTargets...)
	previousErrorCode := session.pendingErrorCode
	previousDrillOrder := session.drillOrder
	session.state = SessionState{SessionID: newSessionID(session.config), Status: SessionActive}
	session.currentAnswer = ""
	session.remainingTargets = nil
	session.pendingErrorCode = ""
	session.drillOrder = finalDrillOrderState{}
	var state SessionState
	if len(plan.Outstanding) > 0 {
		state, err = session.enterTargetsLocked(StageOutstanding, plan.Outstanding)
	} else {
		state, err = session.offerNextStageLocked(ctx, StageOutstanding)
	}
	if err != nil {
		session.state = previousState
		session.currentAnswer = previousAnswer
		session.remainingTargets = previousTargets
		session.pendingErrorCode = previousErrorCode
		session.drillOrder = previousDrillOrder
		return SessionState{}, err
	}
	return state, nil
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
	if session.state.Current.Kind != "element.item" {
		return session.publicState(), domainErrorWithSession(ErrInvalidSessionPhase, "only Items reveal an answer", session.publicState())
	}
	session.state.Current.Answer = session.currentAnswer
	session.state.Phase = PhaseAnswer
	session.state.AnswerVisible = true
	return session.publicState(), nil
}

func (session *learningSession) AcceptStage(ctx context.Context, stage LearningStage) (SessionState, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state.Status != SessionActive || session.state.Phase != PhaseConfirmation || session.state.Confirmation == nil || session.state.Confirmation.Stage != stage {
		return session.publicState(), domainErrorWithSession(ErrInvalidSessionPhase, "stage confirmation is not available", session.publicState())
	}
	plan, err := session.scheduler.BuildLearningPlan(ctx)
	if err != nil {
		return SessionState{}, err
	}
	switch stage {
	case StagePending:
		if len(plan.Pending) == 0 {
			return session.offerNextStageLocked(ctx, StagePending)
		}
		return session.enterTargetsLocked(StagePending, plan.Pending)
	case StageFinalDrill:
		if len(plan.FinalDrill) == 0 {
			session.completeLocked()
			return session.publicState(), nil
		}
		return session.enterTargetsLocked(StageFinalDrill, plan.FinalDrill)
	default:
		return session.publicState(), domainErrorWithSession(ErrInvalidSessionPhase, "unsupported stage confirmation", session.publicState())
	}
}

func (session *learningSession) DeclineStage(ctx context.Context, stage LearningStage) (SessionState, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state.Status != SessionActive || session.state.Phase != PhaseConfirmation || session.state.Confirmation == nil || session.state.Confirmation.Stage != stage {
		return session.publicState(), domainErrorWithSession(ErrInvalidSessionPhase, "stage confirmation is not available", session.publicState())
	}
	switch stage {
	case StagePending:
		return session.offerNextStageLocked(ctx, StagePending)
	case StageFinalDrill:
		session.completeLocked()
		return session.publicState(), nil
	default:
		return session.publicState(), domainErrorWithSession(ErrInvalidSessionPhase, "unsupported stage confirmation", session.publicState())
	}
}

func (session *learningSession) Stop() SessionState {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.completeLocked()
	return session.publicState()
}

func (session *learningSession) NextTopic(ctx context.Context, elementID, eventID string) (LearningResult, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state.PendingAcceptedEventID != "" {
		return session.retryAcceptedLocked(ctx, elementID, eventID, func(event SchedulingEvent) bool {
			return eventMatchesTopicNext(event, session.state.SessionID, elementID)
		})
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
			if !eventMatchesTopicNext(existing, session.state.SessionID, elementID) {
				return LearningResult{}, domainErrorWithSession(ErrHistoryRequiresRepair, "event identity has conflicting review facts", session.publicState())
			}
			return session.resultForAcceptedEventLocked(existing)
		}
	}
	if session.state.Status != SessionActive || session.state.Phase != PhaseQuestion || session.state.Current == nil {
		return LearningResult{}, domainErrorWithSession(ErrInvalidSessionPhase, "Topic Next requires the current question target", session.publicState())
	}
	if session.state.Current.ElementID != elementID {
		return LearningResult{}, domainErrorWithSession(ErrTargetMismatch, "Topic Next target does not match current target", session.publicState())
	}
	if session.state.Current.Kind != "element.topic" || (session.state.Stage != StageOutstanding && session.state.Stage != StagePending) {
		return LearningResult{}, domainErrorWithSession(ErrInvalidSessionPhase, "Topic Next is not available for this target", session.publicState())
	}
	applyResult, err := session.scheduler.ApplyTopicNext(ctx, *session.state.Current, eventID, session.state.SessionID)
	if err != nil {
		return LearningResult{}, session.attachAcceptedFailure(eventID, err)
	}
	return session.finishApplied(ctx, applyResult, normalizedResultFacts(applyResult.Event))
}

func (session *learningSession) Grade(ctx context.Context, elementID, eventID string, rawGrade int) (LearningResult, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state.PendingAcceptedEventID != "" {
		return session.retryAcceptedLocked(ctx, elementID, eventID, func(event SchedulingEvent) bool {
			return eventMatchesGrade(event, session.state.SessionID, elementID, rawGrade)
		})
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
			return session.resultForAcceptedEventLocked(existing)
		}
	}
	if session.state.Status != SessionActive || session.state.Phase != PhaseAnswer {
		return LearningResult{}, domainErrorWithSession(ErrInvalidSessionPhase, "Item grade requires a visible answer", session.publicState())
	}
	if session.state.Current == nil || session.state.Current.ElementID != elementID {
		return LearningResult{}, domainErrorWithSession(ErrTargetMismatch, "grade target does not match current Item", session.publicState())
	}
	if session.state.Current.Kind != "element.item" || session.state.Stage == StageFinalDrill {
		return LearningResult{}, domainErrorWithSession(ErrInvalidSessionPhase, "Item grade is not available for this target", session.publicState())
	}
	review, err := session.scheduler.normalizeGrade(elementID, session.state.SessionID, eventID, rawGrade, *session.state.Current)
	if err != nil {
		return LearningResult{}, domainErrorWithSession(ErrUnsupportedGrade, err.Error(), session.publicState())
	}
	applyResult, err := session.scheduler.ApplyGrade(ctx, *session.state.Current, review)
	if err != nil {
		return LearningResult{}, session.attachAcceptedFailure(eventID, err)
	}
	return session.finishApplied(ctx, applyResult, review)
}

func (session *learningSession) GradeDrill(ctx context.Context, elementID, eventID string, rawGrade int) (LearningResult, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state.PendingAcceptedEventID != "" {
		return session.retryAcceptedLocked(ctx, elementID, eventID, func(event SchedulingEvent) bool {
			return eventMatchesDrillGrade(event, session.state.SessionID, elementID, rawGrade)
		})
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
			if !eventMatchesDrillGrade(existing, session.state.SessionID, elementID, rawGrade) {
				return LearningResult{}, domainErrorWithSession(ErrHistoryRequiresRepair, "event identity has conflicting review facts", session.publicState())
			}
			return session.resultForAcceptedEventLocked(existing)
		}
	}
	if session.state.Status != SessionActive || session.state.Stage != StageFinalDrill || session.state.Phase != PhaseAnswer {
		return LearningResult{}, domainErrorWithSession(ErrInvalidSessionPhase, "Drill grade requires a visible Drill answer", session.publicState())
	}
	if session.state.Current == nil || session.state.Current.ElementID != elementID {
		return LearningResult{}, domainErrorWithSession(ErrTargetMismatch, "Drill grade target does not match current Item", session.publicState())
	}
	applyResult, err := session.scheduler.ApplyDrillGrade(ctx, *session.state.Current, eventID, session.state.SessionID, rawGrade)
	if err != nil {
		return LearningResult{}, session.attachAcceptedFailure(eventID, err)
	}
	return session.finishApplied(ctx, applyResult, normalizedResultFacts(applyResult.Event))
}

func (session *learningSession) finishAccepted(ctx context.Context, eventID string) (LearningResult, error) {
	event, found, err := session.ledger.EventByID(eventID)
	if err != nil || !found {
		return LearningResult{}, domainErrorWithSession(ErrProjectionRefreshFailed, "accepted review event is unavailable after refresh", session.publicState())
	}
	return session.finishApplied(ctx, scheduleApplyResult{Event: event}, normalizedResultFacts(event))
}

func (session *learningSession) finishApplied(ctx context.Context, apply scheduleApplyResult, review NormalizedReview) (LearningResult, error) {
	projection := apply.Projection
	if projection.ElementID == "" {
		var err error
		projection, err = session.ledger.Snapshot(apply.Event.ElementID)
		if err != nil {
			return LearningResult{}, err
		}
	}
	finalDrillProjection := apply.FinalDrillProjection
	if finalDrillProjection.ElementID == "" {
		if current, finalDrillErr := session.ledger.FinalDrillSnapshot(apply.Event.ElementID); finalDrillErr == nil {
			finalDrillProjection = current
		} else if apply.Event.Type == "drillElement" || apply.Event.DrillEffect == "admit" {
			return LearningResult{}, finalDrillErr
		}
	}
	result := LearningResult{ReviewAccepted: true, EventID: apply.Event.EventID, RawGrade: apply.Event.RawGrade, Passed: apply.Event.Passed, RatingLabel: apply.Event.RatingLabel, RatingMapping: apply.Event.RatingMapping, Candidates: apply.Event.AlgorithmCandidates, Projection: &projection}
	if finalDrillProjection.ElementID != "" {
		result.FinalDrillProjection = &finalDrillProjection
	}
	if apply.Event.AlgorithmDecision.Winner != "" {
		result.Decision = &apply.Event.AlgorithmDecision
	}
	if review.RawGrade != 0 || apply.Event.RawGrade != nil {
		result.RawGrade = apply.Event.RawGrade
		result.Passed = apply.Event.Passed
		result.RatingLabel = apply.Event.RatingLabel
		result.RatingMapping = apply.Event.RatingMapping
	}
	session.state.LastProjection = &projection
	if finalDrillProjection.ElementID != "" {
		session.state.LastFinalDrillProjection = &finalDrillProjection
	}
	if err := session.advanceLocked(ctx); err != nil {
		session.setPendingAccepted(apply.Event.EventID, ErrQueueAdvanceFailed)
		return LearningResult{}, session.pendingAcceptedError("advance accepted review queue", err)
	}
	session.clearPendingAccepted()
	state := session.publicState()
	result.Session = &state
	return result, nil
}

func (session *learningSession) advanceLocked(ctx context.Context) error {
	remainingTargets := append([]ReviewTarget(nil), session.remainingTargets...)
	if session.state.Stage == StageFinalDrill && session.state.Current != nil && session.state.LastProjection != nil && session.state.LastFinalDrillProjection != nil && session.state.LastFinalDrillProjection.Member {
		target := *session.state.Current
		target.Answer = ""
		target.ObservedProjection = *session.state.LastProjection
		target.ObservedFinalDrillProjection = *session.state.LastFinalDrillProjection
		remainingTargets = append(remainingTargets, target)
	}
	if session.state.Stage == StageFinalDrill {
		plan, err := session.scheduler.BuildLearningPlan(ctx)
		if err != nil {
			return err
		}
		remainingTargets = reconcileFinalDrillTargets(remainingTargets, plan.FinalDrill)
	}
	if len(remainingTargets) == 0 {
		switch session.state.Stage {
		case StageOutstanding:
			_, err := session.offerNextStageLocked(ctx, StageOutstanding)
			return err
		case StagePending:
			_, err := session.offerNextStageLocked(ctx, StagePending)
			return err
		default:
			session.completeLocked()
			return nil
		}
	}
	drillOrder := session.drillOrder
	if session.state.Stage == StageFinalDrill {
		remainingTargets = flipFinalDrillTargets(remainingTargets, &drillOrder)
	}
	next := remainingTargets[0]
	element, err := session.scheduler.element(next.ElementID)
	if err != nil {
		return err
	}
	remainingTargets = remainingTargets[1:]
	next.Answer = ""
	session.remainingTargets = remainingTargets
	session.drillOrder = drillOrder
	session.currentAnswer = element.Payload.Answer
	session.state.Status = SessionActive
	session.state.Phase = PhaseQuestion
	session.state.AnswerVisible = false
	session.state.Confirmation = nil
	session.state.Current = &next
	session.state.RemainingElementIDs = remainingElementIDs(session.remainingTargets)
	return nil
}

func reconcileFinalDrillTargets(local, global []ReviewTarget) []ReviewTarget {
	globalByID := make(map[string]ReviewTarget, len(global))
	for _, target := range global {
		globalByID[target.ElementID] = target
	}
	reconciled := make([]ReviewTarget, 0, len(global))
	seen := make(map[string]bool, len(global))
	for _, target := range local {
		current, member := globalByID[target.ElementID]
		if !member || seen[target.ElementID] {
			continue
		}
		reconciled = append(reconciled, current)
		seen[target.ElementID] = true
	}
	for _, target := range global {
		if seen[target.ElementID] {
			continue
		}
		reconciled = append(reconciled, target)
		seen[target.ElementID] = true
	}
	return reconciled
}

func (session *learningSession) enterTargetsLocked(stage LearningStage, targets []ReviewTarget) (SessionState, error) {
	target := targets[0]
	element, err := session.scheduler.element(target.ElementID)
	if err != nil {
		return SessionState{}, err
	}
	target.Answer = ""
	session.currentAnswer = element.Payload.Answer
	session.remainingTargets = append(session.remainingTargets[:0], targets[1:]...)
	if stage == StageFinalDrill {
		session.drillOrder = newFinalDrillOrderState(session.state.SessionID, targets)
	}
	session.state.Status = SessionActive
	session.state.Stage = stage
	session.state.Phase = PhaseQuestion
	session.state.Current = &target
	session.state.Confirmation = nil
	session.state.AnswerVisible = false
	session.state.RemainingElementIDs = remainingElementIDs(session.remainingTargets)
	return session.publicState(), nil
}

func (session *learningSession) offerNextStageLocked(ctx context.Context, completed LearningStage) (SessionState, error) {
	plan, err := session.scheduler.BuildLearningPlan(ctx)
	if err != nil {
		return SessionState{}, err
	}
	switch completed {
	case StageOutstanding:
		if len(plan.Pending) > 0 {
			session.state.Stage = StagePending
			session.state.Phase = PhaseConfirmation
			session.state.Current = nil
			session.state.Confirmation = &StageConfirmation{Stage: StagePending}
			session.state.AnswerVisible = false
			session.state.RemainingElementIDs = nil
			return session.publicState(), nil
		}
		return session.offerNextStageLocked(ctx, StagePending)
	case StagePending:
		if len(plan.FinalDrill) > 0 {
			session.state.Stage = StageFinalDrill
			session.state.Phase = PhaseConfirmation
			session.state.Current = nil
			session.state.Confirmation = &StageConfirmation{Stage: StageFinalDrill}
			session.state.AnswerVisible = false
			session.state.RemainingElementIDs = nil
			return session.publicState(), nil
		}
		session.completeLocked()
		return session.publicState(), nil
	default:
		session.completeLocked()
		return session.publicState(), nil
	}
}

func (session *learningSession) completeLocked() {
	session.state = SessionState{SessionID: session.state.SessionID, Status: SessionCompleted, Stage: StageCompleted, Phase: PhaseComplete, LastProjection: session.state.LastProjection, LastFinalDrillProjection: session.state.LastFinalDrillProjection}
	session.currentAnswer = ""
	session.remainingTargets = nil
	session.pendingErrorCode = ""
	session.drillOrder = finalDrillOrderState{}
}

func (session *learningSession) retryAcceptedLocked(ctx context.Context, elementID, eventID string, matches func(SchedulingEvent) bool) (LearningResult, error) {
	if session.state.PendingAcceptedEventID != eventID {
		return LearningResult{}, session.pendingAcceptedError("a previously accepted review requires recovery", nil)
	}
	existing, found, lookupErr := session.ledger.EventByID(eventID)
	if lookupErr != nil || !found {
		return LearningResult{}, session.pendingAcceptedError("accepted review event is unavailable for recovery", lookupErr)
	}
	if existing.ElementID != elementID || !matches(existing) {
		return LearningResult{}, domainErrorWithSession(ErrHistoryRequiresRepair, "event identity has conflicting review facts", session.publicState())
	}
	if err := session.refreshProjection(ctx); err != nil {
		return LearningResult{}, session.pendingAcceptedError("refresh accepted review", err)
	}
	return session.finishAccepted(ctx, eventID)
}

func (session *learningSession) resultForAcceptedEventLocked(event SchedulingEvent) (LearningResult, error) {
	projection, projectionErr := session.ledger.Snapshot(event.ElementID)
	if projectionErr != nil {
		return LearningResult{}, projectionErr
	}
	result := LearningResult{ReviewAccepted: true, EventID: event.EventID, RawGrade: event.RawGrade, Passed: event.Passed, RatingLabel: event.RatingLabel, RatingMapping: event.RatingMapping, Candidates: event.AlgorithmCandidates, Projection: &projection}
	if finalDrillProjection, finalDrillErr := session.ledger.FinalDrillSnapshot(event.ElementID); finalDrillErr == nil {
		result.FinalDrillProjection = &finalDrillProjection
	}
	if event.AlgorithmDecision.Winner != "" {
		result.Decision = &event.AlgorithmDecision
	}
	state := session.publicState()
	result.Session = &state
	return result, nil
}

func (session *learningSession) attachAcceptedFailure(eventID string, err error) error {
	if domainErr, ok := AsDomainError(err); ok {
		domainErr.Session = cloneSession(session.publicState())
		if domainErr.ReviewAccepted {
			session.setPendingAccepted(eventID, domainErr.Code)
			domainErr.Session = cloneSession(session.publicState())
		}
	}
	return err
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

func newFinalDrillOrderState(sessionID string, targets []ReviewTarget) finalDrillOrderState {
	hash := sha256.New()
	_, _ = hash.Write([]byte(sessionID))
	for _, target := range targets {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(target.ElementID))
	}
	return finalDrillOrderState{Version: finalDrillOrderVersion, Seed: hex.EncodeToString(hash.Sum(nil))}
}

func flipFinalDrillTargets(targets []ReviewTarget, state *finalDrillOrderState) []ReviewTarget {
	count := len(targets)
	if count < 5 || state == nil {
		return targets
	}
	sourceRange := count - 4
	source := 4 + state.intN(sourceRange)
	maxDestinationPosition := 6
	if count < maxDestinationPosition {
		maxDestinationPosition = count
	}
	destination := 2 + state.intN(maxDestinationPosition-2)
	if source == destination {
		source++
		if source >= count {
			return targets
		}
	}
	if source == destination {
		return targets
	}
	target := targets[source]
	next := append([]ReviewTarget{}, targets[:source]...)
	next = append(next, targets[source+1:]...)
	if destination > len(next) {
		destination = len(next)
	}
	result := append([]ReviewTarget{}, next[:destination]...)
	result = append(result, target)
	result = append(result, next[destination:]...)
	return result
}

func (state *finalDrillOrderState) intN(limit int) int {
	if limit <= 1 {
		return 0
	}
	bound := uint64(limit)
	threshold := -bound % bound
	for {
		value := state.nextUint64()
		if value >= threshold {
			return int(value % bound)
		}
	}
}

func (state *finalDrillOrderState) nextUint64() uint64 {
	var encoded [8]byte
	for i := range encoded {
		encoded[i] = state.nextByte()
	}
	return binary.BigEndian.Uint64(encoded[:])
}

func (state *finalDrillOrderState) nextByte() byte {
	block := state.Cursor / sha256.Size
	offset := state.Cursor % sha256.Size
	state.Cursor++
	var encodedBlock [8]byte
	binary.BigEndian.PutUint64(encodedBlock[:], block)
	payload := make([]byte, 0, len(state.Version)+len(state.Seed)+10)
	payload = append(payload, state.Version...)
	payload = append(payload, 0)
	payload = append(payload, state.Seed...)
	payload = append(payload, 0)
	payload = append(payload, encodedBlock[:]...)
	digest := sha256.Sum256(payload)
	return digest[offset]
}

func (session *learningSession) publicState() SessionState {
	state := cloneSessionState(session.state)
	if state.Current != nil {
		target := *state.Current
		if state.Phase == PhaseQuestion {
			target.Answer = ""
		}
		state.Current = &target
	}
	return state
}

func cloneSession(state SessionState) *SessionState {
	cloned := cloneSessionState(state)
	return &cloned
}

func cloneSessionState(state SessionState) SessionState {
	data, err := json.Marshal(state)
	if err != nil {
		panic(err)
	}
	var cloned SessionState
	if err = json.Unmarshal(data, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

func eventMatchesGrade(event SchedulingEvent, sessionID, elementID string, rawGrade int) bool {
	if event.ElementID != elementID || event.SessionID != sessionID || event.RawGrade == nil || *event.RawGrade != rawGrade {
		return false
	}
	return (event.Type == "reviewElement" && (event.ReviewKind == "gradeItem" || event.ReviewKind == "scheduled")) ||
		(event.Type == "introduceElement" && event.ReviewKind == "introduceItem")
}

func eventMatchesTopicNext(event SchedulingEvent, sessionID, elementID string) bool {
	if event.ElementID != elementID || event.SessionID != sessionID || event.RawGrade != nil {
		return false
	}
	return (event.Type == "reviewElement" && event.ReviewKind == "nextTopic") ||
		(event.Type == "introduceElement" && event.ReviewKind == "introduceTopic")
}

func eventMatchesDrillGrade(event SchedulingEvent, sessionID, elementID string, rawGrade int) bool {
	return event.ElementID == elementID && event.SessionID == sessionID && event.Type == "drillElement" && event.ReviewKind == "drillGrade" && event.RawGrade != nil && *event.RawGrade == rawGrade
}

func normalizedResultFacts(event SchedulingEvent) NormalizedReview {
	review := NormalizedReview{ElementID: event.ElementID, SessionID: event.SessionID, EventID: event.EventID, ReviewAt: event.OccurredAt, LearningDate: event.LearningDate, LearningDayID: event.LearningDayID}
	if event.RawGrade != nil {
		review.RawGrade = *event.RawGrade
	}
	if event.Passed != nil {
		review.Passed = *event.Passed
	}
	review.RatingLabel = event.RatingLabel
	review.RatingMapping = event.RatingMapping
	return review
}

func domainErrorWithSession(code ErrorCode, message string, state SessionState) *DomainError {
	err := domainError(code, message, nil)
	err.Session = cloneSession(state)
	return err
}
