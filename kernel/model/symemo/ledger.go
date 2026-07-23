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
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/siyuan-note/filelock"
)

type SchedulingLedger struct {
	config Config
	index  *projectionIndex
	mu     sync.Mutex
}

type preparedSchedulingEventWrite struct {
	path string
	data []byte
}

type schedulingEventSerializationError struct {
	cause error
}

func (err *schedulingEventSerializationError) Error() string {
	return "serialize scheduling event envelope: " + err.cause.Error()
}

func (err *schedulingEventSerializationError) Unwrap() error {
	return err.cause
}

func newSchedulingLedger(config Config, index *projectionIndex) *SchedulingLedger {
	return &SchedulingLedger{config: config, index: index}
}

func (ledger *SchedulingLedger) Snapshot(elementID string) (SchedulingProjection, error) {
	projection, err := ledger.index.projection(elementID)
	if err != nil {
		if errors.Is(err, errProjectionNotFound) {
			return SchedulingProjection{}, domainError(ErrAuthoritativeElementUnavailable, "scheduling projection is unavailable", err)
		}
		return SchedulingProjection{}, err
	}
	return projection, nil
}

func (ledger *SchedulingLedger) FinalDrillSnapshot(elementID string) (FinalDrillProjection, error) {
	projection, err := ledger.index.finalDrillProjection(elementID)
	if err != nil {
		if errors.Is(err, errProjectionNotFound) {
			return FinalDrillProjection{}, domainError(ErrAuthoritativeElementUnavailable, "Final Drill projection is unavailable", err)
		}
		return FinalDrillProjection{}, err
	}
	return projection, nil
}

func (ledger *SchedulingLedger) Commit(event SchedulingEvent) (SchedulingProjection, bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if event.EventID == "" || event.ElementID == "" || event.OccurredAt.IsZero() {
		return SchedulingProjection{}, false, domainError(ErrHistoryRequiresRepair, "review event identity is incomplete", nil)
	}
	existing, found, err := ledger.findEventLocked(event.EventID)
	if err != nil {
		return SchedulingProjection{}, false, err
	}
	if found {
		existingHash, _ := canonicalHash(existing)
		incomingHash, _ := canonicalHash(event)
		if existingHash != incomingHash {
			return SchedulingProjection{}, false, domainError(ErrHistoryRequiresRepair, "event identity has conflicting payloads", nil)
		}
		projection, projectionErr := ledger.index.projection(event.ElementID)
		return projection, true, projectionErr
	}
	if err = ledger.writeEventLocked(event); err != nil {
		domainErr := wrapDomainError(ErrDurableWriteFailed, "persist scheduling event: %v", err)
		domainErr.Retryable = true
		return SchedulingProjection{}, false, domainErr
	}
	return SchedulingProjection{}, false, nil
}

func (ledger *SchedulingLedger) commitPrepared(event SchedulingEvent, marshal func(any, string, string) ([]byte, error), beforeCommit func() error) (bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if event.EventID == "" || event.ElementID == "" || event.OccurredAt.IsZero() {
		return false, domainError(ErrHistoryRequiresRepair, "review event identity is incomplete", nil)
	}
	_, found, err := ledger.findEventLocked(event.EventID)
	if err != nil {
		return false, err
	}
	if found {
		return true, nil
	}
	prepared, err := ledger.prepareEventWriteLocked(event, marshal)
	if err != nil {
		return false, err
	}
	if err = beforeCommit(); err != nil {
		return false, err
	}
	if err = ledger.writePreparedEventLocked(prepared); err != nil {
		domainErr := wrapDomainError(ErrDurableWriteFailed, "persist scheduling event: %v", err)
		domainErr.Retryable = true
		return false, domainErr
	}
	return false, nil
}

type schedulingRefreshResult struct {
	Projections              map[string]SchedulingProjection
	FinalDrillProjections    map[string]FinalDrillProjection
	EventDiagnostics         []EventDiagnostic
	HistoryElementIDs        map[string]bool
	HistoryEventIDs          map[string]bool
	HistoryEventFingerprints map[string]bool
	HasEvents                bool
}

func (ledger *SchedulingLedger) Refresh(ctx context.Context) (schedulingRefreshResult, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return schedulingRefreshResult{}, err
	}
	events, err := ledger.config.LoadEventFiles()
	if err != nil {
		return schedulingRefreshResult{}, err
	}
	for _, event := range events {
		if _, fatal := validateSchedulingEvent(event); fatal {
			return schedulingRefreshResult{}, domainError(ErrHistoryRequiresRepair, "review history contains an unsupported event type", nil)
		}
	}
	currentLearningDay := ledger.config.LoadEffectiveSchedulerConfig().ResolveLearningDayID(ledger.config.Now())
	projections, finalDrillProjections, diagnostics := projectSchedulingTruth(events, currentLearningDay)
	historyElementIDs := make(map[string]bool)
	historyEventIDs := make(map[string]bool)
	historyEventFingerprints := make(map[string]bool)
	for _, event := range events {
		if event.ElementID != "" {
			historyElementIDs[event.ElementID] = true
		}
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.PayloadHash != "" {
			historyEventFingerprints[diagnostic.PayloadHash] = true
		}
		if diagnostic.EventID != "" && diagnostic.Classification != "invalid" {
			historyEventIDs[diagnostic.EventID] = true
		}
	}
	return schedulingRefreshResult{Projections: projections, FinalDrillProjections: finalDrillProjections, EventDiagnostics: diagnostics, HistoryElementIDs: historyElementIDs, HistoryEventIDs: historyEventIDs, HistoryEventFingerprints: historyEventFingerprints, HasEvents: len(events) > 0}, nil
}

func (ledger *SchedulingLedger) EventByID(eventID string) (SchedulingEvent, bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	return ledger.findEventLocked(eventID)
}

func (ledger *SchedulingLedger) findEventLocked(eventID string) (SchedulingEvent, bool, error) {
	events, err := ledger.config.LoadEventFiles()
	if err != nil {
		return SchedulingEvent{}, false, err
	}
	var found SchedulingEvent
	var foundHash string
	for _, event := range events {
		if event.EventID != eventID {
			continue
		}
		hash, hashErr := canonicalHash(event)
		if hashErr != nil {
			return SchedulingEvent{}, false, hashErr
		}
		if foundHash != "" && foundHash != hash {
			return SchedulingEvent{}, false, domainError(ErrHistoryRequiresRepair, "event identity has conflicting payloads", nil)
		}
		found = event
		foundHash = hash
	}
	return found, foundHash != "", nil
}

