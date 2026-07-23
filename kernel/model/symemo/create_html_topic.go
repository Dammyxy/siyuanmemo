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
	"strings"

	"github.com/88250/lute/ast"
)

var newCreateHTMLTopicElementID = ast.NewNodeID
var newCreateHTMLTopicEventID = ast.NewNodeID
var marshalCreateHTMLTopicAuthorityJSON = json.MarshalIndent
var statCreateHTMLTopicRoot = os.Stat

var createHTMLTopicAuthorityFault func(stage string) error

func invokeCreateHTMLTopicAuthorityFault(stage string) error {
	if createHTMLTopicAuthorityFault == nil {
		return nil
	}
	return createHTMLTopicAuthorityFault(stage)
}

type createHTMLTopicPlan struct {
	elementID string
	eventID   string
	element   Element
	event     SchedulingEvent
	rootBytes []byte
	sortBytes []byte
}

func (engine *Engine) createHTMLTopic(ctx context.Context, command AddNewTopicCommand) (CreateElementResult, error) {
	title := strings.TrimSpace(command.Title)
	if title == "" {
		return CreateElementResult{}, createHTMLTopicError(ErrInvalidCreateCommand, "Topic title is required", "", "", false, false, nil)
	}
	cleanedHTML, err := cleanTopicHTMLFragment(command.HTML)
	if err != nil {
		return CreateElementResult{}, createHTMLTopicError(ErrInvalidCreateCommand, "Topic HTML is not renderable", "", "", false, false, err)
	}
	if !engine.schedulerConfigIsCurrent() {
		return CreateElementResult{}, createHTMLTopicError(ErrHistoryRequiresRepair, "scheduler configuration requires repair", "", "", false, false, nil)
	}
	plan, err := engine.planCreateHTMLTopic(title, cleanedHTML)
	if err != nil {
		return CreateElementResult{}, err
	}
	result := CreateElementResult{ElementID: plan.elementID, EventID: plan.eventID, Retryable: false}
	rootWritten := false
	sortWritten := false
	alreadyAccepted, err := engine.ledger.commitPrepared(plan.event, marshalCreateHTMLTopicAuthorityJSON, func() error {
		if writeErr := engine.config.writeRootElementSource(plan.element, plan.rootBytes); writeErr != nil {
			if _, statErr := statCreateHTMLTopicRoot(filepath.Join(engine.config.ElementsRoot(), plan.elementID+".sme")); !errors.Is(statErr, os.ErrNotExist) {
				rootWritten = true
			}
			return writeErr
		}
		rootWritten = true
		if writeErr := engine.config.writeTopLevelSortRanks(plan.sortBytes); writeErr != nil {
			return writeErr
		}
		sortWritten = true
		return nil
	})
	if err != nil {
		var serializationErr *schedulingEventSerializationError
		if errors.As(err, &serializationErr) {
			return result, createHTMLTopicError(ErrInvalidCreateCommand, "created Topic authority is not serializable", plan.elementID, plan.eventID, false, false, err)
		}
		writeErr := classifyCreateHTMLTopicWriteError(err, plan.elementID, plan.eventID, rootWritten, sortWritten)
		if rootWritten || sortWritten {
			engine.unavailable.Store(true)
		}
		return result, writeErr
	}
	if alreadyAccepted {
		return result, createHTMLTopicError(ErrInvalidCreateCommand, "generated Topic event identity collides with existing authority", plan.elementID, plan.eventID, false, false, nil)
	}
	if err = engine.refreshProjectionWithConfig(ctx, engine.config.LoadEffectiveSchedulerConfig()); err != nil {
		engine.unavailable.Store(true)
		return result, createHTMLTopicError(ErrProjectionRefreshFailed, "refresh created Topic projection", plan.elementID, plan.eventID, true, true, err)
	}
	summary, err := engine.createdHTMLTopicSummary(plan.elementID, plan.eventID)
	if err != nil {
		engine.unavailable.Store(true)
		return result, createHTMLTopicError(ErrProjectionRefreshFailed, "read created Topic projection", plan.elementID, plan.eventID, true, true, err)
	}
	result.CreateAccepted = true
	result.ReviewAccepted = true
	result.Topic = summary
	return result, nil
}

