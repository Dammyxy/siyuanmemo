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

func TestCreateHTMLTopicAppendsTopLevelSortWithoutPriorityCoupling(t *testing.T) {
	config := copyFixtureWorkspace(t)
	installFixtureSchedulerConfig(t, config)
	first := Element{Spec: SupportedElementSpec, ID: "20260723100000-firstaa", Type: "topic", Title: "First", ProcessingState: "new", PayloadSpec: SupportedPayloadSpec, Payload: ElementPayload{Material: &TopicMaterial{Kind: "html", HTML: "<p>first</p>"}}}
	second := Element{Spec: SupportedElementSpec, ID: "20260723100001-seconda", Type: "topic", Title: "Second", ProcessingState: "new", PayloadSpec: SupportedPayloadSpec, Payload: ElementPayload{Material: &TopicMaterial{Kind: "html", HTML: "<p>second</p>"}}}
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), first.ID+".sme"), first)
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), second.ID+".sme"), second)
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), ".siyuan", "sort.json"), map[string]int{
		fixtureElementID: 1,
		first.ID:         2,
		second.ID:        7,
	})
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	restoreIDs := withCreateHTMLTopicNodeIDs(t, "20260723100002-newroot", "20260723100003-newevnt")
	defer restoreIDs()

	result, err := engine.CreateElement(context.Background(), CreateElementCommand{Kind: CreateElementAddNewTopic, AddNewTopic: AddNewTopicCommand{Title: "Ordered", HTML: "<p>body</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Topic == nil || result.Topic.SortRank == nil || *result.Topic.SortRank != 8 || result.Topic.PriorityPosition == nil || *result.Topic.PriorityPosition != 0 {
		t.Fatalf("created order summary = %#v", result.Topic)
	}
	tree, err := engine.Query(context.Background(), Query{Kind: QueryElementTree, IncludeScheduleSummary: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Nodes) != 4 {
		t.Fatalf("tree roots = %#v", tree.Nodes)
	}
	wantOrder := []string{fixtureElementID, first.ID, second.ID, result.ElementID}
	for i, want := range wantOrder {
		if tree.Nodes[i].ElementID != want {
			t.Fatalf("root order[%d] = %s, want %s: %#v", i, tree.Nodes[i].ElementID, want, tree.Nodes)
		}
	}
	if tree.Nodes[3].ParentElementID != "" || tree.Nodes[3].ScheduleSummary == nil || tree.Nodes[3].ScheduleSummary.PriorityPosition == nil || *tree.Nodes[3].ScheduleSummary.PriorityPosition != 0 {
		t.Fatalf("created root schedule summary = %#v", tree.Nodes[3])
	}
}