func (ledger *SchedulingLedger) writeEventLocked(event SchedulingEvent) error {
	prepared, err := ledger.prepareEventWriteLocked(event, json.MarshalIndent)
	if err != nil {
		return err
	}
	return ledger.writePreparedEventLocked(prepared)
}

func (ledger *SchedulingLedger) prepareEventWriteLocked(event SchedulingEvent, marshal func(any, string, string) ([]byte, error)) (preparedSchedulingEventWrite, error) {
	month := monthFor(event.OccurredAt.In(ledger.config.Location))
	path := filepath.Join(ledger.config.ReviewsRoot(), month+".smr")
	file := EventFile{Spec: SupportedEventSpec, Month: month}
	if data, err := filelock.ReadFile(path); err == nil {
		if err = json.Unmarshal(data, &file); err != nil {
			return preparedSchedulingEventWrite{}, err
		}
		if file.Spec != SupportedEventSpec || file.Month != month {
			return preparedSchedulingEventWrite{}, errors.New("event file envelope is incompatible")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return preparedSchedulingEventWrite{}, err
	}
	file.Events = append(file.Events, event)
	sort.SliceStable(file.Events, func(i, j int) bool {
		if !file.Events[i].OccurredAt.Equal(file.Events[j].OccurredAt) {
			return file.Events[i].OccurredAt.Before(file.Events[j].OccurredAt)
		}
		return file.Events[i].EventID < file.Events[j].EventID
	})
	data, err := marshal(file, "", "  ")
	if err != nil {
		return preparedSchedulingEventWrite{}, &schedulingEventSerializationError{cause: err}
	}
	data = append(data, '\n')
	return preparedSchedulingEventWrite{path: path, data: data}, nil
}

func (ledger *SchedulingLedger) writePreparedEventLocked(prepared preparedSchedulingEventWrite) error {
	if err := os.MkdirAll(filepath.Dir(prepared.path), 0755); err != nil {
		return err
	}
	return filelock.WriteFile(prepared.path, prepared.data)
}

type eventGroup struct {
	event  SchedulingEvent
	hashes map[string]int
}

func projectSchedulingEvents(events []SchedulingEvent, currentLearningDay ...string) (map[string]SchedulingProjection, []EventDiagnostic) {
	projections, _, diagnostics := projectSchedulingTruth(events, currentLearningDay...)
	return projections, diagnostics
}

func projectSchedulingTruth(events []SchedulingEvent, currentLearningDay ...string) (map[string]SchedulingProjection, map[string]FinalDrillProjection, []EventDiagnostic) {
	groups := map[string]*eventGroup{}
	for _, event := range events {
		hash, err := canonicalHash(event)
		if err != nil {
			continue
		}
		group := groups[event.EventID]
		if group == nil {
			group = &eventGroup{event: event, hashes: map[string]int{}}
			groups[event.EventID] = group
		}
		group.hashes[hash]++
	}
	effective := map[string]SchedulingEvent{}
	duplicate := map[string]bool{}
	invalidReason := map[string]string{}
	for id, group := range groups {
		if id == "" || len(group.hashes) != 1 {
			invalidReason[id] = "conflicting-event-identity"
			continue
		}
		for _, count := range group.hashes {
			duplicate[id] = count > 1
		}
		effective[id] = group.event
	}
	visitState := map[string]int{}
	valid := map[string]bool{}
	var validate func(string) bool
	validate = func(id string) bool {
		if reason := invalidReason[id]; reason != "" {
			return false
		}
		if visitState[id] == 2 {
			return valid[id]
		}
		if visitState[id] == 1 {
			invalidReason[id] = "cyclic-base"
			return false
		}
		event, ok := effective[id]
		if !ok || event.ElementID == "" {
			invalidReason[id] = "invalid-event"
			return false
		}
		if reason, _ := validateSchedulingEvent(event); reason != "" {
			invalidReason[id] = reason
			visitState[id] = 2
			valid[id] = false
			return false
		}
		visitState[id] = 1
		if isFormalSchedulingEvent(event) && event.BaseEventID != "" {
			parent, parentOK := effective[event.BaseEventID]
			if !parentOK {
				invalidReason[id] = "missing-base"
			} else if parent.ElementID != event.ElementID {
				invalidReason[id] = "cross-item-base"
			} else if !validate(event.BaseEventID) {
				if invalidReason[id] == "" {
					invalidReason[id] = "invalid-base"
				}
			} else if !projectionsCompatible(parent.After, event.Before) {
				invalidReason[id] = "incompatible-transition"
			}
		}
		if invalidReason[id] == "" {
			if reason := validateSchedulingEventCandidateTransitions(event); reason != "" {
				invalidReason[id] = reason
			}
		}
		visitState[id] = 2
		valid[id] = invalidReason[id] == ""
		return valid[id]
	}
	ids := make([]string, 0, len(effective))
	for id := range effective {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		validate(id)
	}
	children := map[string][]string{}
	for id, event := range effective {
		if valid[id] && isFormalSchedulingEvent(event) && event.BaseEventID != "" && valid[event.BaseEventID] {
			children[event.BaseEventID] = append(children[event.BaseEventID], id)
		}
	}
	winningTerminal := map[string]string{}
	for id, event := range effective {
		if !valid[id] || !isFormalSchedulingEvent(event) || len(children[id]) > 0 {
			continue
		}
		currentID := winningTerminal[event.ElementID]
		if currentID == "" || eventLater(event, effective[currentID]) {
			winningTerminal[event.ElementID] = id
		}
	}
	adopted := map[string]bool{}
	projections := map[string]SchedulingProjection{}
	for elementID, terminalID := range winningTerminal {
		for id := terminalID; id != ""; id = effective[id].BaseEventID {
			adopted[id] = true
		}
		projection := effective[terminalID].After
		projection.ElementID = elementID
		projection.AdoptedTerminalID = terminalID
		projections[elementID] = projection
	}
	finalDrillProjections, drillAdopted := applyFinalDrillProjection(effective, valid, adopted, duplicate, invalidReason, firstString(currentLearningDay))
	var diagnostics []EventDiagnostic
	for id, group := range groups {
		hashes := make([]string, 0, len(group.hashes))
		for hash := range group.hashes {
			hashes = append(hashes, hash)
		}
		sort.Strings(hashes)
		for _, hash := range hashes {
			classification := "invalid"
			reason := invalidReason[id]
			if len(group.hashes) == 1 && group.hashes[hash] > 1 {
				classification = "duplicate"
				reason = "canonical-duplicate"
			} else if adopted[id] || drillAdopted[id] {
				classification = "adopted"
			} else if valid[id] && (isFormalSchedulingEvent(group.event) || group.event.Type == "drillElement") {
				classification = "concurrent-superseded"
			} else if valid[id] {
				classification = "applied"
			}
			diagnostics = append(diagnostics, EventDiagnostic{EventID: id, PayloadHash: hash, Classification: classification, Reason: reason, DuplicateCount: group.hashes[hash]})
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].EventID != diagnostics[j].EventID {
			return diagnostics[i].EventID < diagnostics[j].EventID
		}
		return diagnostics[i].PayloadHash < diagnostics[j].PayloadHash
	})
	return projections, finalDrillProjections, diagnostics
}

