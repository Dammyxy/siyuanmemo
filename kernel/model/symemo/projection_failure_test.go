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
	"path/filepath"
	"testing"
	"time"
)

func TestAcceptedProjectionFailureActionMatrix(t *testing.T) {
	good := 4
	tests := []struct {
		name       string
		fixture    func(*testing.T) (Config, string, LearningStage)
		action     LearningAction
		showAnswer bool
	}{
		{name: "Outstanding Item", fixture: projectionFailureOutstandingItemFixture, action: LearningAction{Kind: ActionGradeItem, RawGrade: &good}, showAnswer: true},
		{name: "Outstanding Topic", fixture: projectionFailureOutstandingTopicFixture, action: LearningAction{Kind: ActionNextTopic}},
		{name: "Pending Item", fixture: projectionFailurePendingItemFixture, action: LearningAction{Kind: ActionGradeItem, RawGrade: &good}, showAnswer: true},
		{name: "Pending Topic", fixture: projectionFailurePendingTopicFixture, action: LearningAction{Kind: ActionNextTopic}},
		{name: "Final Drill", fixture: projectionFailureDrillFixture, action: LearningAction{Kind: ActionGradeDrill, RawGrade: &good}, showAnswer: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, elementID, stage := test.fixture(t)
			action := test.action
			action.ElementID = elementID
			action.EventID = "projection-failure-" + test.name
			assertAcceptedProjectionFailure(t, config, stage, action, test.showAnswer)
		})
	}
}

func assertAcceptedProjectionFailure(t *testing.T, config Config, stage LearningStage, action LearningAction, showAnswer bool) {
	t.Helper()
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	started, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart})
	if err != nil || started.Session == nil {
		t.Fatalf("start = %#v, err=%v", started, err)
	}
	if stage != StageOutstanding {
		if started.Session.Confirmation == nil || started.Session.Confirmation.Stage != stage {
			t.Fatalf("stage confirmation = %#v", started.Session)
		}
		started, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: stage})
		if err != nil || started.Session == nil {
			t.Fatalf("accept stage = %#v, err=%v", started, err)
		}
	}
	if started.Session.Current == nil || started.Session.Current.ElementID != action.ElementID {
		t.Fatalf("current target = %#v", started.Session)
	}
	if showAnswer {
		if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: action.ElementID}); err != nil {
			t.Fatal(err)
		}
	}
	restore := installProjectionRefreshFailure(t, engine, config)
	_, err = engine.RunLearningAction(t.Context(), action)
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrProjectionRefreshFailed || !domainErr.ReviewAccepted || domainErr.AcceptedEventID != action.EventID || domainErr.Session == nil || domainErr.Session.Current == nil || domainErr.Session.Current.ElementID != action.ElementID || domainErr.Session.PendingAcceptedEventID != action.EventID {
		t.Fatalf("accepted projection failure = %#v", domainErr)
	}
	if countEventsByID(t, config, action.EventID) != 1 {
		t.Fatalf("accepted projection failure event count for %q is not one", action.EventID)
	}
	restore()
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatalf("recover Engine = %v", err)
	}
	t.Cleanup(func() { _ = recovered.Close() })
	if _, found, lookupErr := recovered.ledger.EventByID(action.EventID); lookupErr != nil || !found {
		t.Fatalf("recovered accepted event found=%v, err=%v", found, lookupErr)
	}
	if _, snapshotErr := recovered.ledger.Snapshot(action.ElementID); snapshotErr != nil {
		t.Fatalf("recovered projection = %v", snapshotErr)
	}
	if countEventsByID(t, config, action.EventID) != 1 {
		t.Fatalf("projection recovery duplicated event %q", action.EventID)
	}
}

func projectionFailureOutstandingItemFixture(t *testing.T) (Config, string, LearningStage) {
	t.Helper()
	return copyFixtureWorkspace(t), fixtureElementID, StageOutstanding
}

func projectionFailureOutstandingTopicFixture(t *testing.T) (Config, string, LearningStage) {
	t.Helper()
	config := projectionFailureBaseConfig(t)
	id := "20260719050101-duetopc"
	addDueDailyTopic(t, config, id, "Due Topic", "2026-07-18", 0)
	return config, id, StageOutstanding
}

func projectionFailurePendingItemFixture(t *testing.T) (Config, string, LearningStage) {
	t.Helper()
	config := projectionFailureBaseConfig(t)
	element := pendingItemElement("20260719050201-pnditem", "Pending Item")
	addRootElement(t, config, element)
	return config, element.ID, StagePending
}

func projectionFailurePendingTopicFixture(t *testing.T) (Config, string, LearningStage) {
	t.Helper()
	config := projectionFailureBaseConfig(t)
	element := pendingTopicElement("20260719050301-pndtopc", "Pending Topic")
	addRootElement(t, config, element)
	return config, element.ID, StagePending
}

func projectionFailureDrillFixture(t *testing.T) (Config, string, LearningStage) {
	t.Helper()
	config, ids := finalDrillFixtureConfig(t, 1, nil)
	return config, ids[0], StageFinalDrill
}

func projectionFailureBaseConfig(t *testing.T) Config {
	t.Helper()
	config := copyFixtureWorkspace(t)
	installDailyLearningConfig(t, config, "Asia/Shanghai", 4)
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	file := readEventFile(t, reviewPath)
	moveLegacyItemIntroductionDue(t, &file.Events[0], time.Date(2026, time.July, 30, 8, 0, 0, 0, config.Location))
	writeTestJSON(t, reviewPath, file)
	return config
}
