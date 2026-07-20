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

func newSchedulingLedger(config Config, index *projectionIndex) *SchedulingLedger {
	return &SchedulingLedger{config: config, index: index}
}

func (ledger *SchedulingLedger) Snapshot(elementID string) (SchedulingProjection, error) {
	projection, err := ledger.index.projection(elementID)
	if err != nil {
		if errors.Is(err, errProjectionNotFound) {
			return SchedulingProjection{}, domainError(ErrAuthoritativeItemUnavailable, "scheduling projection is unavailable", err)
		}
		return SchedulingProjection{}, err
	}
	return projection, nil
}

func (ledger *SchedulingLedger) Commit(ctx context.Context, event SchedulingEvent) (SchedulingProjection, bool, error) {
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
	if err = ledger.refreshLocked(ctx); err != nil {
		domainErr := wrapDomainError(ErrProjectionRefreshFailed, "refresh scheduling projection: %v", err)
		domainErr.Retryable = true
		domainErr.ReviewAccepted = true
		domainErr.AcceptedEventID = event.EventID
		return SchedulingProjection{}, false, domainErr
	}
	projection, err := ledger.index.projection(event.ElementID)
	return projection, false, err
}

func (ledger *SchedulingLedger) Refresh(ctx context.Context) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	return ledger.refreshLocked(ctx)
}

func (ledger *SchedulingLedger) refreshLocked(ctx context.Context) error {
	elementScan, err := ledger.config.scanElements()
	if err != nil {
		return err
	}
	events, err := ledger.config.LoadEventFiles()
	if err != nil {
		return err
	}
	for _, event := range events {
		if _, fatal := validateSchedulingEvent(event); fatal {
			return domainError(ErrHistoryRequiresRepair, "review history contains an unsupported event type", nil)
		}
	}
	projections, diagnostics := projectSchedulingEvents(events)
	sourceDiagnostics := sourceDiagnosticsWithMissingProjections(elementScan, projections)
	effectiveConfig := ledger.config.LoadEffectiveSchedulerConfig()
	if len(events) > 0 {
		sourceDiagnostics = append(sourceDiagnostics, effectiveConfig.Diagnostics...)
	}
	sourceDiagnostics = normalizeSourceDiagnostics(sourceDiagnostics)
	tree := buildElementTree(elementScan.Records, projections, true)
	return ledger.index.replaceAll(ctx, projectionBuild{Elements: elementScan.Elements, Tree: tree, Projections: projections, EventDiagnostics: diagnostics, SourceDiagnostics: sourceDiagnostics})
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
	month := monthFor(event.OccurredAt.In(ledger.config.Location))
	path := filepath.Join(ledger.config.ReviewsRoot(), month+".smr")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file := EventFile{Spec: SupportedEventSpec, Month: month}
	if data, err := filelock.ReadFile(path); err == nil {
		if err = json.Unmarshal(data, &file); err != nil {
			return err
		}
		if file.Spec != SupportedEventSpec || file.Month != month {
			return errors.New("event file envelope is incompatible")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file.Events = append(file.Events, event)
	sort.SliceStable(file.Events, func(i, j int) bool {
		if !file.Events[i].OccurredAt.Equal(file.Events[j].OccurredAt) {
			return file.Events[i].OccurredAt.Before(file.Events[j].OccurredAt)
		}
		return file.Events[i].EventID < file.Events[j].EventID
	})
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return filelock.WriteFile(path, data)
}

type eventGroup struct {
	event  SchedulingEvent
	hashes map[string]int
}

func projectSchedulingEvents(events []SchedulingEvent) (map[string]SchedulingProjection, []EventDiagnostic) {
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
	invalidReason := map[string]string{}
	for id, group := range groups {
		if id == "" || len(group.hashes) != 1 {
			invalidReason[id] = "conflicting-event-identity"
			continue
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
		if event.BaseEventID != "" {
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
		if valid[id] && event.BaseEventID != "" && valid[event.BaseEventID] {
			children[event.BaseEventID] = append(children[event.BaseEventID], id)
		}
	}
	winningTerminal := map[string]string{}
	for id, event := range effective {
		if !valid[id] || len(children[id]) > 0 {
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
			} else if adopted[id] {
				classification = "adopted"
			} else if valid[id] {
				classification = "concurrent-superseded"
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
	return projections, diagnostics
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
	if event.After.AdoptedTerminalID != event.EventID {
		return "projection-terminal-mismatch", false
	}
	if event.After.LifecycleState == "" || event.After.DueAt.IsZero() {
		return "incomplete-after-projection", false
	}
	if event.After.IntervalDays < 0 || event.After.Repetitions < 0 || event.After.Lapses < 0 || event.After.Lapses > event.After.Repetitions {
		return "invalid-after-projection", false
	}
	if event.Type == "introduceElement" {
		if event.Before.LifecycleState == "memorized" || event.After.LifecycleState != "memorized" {
			return "invalid-introduction-transition", false
		}
		return "", false
	}
	return validateReviewSchedulingEvent(event), false
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
	if event.ReviewKind != "scheduled" {
		return "review-kind-invalid"
	}
	if _, err = time.Parse("2006-01-02", event.LearningDate); err != nil {
		return "review-learning-date-invalid"
	}
	if event.Before.LifecycleState != "memorized" || event.After.LifecycleState != "memorized" {
		return "invalid-review-lifecycle-transition"
	}
	if event.After.LastRawGrade == nil || *event.After.LastRawGrade != *event.RawGrade {
		return "review-after-grade-mismatch"
	}
	if event.After.LastPassed == nil || *event.After.LastPassed != *event.Passed {
		return "review-after-pass-mismatch"
	}
	if event.After.LastReviewAt == nil || !event.After.LastReviewAt.Equal(event.OccurredAt) || event.After.LastLearningDate != event.LearningDate {
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
	descriptor, supported := schedulingAlgorithmDescriptor(decision.Winner)
	if !supported || winner.Status != "valid" || ValidateCandidate(*winner, descriptor, event.OccurredAt) != nil {
		return "review-winner-candidate-invalid"
	}
	if winner.NextIntervalDays != event.After.IntervalDays || !winner.NextDueAt.Equal(event.After.DueAt) {
		return "review-winner-schedule-mismatch"
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
	return ""
}

func schedulingAlgorithmDescriptor(algorithm string) (AlgorithmDescriptor, bool) {
	switch algorithm {
	case fsrsV1ID:
		return NewFSRSV1Adapter(SchedulerConfig{}).Describe(), true
	case simpleV1ID:
		return NewSimpleV1Adapter().Describe(), true
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
	parent.AdoptedTerminalID = ""
	child.AdoptedTerminalID = ""
	parentData, parentErr := json.Marshal(parent)
	childData, childErr := json.Marshal(child)
	return parentErr == nil && childErr == nil && string(parentData) == string(childData)
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