func validateSchedulingEvent(event SchedulingEvent) (reason string, fatal bool) {
	switch event.Type {
	case "introduceElement":
		if event.BaseEventID != "" {
			return "introduction-has-base", false
		}
	case "reviewElement":
		if event.BaseEventID == "" {
			return "review-base-missing", false
		}
	case "drillElement":
		if event.BaseEventID != "" {
			return "drill-has-base", false
		}
	default:
		return "unsupported-event-type", true
	}
	if event.Spec != SupportedEventSpec {
		return "unsupported-event-spec", false
	}
	if event.EventID == "" || event.ElementID == "" {
		return "invalid-event", false
	}
	if event.OccurredAt.IsZero() {
		return "event-time-missing", false
	}
	if event.Before.ElementID != event.ElementID {
		return "before-projection-element-mismatch", false
	}
	if event.After.ElementID != event.ElementID {
		return "projection-element-mismatch", false
	}
	if isFormalSchedulingEvent(event) && event.After.AdoptedTerminalID != event.EventID {
		return "projection-terminal-mismatch", false
	}
	if event.After.LifecycleState == "" || (isFormalSchedulingEvent(event) && event.After.DueAt.IsZero()) {
		return "incomplete-after-projection", false
	}
	if event.After.IntervalDays < 0 || event.After.Repetitions < 0 || event.After.Lapses < 0 || event.After.Lapses > event.After.Repetitions {
		return "invalid-after-projection", false
	}
	if event.Type == "introduceElement" {
		if event.Before.LifecycleState == "memorized" || event.After.LifecycleState != "memorized" {
			return "invalid-introduction-transition", false
		}
		switch event.ReviewKind {
		case "":
			return validateLegacyItemIntroduction(event), false
		case "introduceElement":
			return "review-kind-invalid", false
		case "introduceItem":
			if !isCleanPendingIntroduction(event.Before) {
				return "invalid-introduction-transition", false
			}
			return validateReviewSchedulingEvent(event), false
		case "introduceTopic":
			if !isCleanPendingIntroduction(event.Before) {
				return "invalid-introduction-transition", false
			}
			return validateTopicSchedulingEvent(event), false
		default:
			return "review-kind-invalid", false
		}
	}
	if event.Type == "drillElement" {
		return validateDrillSchedulingEvent(event), false
	}
	switch event.ReviewKind {
	case "scheduled", "gradeItem":
		return validateReviewSchedulingEvent(event), false
	case "nextTopic":
		return validateTopicSchedulingEvent(event), false
	default:
		return "review-kind-invalid", false
	}
}

func validateLegacyItemIntroduction(event SchedulingEvent) string {
	before := event.Before
	if !before.DueAt.IsZero() && !before.DueAt.Equal(event.OccurredAt) {
		return "invalid-legacy-introduction"
	}
	before.DueAt = time.Time{}
	if !isCleanPendingIntroduction(before) {
		return "invalid-legacy-introduction"
	}
	after := event.After
	if event.SessionID != "" || event.LearningDate != "" || event.LearningDayID != "" || event.RawGrade != nil || event.Passed != nil || event.RatingLabel != "" || event.RatingMapping != "" || event.AlgorithmDecision.Policy != "" || len(event.AlgorithmDecision.EnabledAlgorithms) != 0 || event.AlgorithmDecision.Winner != "" || event.AlgorithmDecision.FallbackReason != "" || len(event.AlgorithmCandidates) != 0 || event.TopicPolicyVersion != "" || event.TopicInitialIntervalDays != 0 || event.TopicPreviousIntervalDays != 0 || event.TopicEffectiveAFactor != 0 || event.TopicNextIntervalDays != 0 || event.TopicMinimumIntervalDays != 0 || event.TopicMaximumIntervalDays != 0 || event.TopicSkipPolicy != "" || event.TopicSeed != "" || event.DrillEffect != "" || event.DrillAdmissionEventID != "" || event.BaseDrillEventID != "" {
		return "invalid-legacy-introduction"
	}
	if (after.ScheduleProfile == "") != (after.AcceptedReviewAction == "") || after.DueLearningDay != "" || after.Repetitions != 1 || after.Lapses != 0 || after.LastReviewAt == nil || !after.LastReviewAt.Equal(event.OccurredAt) || after.LastLearningDate != event.OccurredAt.Format("2006-01-02") || after.LastRawGrade != nil || after.LastPassed != nil || after.ActiveAlgorithm != fsrsV1ID || len(after.AlgorithmStates) != 2 {
		return "invalid-legacy-introduction"
	}
	fsrsState, fsrsErr := decodeAlgorithmState[FSRSV1State](after.AlgorithmStates[fsrsV1ID], fsrsV1ID, 1)
	simpleState, simpleErr := decodeAlgorithmState[SimpleV1State](after.AlgorithmStates[simpleV1ID], simpleV1ID, 1)
	if fsrsErr != nil || simpleErr != nil || !fsrsState.DueAt.Equal(after.DueAt) || fsrsState.ScheduledDays != uint64(after.IntervalDays) || fsrsState.Repetitions != uint64(after.Repetitions) || fsrsState.Lapses != uint64(after.Lapses) || !fsrsState.LastReviewAt.Equal(*after.LastReviewAt) || simpleState.DueAt == nil || !simpleState.DueAt.Equal(after.DueAt) || simpleState.IntervalDays != after.IntervalDays || simpleState.Repetitions != after.Repetitions || simpleState.Lapses != after.Lapses || simpleState.LastReviewAt == nil || !simpleState.LastReviewAt.Equal(*after.LastReviewAt) {
		return "invalid-legacy-introduction"
	}
	return ""
}

