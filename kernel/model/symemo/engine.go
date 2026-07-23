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
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

type Engine struct {
	config                        Config
	schedulingWriteMu             sync.Mutex
	projectionMu                  sync.Mutex
	schedulerConfigHash           string
	index                         *projectionIndex
	ledger                        *SchedulingLedger
	scheduler                     *Scheduler
	session                       *learningSession
	unavailable                   atomic.Bool
	beforeCreateHTMLTopicLock     func()
	onLearningActionLockContended func()
	beforeProjectionPublish       func()
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
	schedulerConfigHash, err := canonicalHash(effectiveConfig)
	if err != nil {
		index.close()
		return nil, fmt.Errorf("hash scheduler configuration: %w", err)
	}
	ledger := newSchedulingLedger(config, index)
	engine := &Engine{config: config, schedulerConfigHash: schedulerConfigHash, index: index, ledger: ledger}
	if err = engine.refreshProjectionWithConfig(ctx, effectiveConfig); err != nil {
		index.close()
		return nil, fmt.Errorf("initialize scheduling projection: %w", err)
	}
	scheduler := newScheduler(config, index, ledger, engine.refreshProjection, NewFSRSV1Adapter(effectiveConfig.FSRS), NewSimpleV1Adapter())
	engine.scheduler = scheduler
	engine.session = newLearningSession(config, scheduler, ledger, engine.refreshProjection)
	return engine, nil
}

func (engine *Engine) Close() error {
	if engine == nil || engine.index == nil {
		return nil
	}
	return engine.index.close()
}

func (engine *Engine) CreateElement(ctx context.Context, command CreateElementCommand) (CreateElementResult, error) {
	if engine.unavailable.Load() {
		return CreateElementResult{}, projectionRebuildFailedError()
	}
	switch command.Kind {
	case CreateElementAddNewTopic:
		if engine.config.ReadOnly {
			return CreateElementResult{}, domainError(ErrUnsupportedOperation, "Element creation is unavailable in read-only mode", nil)
		}
		if engine.beforeCreateHTMLTopicLock != nil {
			engine.beforeCreateHTMLTopicLock()
		}
		engine.schedulingWriteMu.Lock()
		defer engine.schedulingWriteMu.Unlock()
		if engine.unavailable.Load() {
			return CreateElementResult{}, projectionRebuildFailedError()
		}
		return engine.createHTMLTopic(ctx, command.AddNewTopic)
	default:
		return CreateElementResult{}, domainError(ErrUnsupportedOperation, "unsupported CreateElement variant", nil)
	}
}

func (engine *Engine) ChangeElement(context.Context, ChangeElementCommand) (ChangeElementResult, error) {
	return ChangeElementResult{}, domainError(ErrUnsupportedOperation, "ChangeElement has no variants in item-learning-core", nil)
}

func (engine *Engine) SendToNote(context.Context, SendToNoteCommand) (SendToNoteResult, error) {
	return SendToNoteResult{}, domainError(ErrUnsupportedOperation, "SendToNote has no variants in item-learning-core", nil)
}

