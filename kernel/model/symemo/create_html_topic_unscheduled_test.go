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
	"path/filepath"
	"testing"
)

func TestCreateHTMLTopicUnscheduledRootHasNoInferredLearningState(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installFixtureSchedulerConfig(t, config)
	rootID := "20260723113000-unsched"
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), rootID+".sme"), Element{
		Spec:            SupportedElementSpec,
		ID:              rootID,
		Type:            "topic",
		Title:           "Unscheduled HTML Topic",
		ProcessingState: "new",
		PayloadSpec:     SupportedPayloadSpec,
		Payload: ElementPayload{Material: &TopicMaterial{
			Kind:                  "html",
			HTML:                  `<p data-symemo-node-id="20260723113001-nodeaaa">Body</p>`,
			CleaningPolicyVersion: topicHTMLCleaningPolicyVersion,
		}},
	})
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), ".siyuan", "sort.json"), map[string]int{rootID: 4})
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	element, err := engine.Query(context.Background(), Query{Kind: QueryElement, ElementID: rootID})
	if err != nil || element.Element == nil || element.Element.ScheduleProjection != nil {
		t.Fatalf("unscheduled root detail = %#v, err=%v", element.Element, err)
	}
	tree, err := engine.Query(context.Background(), Query{Kind: QueryElementTree, RootElementID: rootID, IncludeScheduleSummary: true})
	if err != nil || len(tree.Nodes) != 1 {
		t.Fatalf("unscheduled root tree = %#v, err=%v", tree.Nodes, err)
	}
	summary := tree.Nodes[0].ScheduleSummary
	if summary == nil || summary.LifecycleState != "" || summary.DueAt != nil || summary.PriorityPosition != nil {
		t.Fatalf("unscheduled root inferred schedule = %#v", summary)
	}
	plan, err := engine.scheduler.BuildLearningPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, targets := range [][]ReviewTarget{plan.Outstanding, plan.Pending, plan.FinalDrill} {
		for _, target := range targets {
			if target.ElementID == rootID {
				t.Fatalf("unscheduled root entered learning plan: %#v", plan)
			}
		}
	}
	diagnostics, err := engine.Query(context.Background(), Query{Kind: QueryElementSourceDiagnostics, ElementID: rootID})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics.Diagnostics) != 1 || diagnostics.Diagnostics[0].Code != missingTopicInitializationCode || diagnostics.Diagnostics[0].SourcePath != rootID+".sme" {
		t.Fatalf("unscheduled diagnostics = %#v", diagnostics.Diagnostics)
	}
}