func isCleanPendingIntroduction(before SchedulingProjection) bool {
	return before.LifecycleState == "pending" && before.ScheduleProfile == "" && before.AcceptedReviewAction == "" && before.AdoptedTerminalID == "" && before.DueAt.IsZero() && before.DueLearningDay == "" && before.IntervalDays == 0 && before.Repetitions == 0 && before.Lapses == 0 && before.LastReviewAt == nil && before.LastLearningDate == "" && before.LastRawGrade == nil && before.LastPassed == nil && before.ActiveAlgorithm == "" && len(before.AlgorithmStates) == 0
}

func validateReviewSchedulingEvent(event SchedulingEvent) string {
	if event.SessionID == "" {
		return "review-session-missing"
	}
	if event.RawGrade == nil {
		return "review-grade-missing"
	}
	normalized, err := NormalizeGrade(*event.RawGrade)
	if err != nil {
		return "review-grade-invalid"
	}
	if event.Passed == nil || *event.Passed != normalized.Passed {
		return "review-pass-mismatch"
	}
	if event.RatingLabel != normalized.RatingLabel || event.RatingMapping != normalized.RatingMapping {
		return "review-rating-invalid"
	}
	legacyScheduled := event.ReviewKind == "scheduled"
	if legacyScheduled {
		if event.LearningDayID != "" || hasFeature003ReviewProjectionFields(event.Before) || hasFeature003ReviewProjectionFields(event.After) || event.TopicPolicyVersion != "" || event.TopicInitialIntervalDays != 0 || event.TopicPreviousIntervalDays != 0 || event.TopicEffectiveAFactor != 0 || event.TopicNextIntervalDays != 0 || event.TopicMinimumIntervalDays != 0 || event.TopicMaximumIntervalDays != 0 || event.TopicSkipPolicy != "" || event.TopicSeed != "" || event.DrillEffect != "" {
			return "invalid-legacy-scheduled-review"
		}
	} else if event.After.ScheduleProfile != fsrsV1ID || event.After.AcceptedReviewAction != "GradeItem" {
		return "review-schedule-profile-invalid"
	}
	if event.ReviewKind != "scheduled" && event.ReviewKind != "gradeItem" && event.ReviewKind != "introduceItem" {
		return "review-kind-invalid"
	}
	if reason := validateEventLearningDayFields(event, "review-learning-date-invalid"); reason != "" {
		return reason
	}
	if event.ReviewKind == "introduceItem" {
		if event.Before.LifecycleState == "memorized" || event.After.LifecycleState != "memorized" {
			return "invalid-review-lifecycle-transition"
		}
	} else if event.Before.LifecycleState != "memorized" || event.After.LifecycleState != "memorized" {
		return "invalid-review-lifecycle-transition"
	}
	if event.After.LastRawGrade == nil || *event.After.LastRawGrade != *event.RawGrade {
		return "review-after-grade-mismatch"
	}
	if event.After.LastPassed == nil || *event.After.LastPassed != *event.Passed {
		return "review-after-pass-mismatch"
	}
	if event.After.LastReviewAt == nil || !event.After.LastReviewAt.Equal(event.OccurredAt) || event.After.LastLearningDate != eventLearningDayID(event) {
		return "review-after-time-mismatch"
	}
	decision := event.AlgorithmDecision
	if (decision.Policy != "primary" && decision.Policy != "fallback") || decision.Winner == "" || !containsString(decision.EnabledAlgorithms, decision.Winner) || (decision.Policy == "fallback" && decision.FallbackReason == "") {
		return "review-decision-invalid"
	}
	var winner *AlgorithmCandidate
	for i := range event.AlgorithmCandidates {
		if event.AlgorithmCandidates[i].Algorithm == decision.Winner {
			winner = &event.AlgorithmCandidates[i]
			break
		}
	}
	if winner == nil {
		return "review-winner-candidate-missing"
	}
	seenCandidates := make(map[string]bool, len(event.AlgorithmCandidates))
	candidatesByAlgorithm := make(map[string]AlgorithmCandidate, len(event.AlgorithmCandidates))
	for _, candidate := range event.AlgorithmCandidates {
		descriptor, supported := schedulingAlgorithmDescriptor(candidate.Algorithm)
		if !supported || !supportsTarget(descriptor, "element.item") {
			if candidate.Algorithm == decision.Winner {
				return "review-winner-target-kind-invalid"
			}
			return "review-candidate-target-kind-invalid"
		}
		if seenCandidates[candidate.Algorithm] || !containsString(decision.EnabledAlgorithms, candidate.Algorithm) {
			return "review-candidate-set-invalid"
		}
		seenCandidates[candidate.Algorithm] = true
		candidatesByAlgorithm[candidate.Algorithm] = candidate
		switch candidate.Status {
		case "valid":
			if ValidateCandidate(candidate, descriptor, "element.item", event.OccurredAt) != nil {
				if candidate.Algorithm == decision.Winner {
					return "review-winner-candidate-invalid"
				}
				return "review-candidate-invalid"
			}
			if validateCandidateTransition(candidate, reviewAlgorithmInput(event, "element.item", candidate.Algorithm)) != nil {
				return "review-candidate-transition-invalid"
			}
			state, found := event.After.AlgorithmStates[candidate.Algorithm]
			if !found {
				if candidate.Algorithm == decision.Winner {
					return "review-winner-state-missing"
				}
				return "review-candidate-state-missing"
			}
			stateHash, stateErr := canonicalHash(state)
			candidateHash, candidateErr := canonicalHash(candidate.NextState)
			if stateErr != nil || candidateErr != nil || stateHash != candidateHash {
				if candidate.Algorithm == decision.Winner {
					return "review-winner-state-mismatch"
				}
				return "review-candidate-state-mismatch"
			}
		case "invalid", "error", "unsupported":
			input := reviewAlgorithmInput(event, "element.item", candidate.Algorithm)
			if candidate.ValidationReason == "" || (ValidateCandidate(candidate, descriptor, "element.item", event.OccurredAt) == nil && validateCandidateTransition(candidate, input) == nil) {
				return "review-candidate-status-invalid"
			}
		default:
			return "review-candidate-status-invalid"
		}
	}
	if len(decision.EnabledAlgorithms) != 2 || decision.EnabledAlgorithms[0] != fsrsV1ID || decision.EnabledAlgorithms[1] != simpleV1ID || len(event.AlgorithmCandidates) != 2 || event.AlgorithmCandidates[0].Algorithm != fsrsV1ID || event.AlgorithmCandidates[1].Algorithm != simpleV1ID {
		return "review-candidate-set-invalid"
	}
	for _, algorithm := range decision.EnabledAlgorithms {
		if !seenCandidates[algorithm] {
			return "review-candidate-set-invalid"
		}
	}
	primary := candidatesByAlgorithm[decision.EnabledAlgorithms[0]]
	if primary.Status == "valid" {
		if decision.Policy != "primary" || decision.Winner != primary.Algorithm || decision.FallbackReason != "" {
			return "review-decision-invalid"
		}
	} else if decision.Policy != "fallback" || decision.Winner == primary.Algorithm || winner.Status != "valid" || decision.FallbackReason == "" {
		return "review-decision-invalid"
	}
	if winner.Status != "valid" {
		return "review-winner-candidate-invalid"
	}
	if winner.NextIntervalDays != event.After.IntervalDays || !winner.NextDueAt.Equal(event.After.DueAt) {
		return "review-winner-schedule-mismatch"
	}
	if !legacyScheduled && event.After.DueLearningDay != addLearningDays(eventLearningDayID(event), winner.NextIntervalDays) {
		return "review-due-learning-day-invalid"
	}
	if event.After.ActiveAlgorithm != decision.Winner {
		return "review-active-algorithm-mismatch"
	}
	state, found := event.After.AlgorithmStates[decision.Winner]
	if !found {
		return "review-winner-state-missing"
	}
	stateHash, stateErr := canonicalHash(state)
	winnerHash, winnerErr := canonicalHash(winner.NextState)
	if stateErr != nil || winnerErr != nil || stateHash != winnerHash {
		return "review-winner-state-mismatch"
	}
	switch decision.Winner {
	case fsrsV1ID:
		decoded, err := decodeAlgorithmState[FSRSV1State](state, fsrsV1ID, 1)
		if err != nil || uint64(event.After.Repetitions) != decoded.Repetitions || uint64(event.After.Lapses) != decoded.Lapses {
			return "review-winner-projection-state-mismatch"
		}
	case simpleV1ID:
		decoded, err := decodeAlgorithmState[SimpleV1State](state, simpleV1ID, 1)
		if err != nil || event.After.Repetitions != decoded.Repetitions || event.After.Lapses != decoded.Lapses {
			return "review-winner-projection-state-mismatch"
		}
	}
	if legacyScheduled {
		if event.DrillAdmissionEventID != "" || event.BaseDrillEventID != "" {
			return "invalid-legacy-scheduled-review"
		}
		return ""
	}
	if *event.RawGrade <= 3 {
		if event.DrillEffect != "admit" || event.DrillAdmissionEventID != "" || event.BaseDrillEventID != "" {
			return "review-drill-admission-invalid"
		}
	} else if event.DrillEffect != "" || event.DrillAdmissionEventID != "" || event.BaseDrillEventID != "" {
		return "review-drill-effect-invalid"
	}
	return ""
}