func (engine *Engine) planCreateHTMLTopic(title, cleanedHTML string) (createHTMLTopicPlan, error) {
	scan, err := engine.config.scanElements()
	if err != nil {
		return createHTMLTopicPlan{}, createHTMLTopicError(ErrHistoryRequiresRepair, "Element source requires repair", "", "", false, false, err)
	}
	if len(scan.Diagnostics) != 0 {
		return createHTMLTopicPlan{}, createHTMLTopicError(ErrHistoryRequiresRepair, "Element source requires repair", "", "", false, false, nil)
	}
	sortRanks, err := engine.config.loadSortRanksForCreate()
	if err != nil {
		return createHTMLTopicPlan{}, err
	}
	events, err := engine.config.LoadEventFiles()
	if err != nil {
		return createHTMLTopicPlan{}, err
	}
	sortRank, err := nextTopLevelSortRank(scan.Records)
	if err != nil {
		return createHTMLTopicPlan{}, err
	}
	elementID, eventID, err := engine.generateCreateHTMLTopicIdentities(scan, events)
	if err != nil {
		return createHTMLTopicPlan{}, err
	}
	sortRanks[elementID] = sortRank
	element := Element{
		Spec:            SupportedElementSpec,
		ID:              elementID,
		Type:            "topic",
		Title:           title,
		ProcessingState: "new",
		PayloadSpec:     SupportedPayloadSpec,
		Payload: ElementPayload{Material: &TopicMaterial{
			Kind:                  "html",
			HTML:                  cleanedHTML,
			CleaningPolicyVersion: topicHTMLCleaningPolicyVersion,
		}},
	}
	rootBytes, err := marshalCreateHTMLTopicAuthorityJSON(element, "", "  ")
	if err != nil {
		return createHTMLTopicPlan{}, createHTMLTopicError(ErrInvalidCreateCommand, "created Topic source is not serializable", elementID, eventID, false, false, err)
	}
	rootBytes = append(rootBytes, '\n')
	sortBytes, err := marshalCreateHTMLTopicAuthorityJSON(sortRanks, "", "  ")
	if err != nil {
		return createHTMLTopicPlan{}, createHTMLTopicError(ErrInvalidCreateCommand, "created Topic sort metadata is not serializable", elementID, eventID, false, false, err)
	}
	sortBytes = append(sortBytes, '\n')
	event, err := engine.scheduler.planTopicIntroduction(elementID, eventID)
	if err != nil {
		return createHTMLTopicPlan{}, err
	}
	if reason, fatal := validateSchedulingEvent(event); reason != "" || fatal {
		return createHTMLTopicPlan{}, createHTMLTopicError(ErrInvalidCreateCommand, "created Topic introduction is invalid", elementID, eventID, false, false, errors.New(reason))
	}
	return createHTMLTopicPlan{elementID: elementID, eventID: eventID, element: element, event: event, rootBytes: rootBytes, sortBytes: sortBytes}, nil
}

func (engine *Engine) generateCreateHTMLTopicIdentities(scan elementScanResult, events []SchedulingEvent) (string, string, error) {
	eventIDs := map[string]bool{}
	for _, event := range events {
		eventIDs[event.EventID] = true
	}
	for attempt := 0; attempt < 32; attempt++ {
		elementID := newCreateHTMLTopicElementID()
		eventID := newCreateHTMLTopicEventID()
		if !ast.IsNodeIDPattern(elementID) || !ast.IsNodeIDPattern(eventID) {
			continue
		}
		if _, found := scan.Elements[elementID]; found {
			continue
		}
		if _, found := scan.Records[elementID]; found {
			continue
		}
		if _, err := os.Stat(filepath.Join(engine.config.ElementsRoot(), elementID+".sme")); err == nil || !errors.Is(err, os.ErrNotExist) {
			continue
		}
		if eventIDs[eventID] {
			continue
		}
		return elementID, eventID, nil
	}
	return "", "", createHTMLTopicError(ErrInvalidCreateCommand, "generated Topic identity collides with existing authority", "", "", false, false, nil)
}

func classifyCreateHTMLTopicWriteError(err error, elementID, eventID string, rootWritten, sortWritten bool) error {
	if rootWritten || sortWritten {
		return createHTMLTopicError(ErrElementWritePartial, "created Topic authority is partial", elementID, eventID, false, false, err)
	}
	return createHTMLTopicError(ErrDurableWriteFailed, "created Topic authority could not be written", elementID, eventID, false, false, err)
}

func createHTMLTopicError(code ErrorCode, message, elementID, eventID string, createAccepted, reviewAccepted bool, cause error) *DomainError {
	err := &DomainError{
		Code:           code,
		Message:        message,
		Retryable:      false,
		CreateAccepted: createAccepted,
		ReviewAccepted: reviewAccepted,
		ElementID:      elementID,
		EventID:        eventID,
		Cause:          cause,
	}
	if reviewAccepted {
		err.AcceptedEventID = eventID
	}
	return err
}

func (engine *Engine) createdHTMLTopicSummary(elementID, eventID string) (*CreatedTopicSummary, error) {
	element, err := engine.index.element(elementID)
	if err != nil {
		return nil, err
	}
	nodes, err := engine.index.tree()
	if err != nil {
		return nil, err
	}
	node, ok := projectedTreeNode(nodes, elementID)
	if !ok {
		return nil, errProjectionNotFound
	}
	projection, err := engine.index.projection(elementID)
	if err != nil {
		return nil, err
	}
	summary := &CreatedTopicSummary{
		ElementID:             elementID,
		Title:                 element.Title,
		ProcessingState:       element.ProcessingState,
		SourcePath:            node.SourcePath,
		SortRank:              node.SortRank,
		LifecycleState:        projection.LifecycleState,
		ScheduleProfile:       projection.ScheduleProfile,
		AcceptedReviewAction:  projection.AcceptedReviewAction,
		PriorityPosition:      &projection.PriorityPosition,
		DueLearningDay:        projection.DueLearningDay,
		InitialIntervalDays:   projection.IntervalDays,
		AdoptedTerminalEvent:  eventID,
		CleaningPolicyVersion: element.Payload.Material.CleaningPolicyVersion,
		DueAt:                 &projection.DueAt,
	}
	return summary, nil
}