func (engine *Engine) Query(ctx context.Context, query Query) (QueryResult, error) {
	if engine.unavailable.Load() {
		return QueryResult{}, projectionRebuildFailedError()
	}
	switch query.Kind {
	case QueryElementSubset:
		if query.Subset != "due" {
			return QueryResult{}, domainError(ErrUnsupportedOperation, "only the due Element subset is available", nil)
		}
		engine.schedulingWriteMu.Lock()
		defer engine.schedulingWriteMu.Unlock()
		if engine.unavailable.Load() {
			return QueryResult{}, projectionRebuildFailedError()
		}
		targets, err := engine.scheduler.BuildQueue(ctx)
		if err != nil {
			return QueryResult{}, err
		}
		items := make([]ReviewTargetSummary, 0, len(targets))
		for _, target := range targets {
			items = append(items, ReviewTargetSummary{Kind: target.Kind, ElementID: target.ElementID, Prompt: target.Prompt, DueAt: target.DueAt, DueLearningDay: target.DueLearningDay, PriorityPosition: target.PriorityPosition, LearningDayID: target.LearningDayID})
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
	case QueryElement:
		element, err := engine.index.element(query.ElementID)
		if errors.Is(err, errProjectionNotFound) {
			diagnostics, diagnosticsErr := engine.index.sourceDiagnostics()
			if diagnosticsErr != nil {
				return QueryResult{}, diagnosticsErr
			}
			switch diagnosedElementSourceCode(query.ElementID, diagnostics) {
			case ErrElementSourceAmbiguous:
				return QueryResult{}, domainError(ErrElementSourceAmbiguous, "Element source is ambiguous", nil)
			case ErrElementSourceUnavailable:
				return QueryResult{}, domainError(ErrElementSourceUnavailable, "Element source is unavailable", nil)
			default:
				return QueryResult{}, domainError(ErrElementNotFound, "Element was not found", nil)
			}
		}
		if err != nil {
			return QueryResult{}, err
		}
		nodes, err := engine.index.tree()
		if err != nil {
			return QueryResult{}, err
		}
		node, ok := projectedTreeNode(nodes, query.ElementID)
		if !ok {
			return QueryResult{}, errProjectionNotFound
		}
		view := elementReadView(element, node)
		projection, projectionErr := engine.index.projection(query.ElementID)
		if projectionErr == nil {
			view.ScheduleProjection = &projection
		} else if !errors.Is(projectionErr, errProjectionNotFound) {
			return QueryResult{}, projectionErr
		}
		view, err = overlayElementBlockReference(ctx, engine.config.BlockReader, view)
		if err != nil {
			return QueryResult{}, err
		}
		return QueryResult{Element: &view}, nil
	case QueryElementSourceDiagnostics:
		diagnostics, err := engine.index.sourceDiagnostics()
		if err != nil {
			return QueryResult{}, err
		}
		return QueryResult{Diagnostics: filterSourceDiagnostics(diagnostics, query.ElementID, query.SourcePath)}, nil
	default:
		return QueryResult{}, domainError(ErrUnsupportedOperation, "unsupported Query variant", nil)
	}
}

func (engine *Engine) RunLearningAction(ctx context.Context, action LearningAction) (LearningResult, error) {
	if engine.unavailable.Load() {
		return LearningResult{}, projectionRebuildFailedError()
	}
	if !engine.schedulingWriteMu.TryLock() {
		if engine.onLearningActionLockContended != nil {
			engine.onLearningActionLockContended()
		}
		engine.schedulingWriteMu.Lock()
	}
	defer engine.schedulingWriteMu.Unlock()
	if engine.unavailable.Load() {
		return LearningResult{}, projectionRebuildFailedError()
	}
	if engine.config.ReadOnly && isSchedulingChangingAction(action.Kind) {
		return LearningResult{}, domainError(ErrUnsupportedOperation, "scheduling changes are unavailable in read-only mode", nil)
	}
	switch action.Kind {
	case ActionStart:
		state, err := engine.session.Start(ctx)
		return LearningResult{Session: &state}, err
	case ActionShowAnswer:
		state, err := engine.session.ShowAnswer(action.ElementID)
		return LearningResult{Session: &state}, err
	case ActionGradeItem:
		if action.RawGrade == nil {
			return LearningResult{}, domainError(ErrUnsupportedGrade, "raw grade is required", nil)
		}
		if !engine.schedulerConfigIsCurrent() {
			return LearningResult{}, domainError(ErrHistoryRequiresRepair, "scheduler configuration requires repair", nil)
		}
		result, err := engine.session.Grade(ctx, action.ElementID, action.EventID, *action.RawGrade)
		if domainErr, ok := AsDomainError(err); ok && domainErr.Code == ErrProjectionRefreshFailed {
			engine.unavailable.Store(true)
		}
		return result, err
	case ActionNextTopic:
		if !engine.schedulerConfigIsCurrent() {
			return LearningResult{}, domainError(ErrHistoryRequiresRepair, "scheduler configuration requires repair", nil)
		}
		result, err := engine.session.NextTopic(ctx, action.ElementID, action.EventID)
		if domainErr, ok := AsDomainError(err); ok && domainErr.Code == ErrProjectionRefreshFailed {
			engine.unavailable.Store(true)
		}
		return result, err
	case ActionAcceptStageTransition:
		state, err := engine.session.AcceptStage(ctx, action.Stage)
		return LearningResult{Session: &state}, err
	case ActionDeclineStageTransition:
		state, err := engine.session.DeclineStage(ctx, action.Stage)
		return LearningResult{Session: &state}, err
	case ActionGradeDrill:
		if action.RawGrade == nil {
			return LearningResult{}, domainError(ErrUnsupportedGrade, "raw grade is required", nil)
		}
		if !engine.schedulerConfigIsCurrent() {
			return LearningResult{}, domainError(ErrHistoryRequiresRepair, "scheduler configuration requires repair", nil)
		}
		result, err := engine.session.GradeDrill(ctx, action.ElementID, action.EventID, *action.RawGrade)
		if domainErr, ok := AsDomainError(err); ok && domainErr.Code == ErrProjectionRefreshFailed {
			engine.unavailable.Store(true)
		}
		return result, err
	case ActionStop:
		state := engine.session.Stop()
		return LearningResult{Session: &state}, nil
	default:
		return LearningResult{}, domainError(ErrUnsupportedOperation, "unsupported RunLearningAction variant", nil)
	}
}

func (engine *Engine) schedulerConfigIsCurrent() bool {
	effectiveConfig := engine.config.LoadEffectiveSchedulerConfig()
	if !effectiveConfig.PersistedComplete {
		return false
	}
	hash, err := canonicalHash(effectiveConfig)
	return err == nil && hash == engine.schedulerConfigHash
}

func projectionRebuildFailedError() error {
	return domainError(ErrProjectionRebuildFailed, "Element projection rebuild failed", nil)
}

func isSchedulingChangingAction(kind LearningActionKind) bool {
	return kind == ActionGradeItem || kind == ActionNextTopic || kind == ActionGradeDrill
}

func (engine *Engine) refreshProjection(ctx context.Context) error {
	err := engine.refreshProjectionWithConfig(ctx, engine.config.LoadEffectiveSchedulerConfig())
	if err != nil {
		engine.unavailable.Store(true)
	}
	return err
}

func (engine *Engine) refreshProjectionWithConfig(ctx context.Context, effectiveConfig EffectiveSchedulerConfig) error {
	engine.projectionMu.Lock()
	defer engine.projectionMu.Unlock()
	previous, err := engine.index.snapshot()
	if err != nil {
		previous = projectionSnapshot{Projections: map[string]SchedulingProjection{}, FinalDrillProjections: map[string]FinalDrillProjection{}}
	}
	scheduling, err := engine.ledger.Refresh(ctx)
	if err != nil {
		return err
	}
	for elementID, projection := range previous.Projections {
		terminalMissing := projection.AdoptedTerminalID != "" && !scheduling.HistoryEventIDs[projection.AdoptedTerminalID]
		if terminalMissing || (projection.AdoptedTerminalID != "" && !scheduling.HistoryElementIDs[elementID]) {
			return domainError(ErrHistoryRequiresRepair, "required raw scheduling history is missing", nil)
		}
	}
	for _, projection := range previous.FinalDrillProjections {
		admissionMissing := projection.AdmissionEventID != "" && !scheduling.HistoryEventIDs[projection.AdmissionEventID]
		terminalMissing := projection.AdoptedTerminalEventID != "" && !scheduling.HistoryEventIDs[projection.AdoptedTerminalEventID]
		if admissionMissing || terminalMissing {
			return domainError(ErrHistoryRequiresRepair, "required raw Final Drill history is missing", nil)
		}
	}
	for fingerprint := range previous.HistoryEventFingerprints {
		if !scheduling.HistoryEventFingerprints[fingerprint] {
			return domainError(ErrHistoryRequiresRepair, "required raw scheduling history is missing", nil)
		}
	}
	elementScan, err := engine.config.scanElements()
	if err != nil {
		return err
	}
	sourceDiagnostics := sourceDiagnosticsWithMissingProjections(elementScan, scheduling.Projections, scheduling.HistoryElementIDs)
	if scheduling.HasEvents {
		sourceDiagnostics = append(sourceDiagnostics, effectiveConfig.Diagnostics...)
	}
	sourceDiagnostics = normalizeSourceDiagnostics(sourceDiagnostics)
	tree := buildElementTree(elementScan.Records, scheduling.Projections, true)
	historyEventFingerprints := make([]string, 0, len(scheduling.HistoryEventFingerprints))
	for fingerprint := range scheduling.HistoryEventFingerprints {
		historyEventFingerprints = append(historyEventFingerprints, fingerprint)
	}
	sort.Strings(historyEventFingerprints)
	if engine.beforeProjectionPublish != nil {
		engine.beforeProjectionPublish()
	}
	return engine.index.replaceAll(ctx, projectionBuild{
		Elements:                 elementScan.Elements,
		Tree:                     tree,
		Projections:              scheduling.Projections,
		FinalDrillProjections:    scheduling.FinalDrillProjections,
		HistoryEventFingerprints: historyEventFingerprints,
		EventDiagnostics:         scheduling.EventDiagnostics,
		SourceDiagnostics:        sourceDiagnostics,
	})
}

var sessionSequence atomic.Uint64

func newSessionID(config Config) string {
	return fmt.Sprintf("%d-session-%06d", config.Now().In(config.Location).UnixNano(), sessionSequence.Add(1))
}