func hasFeature003ReviewProjectionFields(projection SchedulingProjection) bool {
	return projection.ScheduleProfile != "" || projection.AcceptedReviewAction != "" || projection.DueLearningDay != ""
}

func validateTopicSchedulingEvent(event SchedulingEvent) string {
	if event.RawGrade != nil || event.Passed != nil || event.RatingLabel != "" || event.RatingMapping != "" {
		return "topic-review-has-grade"
	}
	if reason := validateEventLearningDayFields(event, "topic-learning-date-invalid"); reason != "" {
		return reason
	}
	if event.After.ScheduleProfile != topicAFactorV1ID || event.After.AcceptedReviewAction != "NextTopic" || event.After.ActiveAlgorithm != topicAFactorV1ID {
		return "topic-schedule-profile-invalid"
	}
	if event.After.LifecycleState != "memorized" {
		return "invalid-topic-lifecycle-transition"
	}
	if event.Before.Lapses != 0 || event.After.Lapses != 0 || event.Before.LastRawGrade != nil || event.Before.LastPassed != nil || event.After.LastRawGrade != nil || event.After.LastPassed != nil {
		return "topic-item-memory-state-invalid"
	}
	if event.After.IntervalDays < 1 || event.TopicNextIntervalDays < 1 || event.After.IntervalDays != event.TopicNextIntervalDays {
		return "topic-interval-invalid"
	}
	if event.TopicEffectiveAFactor <= 0 || math.IsNaN(event.TopicEffectiveAFactor) || math.IsInf(event.TopicEffectiveAFactor, 0) {
		return "topic-afactor-invalid"
	}
	expectedDueDay := addLearningDays(eventLearningDayID(event), event.TopicNextIntervalDays)
	if event.After.DueLearningDay != expectedDueDay {
		return "topic-due-learning-day-invalid"
	}
	switch event.ReviewKind {
	case "introduceTopic":
		if event.Before.Repetitions != 0 || event.After.Repetitions != 1 {
			return "topic-repetition-invalid"
		}
		if event.TopicPolicyVersion != "siyuanmemo-topic-initial-v1" || event.TopicSeed != topicInitialSeed(event.EventID, event.ElementID) || event.TopicInitialIntervalDays != topicInitialInterval(event.TopicSeed) || event.TopicInitialIntervalDays < 1 || event.TopicInitialIntervalDays > 15 || event.TopicInitialIntervalDays != event.TopicNextIntervalDays || event.TopicPreviousIntervalDays != 0 {
			return "topic-initial-policy-invalid"
		}
	case "nextTopic":
		if event.After.Repetitions != event.Before.Repetitions+1 {
			return "topic-repetition-invalid"
		}
		if event.TopicPolicyVersion != "siyuanmemo-topic-afactor-v1" || event.TopicPreviousIntervalDays < 1 || event.TopicPreviousIntervalDays != event.Before.IntervalDays || event.TopicMinimumIntervalDays < 1 || event.TopicMaximumIntervalDays < event.TopicMinimumIntervalDays || event.TopicSkipPolicy != "none" {
			return "topic-review-policy-invalid"
		}
		rawInterval := math.Ceil(float64(event.TopicPreviousIntervalDays) * event.TopicEffectiveAFactor)
		if math.IsNaN(rawInterval) || math.IsInf(rawInterval, 0) {
			return "topic-interval-formula-invalid"
		}
		expectedInterval := event.TopicMinimumIntervalDays
		if rawInterval > float64(event.TopicMaximumIntervalDays) {
			expectedInterval = event.TopicMaximumIntervalDays
		} else if rawInterval > float64(expectedInterval) {
			expectedInterval = int(rawInterval)
		}
		if event.TopicNextIntervalDays != expectedInterval {
			return "topic-interval-formula-invalid"
		}
		if len(event.AlgorithmCandidates) != 1 {
			return "topic-candidate-missing"
		}
		candidate := event.AlgorithmCandidates[0]
		descriptor, supported := schedulingAlgorithmDescriptor(candidate.Algorithm)
		if !supported || !supportsTarget(descriptor, "element.topic") {
			return "topic-candidate-target-kind-invalid"
		}
		if candidate.Algorithm != topicAFactorV1ID || candidate.Status != "valid" || ValidateCandidate(candidate, descriptor, "element.topic", event.OccurredAt) != nil {
			return "topic-candidate-invalid"
		}
		if candidate.NextIntervalDays != event.After.IntervalDays || !candidate.NextDueAt.Equal(event.After.DueAt) {
			return "topic-candidate-schedule-mismatch"
		}
		candidateStateHash, candidateStateErr := canonicalHash(candidate.NextState)
		afterStateHash, afterStateErr := canonicalHash(event.After.AlgorithmStates[topicAFactorV1ID])
		if candidateStateErr != nil || afterStateErr != nil || candidateStateHash != afterStateHash {
			return "topic-candidate-state-mismatch"
		}
	}
	if event.After.LastReviewAt == nil || !event.After.LastReviewAt.Equal(event.OccurredAt) || event.After.LastLearningDate != eventLearningDayID(event) {
		return "topic-after-time-mismatch"
	}
	state, found := event.After.AlgorithmStates[topicAFactorV1ID]
	if !found {
		return "topic-state-missing"
	}
	decoded, err := decodeAlgorithmState[TopicAFactorV1State](state, topicAFactorV1ID, 1)
	if err != nil || decoded.IntervalDays != event.After.IntervalDays || decoded.Repetitions != event.After.Repetitions || decoded.EffectiveAFactor != event.TopicEffectiveAFactor {
		return "topic-state-invalid"
	}
	if event.ReviewKind == "introduceTopic" && (decoded.LastLearningDay != eventLearningDayID(event) || decoded.DueLearningDay != expectedDueDay) {
		return "topic-state-invalid"
	}
	return ""
}

