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
	"fmt"
	"sync/atomic"
)

type Engine struct {
	config    Config
	index     *projectionIndex
	ledger    *SchedulingLedger
	scheduler *Scheduler
	session   *learningSession
}

func NewEngine(ctx context.Context, config Config) (*Engine, error) {
	config = config.withDefaults()
	if config.StorageRoot == "" || config.IndexRoot == "" {
		return nil, fmt.Errorf("symemo storage and index roots are required")
	}
	index, err := openProjectionIndex(config.IndexPath())
	if err != nil {
		return nil, err
	}
	effectiveConfig := config.LoadEffectiveSchedulerConfig()
	ledger := newSchedulingLedger(config, index)
	if err = ledger.Refresh(ctx); err != nil {
		index.close()
		return nil, fmt.Errorf("initialize scheduling projection: %w", err)
	}
	scheduler := newScheduler(config, index, ledger, NewFSRSV1Adapter(effectiveConfig.FSRS), NewSimpleV1Adapter())
	engine := &Engine{config: config, index: index, ledger: ledger, scheduler: scheduler}
	engine.session = newLearningSession(config, scheduler, ledger)
	return engine, nil
}

func (engine *Engine) Close() error {
	if engine == nil || engine.index == nil {
		return nil
	}
	return engine.index.close()
}

func (engine *Engine) CreateElement(context.Context, CreateElementCommand) (CreateElementResult, error) {
	return CreateElementResult{}, domainError(ErrUnsupportedOperation, "CreateElement has no variants in item-learning-core", nil)
}

func (engine *Engine) ChangeElement(context.Context, ChangeElementCommand) (ChangeElementResult, error) {
	return ChangeElementResult{}, domainError(ErrUnsupportedOperation, "ChangeElement has no variants in item-learning-core", nil)
}

func (engine *Engine) SendToNote(context.Context, SendToNoteCommand) (SendToNoteResult, error) {
	return SendToNoteResult{}, domainError(ErrUnsupportedOperation, "SendToNote has no variants in item-learning-core", nil)
}

func (engine *Engine) Query(ctx context.Context, query Query) (QueryResult, error) {
	switch query.Kind {
	case QueryElementSubset:
		if query.Subset != "due" {
			return QueryResult{}, domainError(ErrUnsupportedOperation, "only the due Element subset is available", nil)
		}
		targets, err := engine.scheduler.BuildQueue()
		if err != nil {
			return QueryResult{}, err
		}
		items := make([]ReviewTargetSummary, 0, len(targets))
		for _, target := range targets {
			items = append(items, ReviewTargetSummary{Kind: target.Kind, ElementID: target.ElementID, Prompt: target.Prompt, DueAt: target.DueAt, PriorityPosition: target.PriorityPosition})
		}
		return QueryResult{Subset: "due", Items: items}, nil
	case QueryCurrentSession:
		state := engine.session.Current()
		return QueryResult{Session: &state}, nil
	case QueryElementTree:
		nodes, err := engine.index.tree()
		if err != nil {
			return QueryResult{}, err
		}
		nodes = filterTreeRoot(nodes, query.RootElementID)
		if query.RootElementID != "" && len(nodes) == 0 {
			return QueryResult{}, domainError(ErrElementNotFound, "Element was not found", nil)
		}
		nodes = selectScheduleSummaries(nodes, query.IncludeScheduleSummary)
		nodes, err = overlayBlockReferences(ctx, engine.config.BlockReader, nodes)
		if err != nil {
			return QueryResult{}, err
		}
		return QueryResult{Nodes: nodes}, nil
	default:
		return QueryResult{}, domainError(ErrUnsupportedOperation, "unsupported Query variant", nil)
	}
}

func (engine *Engine) RunLearningAction(ctx context.Context, action LearningAction) (LearningResult, error) {
	if engine.config.ReadOnly && isSchedulingChangingAction(action.Kind) {
		return LearningResult{}, domainError(ErrUnsupportedOperation, "scheduling changes are unavailable in read-only mode", nil)
	}
	switch action.Kind {
	case ActionStart:
		state, err := engine.session.Start()
		return LearningResult{Session: &state}, err
	case ActionShowAnswer:
		state, err := engine.session.ShowAnswer(action.ElementID)
		return LearningResult{Session: &state}, err
	case ActionGradeItem:
		if action.RawGrade == nil {
			return LearningResult{}, domainError(ErrUnsupportedGrade, "raw grade is required", nil)
		}
		if !engine.config.LoadEffectiveSchedulerConfig().PersistedComplete {
			return LearningResult{}, domainError(ErrHistoryRequiresRepair, "scheduler configuration requires repair", nil)
		}
		return engine.session.Grade(ctx, action.ElementID, action.EventID, *action.RawGrade)
	default:
		return LearningResult{}, domainError(ErrUnsupportedOperation, "unsupported RunLearningAction variant", nil)
	}
}

func isSchedulingChangingAction(kind LearningActionKind) bool {
	return kind == ActionGradeItem
}

func (engine *Engine) Refresh(ctx context.Context) error { return engine.ledger.Refresh(ctx) }

var sessionSequence atomic.Uint64

func newSessionID(config Config) string {
	return fmt.Sprintf("%d-session-%06d", config.Now().In(config.Location).UnixNano(), sessionSequence.Add(1))
}