func validateDrillSchedulingEvent(event SchedulingEvent) string {
	if event.SessionID == "" {
		return "review-session-missing"
	}
	if event.ReviewKind != "drillGrade" {
		return "review-kind-invalid"
	}
	if event.RawGrade == nil {
		return "review-grade-missing"
	}
	normalized, err := NormalizeGrade(*event.RawGrade)
	if err != nil {
		return "review-grade-invalid"
	}
	if event.Passed == nil || *event.Passed != normalized.Passed {
		return "review-pass-mismatch"
	}
	if event.RatingLabel != normalized.RatingLabel || event.RatingMapping != normalized.RatingMapping {
		return "review-rating-invalid"
	}
	if reason := validateEventLearningDayFields(event, "review-learning-date-invalid"); reason != "" {
		return reason
	}
	if event.Before.ScheduleProfile != fsrsV1ID || event.Before.AcceptedReviewAction != "GradeItem" || event.After.ScheduleProfile != event.Before.ScheduleProfile || event.After.AcceptedReviewAction != event.Before.AcceptedReviewAction {
		return "drill-target-kind-invalid"
	}
	if event.Before.AdoptedTerminalID == "" || event.Before.AdoptedTerminalID != event.After.AdoptedTerminalID || !projectionsCompatible(event.Before, event.After) {
		return "drill-schedule-changed"
	}
	if event.DrillAdmissionEventID == "" {
		return "drill-admission-missing"
	}
	if *event.RawGrade <= 3 {
		if event.DrillEffect != "retain" {
			return "drill-effect-grade-mismatch"
		}
	} else {
		if event.DrillEffect != "remove" {
			return "drill-effect-grade-mismatch"
		}
	}
	return ""
}

func reviewAlgorithmInput(event SchedulingEvent, targetKind, algorithm string) AlgorithmInput {
	review := NormalizedReview{
		ElementID:                   event.ElementID,
		TargetKind:                  targetKind,
		ReviewAt:                    event.OccurredAt,
		LearningDate:                event.LearningDate,
		LearningDayID:               event.LearningDayID,
		SessionID:                   event.SessionID,
		EventID:                     event.EventID,
		ObservedBaseSchedulingEvent: event.BaseEventID,
	}
	if event.RawGrade != nil {
		review.RawGrade = *event.RawGrade
		review.Passed = event.Passed != nil && *event.Passed
		review.RatingLabel = event.RatingLabel
		review.RatingMapping = event.RatingMapping
		review.ActionKind = string(ActionGradeItem)
	} else {
		review.ActionKind = string(ActionNextTopic)
	}
	return AlgorithmInput{ElementID: event.ElementID, TargetKind: targetKind, Review: review, Before: event.Before, CurrentState: event.Before.AlgorithmStates[algorithm]}
}

func validateSchedulingEventCandidateTransitions(event SchedulingEvent) string {
	if event.ReviewKind == "nextTopic" {
		for _, candidate := range event.AlgorithmCandidates {
			if candidate.Status == "valid" && validateCandidateTransition(candidate, reviewAlgorithmInput(event, "element.topic", candidate.Algorithm)) != nil {
				return "topic-candidate-transition-invalid"
			}
		}
	}
	return ""
}

func schedulingAlgorithmDescriptor(algorithm string) (AlgorithmDescriptor, bool) {
	switch algorithm {
	case fsrsV1ID:
		return NewFSRSV1Adapter(SchedulerConfig{}).Describe(), true
	case simpleV1ID:
		return NewSimpleV1Adapter().Describe(), true
	case topicAFactorV1ID:
		return NewTopicAFactorV1Adapter(SchedulerConfig{}).Describe(), true
	default:
		return AlgorithmDescriptor{}, false
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func projectionsCompatible(parent, child SchedulingProjection) bool {
	if parent.ElementID != child.ElementID || parent.ScheduleProfile != child.ScheduleProfile || parent.AcceptedReviewAction != child.AcceptedReviewAction || parent.LifecycleState != child.LifecycleState {
		return false
	}
	if !parent.DueAt.Equal(child.DueAt) || parent.DueLearningDay != child.DueLearningDay || parent.IntervalDays != child.IntervalDays || parent.Repetitions != child.Repetitions || parent.Lapses != child.Lapses {
		return false
	}
	if !sameOptionalTime(parent.LastReviewAt, child.LastReviewAt) || parent.LastLearningDate != child.LastLearningDate || !sameOptionalInt(parent.LastRawGrade, child.LastRawGrade) || !sameOptionalBool(parent.LastPassed, child.LastPassed) {
		return false
	}
	return parent.ActiveAlgorithm == child.ActiveAlgorithm && parent.PriorityPosition == child.PriorityPosition && sameAlgorithmStates(parent.AlgorithmStates, child.AlgorithmStates)
}

func sameAlgorithmStates(left, right map[string]VersionedAlgorithmState) bool {
	leftHash, leftErr := canonicalHash(left)
	rightHash, rightErr := canonicalHash(right)
	return leftErr == nil && rightErr == nil && leftHash == rightHash
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func sameOptionalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameOptionalBool(left, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func isFormalSchedulingEvent(event SchedulingEvent) bool {
	return event.Type == "introduceElement" || event.Type == "reviewElement"
}

func eventLearningDayID(event SchedulingEvent) string {
	if event.LearningDayID != "" {
		return event.LearningDayID
	}
	return event.LearningDate
}

func validateEventLearningDayFields(event SchedulingEvent, reason string) string {
	if event.LearningDate != "" {
		if _, err := time.Parse("2006-01-02", event.LearningDate); err != nil {
			return reason
		}
	}
	if event.LearningDayID != "" {
		if _, err := time.Parse("2006-01-02", event.LearningDayID); err != nil {
			return reason
		}
	}
	if event.LearningDate != "" && event.LearningDayID != "" && event.LearningDate != event.LearningDayID {
		return reason
	}
	if eventLearningDayID(event) == "" {
		return reason
	}
	return ""
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func applyFinalDrillProjection(effective map[string]SchedulingEvent, valid, adopted, duplicate map[string]bool, invalidReason map[string]string, currentLearningDay string) (map[string]FinalDrillProjection, map[string]bool) {
	admissions := make([]SchedulingEvent, 0)
	admissionByID := map[string]SchedulingEvent{}
	for id, event := range effective {
		if adopted[id] && !duplicate[id] && valid[id] && isFinalDrillAdmission(event) {
			admissions = append(admissions, event)
			admissionByID[id] = event
		}
	}
	sortSchedulingEvents(admissions)
	causalDepthByID := make(map[string]int, len(effective))
	causalDepthVisiting := make(map[string]bool, len(effective))
	latestAdmissionAt := func(event SchedulingEvent) string {
		latest := SchedulingEvent{}
		latestDepth := -1
		for _, admission := range admissions {
			if admission.ElementID != event.ElementID || admission.OccurredAt.After(event.OccurredAt) {
				continue
			}
			depth := schedulingEventCausalDepth(admission.EventID, effective, causalDepthByID, causalDepthVisiting)
			if depth > latestDepth || (depth == latestDepth && (latest.EventID == "" || eventLater(admission, latest))) {
				latest = admission
				latestDepth = depth
			}
		}
		return latest.EventID
	}
	admissionAtFormalTerminal := func(terminalID string) string {
		for terminalID != "" {
			event, found := effective[terminalID]
			if !found {
				return ""
			}
			if isFinalDrillAdmission(event) {
				return event.EventID
			}
			terminalID = event.BaseEventID
		}
		return ""
	}

	drillVisit := map[string]int{}
	drillValid := map[string]bool{}
	var validateDrill func(string) bool
	validateDrill = func(id string) bool {
		if drillVisit[id] == 2 {
			return drillValid[id]
		}
		if drillVisit[id] == 1 {
			invalidReason[id] = "cyclic-drill-base"
			valid[id] = false
			return false
		}
		event, ok := effective[id]
		if !ok || event.Type != "drillElement" || !valid[id] || duplicate[id] {
			return false
		}
		drillVisit[id] = 1
		admission, admissionOK := admissionByID[event.DrillAdmissionEventID]
		switch {
		case !admissionOK:
			invalidReason[id] = "drill-admission-invalid"
		case admission.ElementID != event.ElementID:
			invalidReason[id] = "drill-admission-cross-element"
		case latestAdmissionAt(event) != event.DrillAdmissionEventID:
			invalidReason[id] = "drill-admission-stale"
		case !adopted[event.Before.AdoptedTerminalID]:
			invalidReason[id] = "drill-formal-base-invalid"
		case admissionAtFormalTerminal(event.Before.AdoptedTerminalID) != event.DrillAdmissionEventID:
			invalidReason[id] = "drill-admission-formal-base-mismatch"
		default:
			formalBase := effective[event.Before.AdoptedTerminalID]
			if formalBase.ElementID != event.ElementID || !projectionsCompatible(formalBase.After, event.Before) {
				invalidReason[id] = "drill-formal-base-invalid"
			}
		}
		if invalidReason[id] == "" && event.BaseDrillEventID != "" {
			parent, parentOK := effective[event.BaseDrillEventID]
			switch {
			case !parentOK:
				invalidReason[id] = "missing-drill-base"
			case parent.ElementID != event.ElementID || parent.DrillAdmissionEventID != event.DrillAdmissionEventID:
				invalidReason[id] = "cross-drill-base"
			case !validateDrill(parent.EventID):
				invalidReason[id] = "invalid-drill-base"
			case parent.DrillEffect == "remove":
				invalidReason[id] = "drill-member-missing"
			}
		}
		drillVisit[id] = 2
		drillValid[id] = invalidReason[id] == ""
		if !drillValid[id] {
			valid[id] = false
		}
		return drillValid[id]
	}
	for id, event := range effective {
		if event.Type == "drillElement" {
			validateDrill(id)
		}
	}

	children := map[string][]string{}
	for id, event := range effective {
		if drillValid[id] && event.BaseDrillEventID != "" {
			children[event.BaseDrillEventID] = append(children[event.BaseDrillEventID], id)
		}
	}
	winningTerminal := map[string]string{}
	for id, event := range effective {
		if !drillValid[id] || len(children[id]) > 0 {
			continue
		}
		root := event.DrillAdmissionEventID
		currentID := winningTerminal[root]
		if currentID == "" || eventLater(event, effective[currentID]) {
			winningTerminal[root] = id
		}
	}
	drillAdopted := map[string]bool{}
	for _, terminalID := range winningTerminal {
		for id := terminalID; id != ""; id = effective[id].BaseDrillEventID {
			drillAdopted[id] = true
		}
	}

	events := append([]SchedulingEvent(nil), admissions...)
	for id, event := range effective {
		if drillAdopted[id] {
			events = append(events, event)
		}
	}
	sortFinalDrillEvents(events, effective)
	projections := map[string]FinalDrillProjection{}
	members := map[string]bool{}
	lastActivityDay := ""
	expireGeneration := func() {
		for elementID := range members {
			projection := projections[elementID]
			projection.Member = false
			projection.Expired = true
			projections[elementID] = projection
		}
		members = map[string]bool{}
	}
	for _, event := range events {
		activityDay := eventLearningDayID(event)
		if lastActivityDay != "" && completeLearningDayGap(lastActivityDay, activityDay) > 3 {
			expireGeneration()
		}
		if isFinalDrillAdmission(event) {
			projection := FinalDrillProjection{ElementID: event.ElementID, Member: true, LastActivityDay: activityDay, AdmissionEventID: event.EventID}
			projections[event.ElementID] = projection
			members[event.ElementID] = true
			lastActivityDay = activityDay
			continue
		}
		projection, found := projections[event.ElementID]
		if !found || !members[event.ElementID] || projection.AdmissionEventID != event.DrillAdmissionEventID {
			invalidReason[event.EventID] = "drill-member-missing"
			valid[event.EventID] = false
			delete(drillAdopted, event.EventID)
			continue
		}
		projection.Member = event.DrillEffect != "remove"
		projection.LastActivityDay = activityDay
		projection.AdoptedTerminalEventID = event.EventID
		projection.Expired = false
		projections[event.ElementID] = projection
		if projection.Member {
			members[event.ElementID] = true
		} else {
			delete(members, event.ElementID)
		}
		lastActivityDay = activityDay
	}
	if currentLearningDay != "" && lastActivityDay != "" && completeLearningDayGap(lastActivityDay, currentLearningDay) > 3 {
		expireGeneration()
	}
	return projections, drillAdopted
}

func isFinalDrillAdmission(event SchedulingEvent) bool {
	legacyAdmission := event.ReviewKind == "" && event.DrillEffect == ""
	return event.RawGrade != nil && *event.RawGrade <= 3 && (event.DrillEffect == "admit" || legacyAdmission) && (legacyAdmission || event.ReviewKind == "gradeItem" || event.ReviewKind == "introduceItem")
}

func sortSchedulingEvents(events []SchedulingEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].OccurredAt.Equal(events[j].OccurredAt) {
			return events[i].OccurredAt.Before(events[j].OccurredAt)
		}
		return events[i].EventID < events[j].EventID
	})
}

func sortFinalDrillEvents(events []SchedulingEvent, authority map[string]SchedulingEvent) {
	depthByID := make(map[string]int, len(authority))
	visiting := make(map[string]bool, len(authority))
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].OccurredAt.Equal(events[j].OccurredAt) {
			return events[i].OccurredAt.Before(events[j].OccurredAt)
		}
		leftDepth := schedulingEventCausalDepth(events[i].EventID, authority, depthByID, visiting)
		rightDepth := schedulingEventCausalDepth(events[j].EventID, authority, depthByID, visiting)
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return events[i].EventID < events[j].EventID
	})
}

func schedulingEventCausalDepth(eventID string, authority map[string]SchedulingEvent, depths map[string]int, visiting map[string]bool) int {
	if depth, found := depths[eventID]; found {
		return depth
	}
	if visiting[eventID] {
		return 0
	}
	event, found := authority[eventID]
	if !found {
		return 0
	}
	visiting[eventID] = true
	baseEventID := event.BaseEventID
	if event.Type == "drillElement" {
		baseEventID = event.BaseDrillEventID
		if baseEventID == "" {
			baseEventID = event.DrillAdmissionEventID
		}
	}
	depth := 0
	if baseEventID != "" {
		if _, found = authority[baseEventID]; found {
			depth = schedulingEventCausalDepth(baseEventID, authority, depths, visiting) + 1
		}
	}
	delete(visiting, eventID)
	depths[eventID] = depth
	return depth
}

func completeLearningDayGap(left, right string) int {
	start, startErr := time.Parse("2006-01-02", left)
	end, endErr := time.Parse("2006-01-02", right)
	if startErr != nil || endErr != nil || end.Before(start) {
		return 0
	}
	return int(end.Sub(start).Hours() / 24)
}

func eventLater(left, right SchedulingEvent) bool {
	if !left.OccurredAt.Equal(right.OccurredAt) {
		return left.OccurredAt.After(right.OccurredAt)
	}
	return left.EventID > right.EventID
}

func (ledger *SchedulingLedger) String() string {
	return fmt.Sprintf("SchedulingLedger(%s)", ledger.config.ReviewsRoot())
}
