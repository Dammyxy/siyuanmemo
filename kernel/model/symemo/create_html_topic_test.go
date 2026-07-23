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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateHTMLTopicCreatesQueryableRootAndSchedule(t *testing.T) {
	engine, config := newFixtureEngine(t)
	elementID := "20260723090000-topicxx"
	eventID := "20260723090100-eventxx"
	restoreIDs := withCreateHTMLTopicNodeIDs(t, elementID, eventID)
	defer restoreIDs()

	result, err := engine.CreateElement(context.Background(), CreateElementCommand{
		Kind: CreateElementAddNewTopic,
		AddNewTopic: AddNewTopicCommand{
			Title: "  HTML Topic  ",
			HTML:  `<h2 data-symemo-node-id="caller">Heading</h2><script>bad()</script><p style="color: red; position: fixed">Body</p>`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ElementID != elementID || result.EventID != eventID || !result.CreateAccepted || !result.ReviewAccepted || result.Retryable || result.Topic == nil {
		t.Fatalf("create result = %#v", result)
	}
	if result.Topic.Title != "HTML Topic" || result.Topic.ProcessingState != "new" || result.Topic.ScheduleProfile != topicAFactorV1ID || result.Topic.AcceptedReviewAction != "NextTopic" || result.Topic.LifecycleState != "memorized" || result.Topic.InitialIntervalDays < 1 || result.Topic.InitialIntervalDays > 15 {
		t.Fatalf("created topic summary = %#v", result.Topic)
	}
	if result.Topic.PriorityPosition == nil || *result.Topic.PriorityPosition != 0 || result.Topic.CleaningPolicyVersion != topicHTMLCleaningPolicyVersion {
		t.Fatalf("created topic schedule/material summary = %#v", result.Topic)
	}

	elementResult, err := engine.Query(context.Background(), Query{Kind: QueryElement, ElementID: elementID})
	if err != nil {
		t.Fatal(err)
	}
	element := elementResult.Element
	if element == nil || element.Type != "topic" || element.Title != "HTML Topic" || element.ProcessingState != "new" || element.RootElementID != elementID || element.ParentElementID != "" || element.SourceMode != SourceModeHTML {
		t.Fatalf("created Element view = %#v", element)
	}
	material := element.Payload.Material
	if material == nil || material.Kind != "html" || material.CleaningPolicyVersion != topicHTMLCleaningPolicyVersion {
		t.Fatalf("created material = %#v", material)
	}
	normalizedHTML := feature004NodeIDPattern.ReplaceAllString(material.HTML, `data-symemo-node-id="ID"`)
	if normalizedHTML != `<h2 data-symemo-node-id="ID">Heading</h2><p data-symemo-node-id="ID" style="color: red">Body</p>` {
		t.Fatalf("cleaned HTML = %s", normalizedHTML)
	}
	if strings.Contains(material.HTML, "caller") || strings.Contains(material.HTML, "<script") || strings.Contains(material.HTML, "position:") {
		t.Fatalf("forbidden HTML survived: %s", material.HTML)
	}
	if element.ScheduleProjection == nil || element.ScheduleProjection.AdoptedTerminalID != eventID || element.ScheduleProjection.PriorityPosition != 0 || element.ScheduleProjection.AcceptedReviewAction != "NextTopic" {
		t.Fatalf("created projection = %#v", element.ScheduleProjection)
	}

	treeResult, err := engine.Query(context.Background(), Query{Kind: QueryElementTree, IncludeScheduleSummary: true})
	if err != nil {
		t.Fatal(err)
	}
	node, ok := projectedTreeNode(treeResult.Nodes, elementID)
	if !ok || node.ParentElementID != "" || len(node.Children) != 0 || node.ScheduleSummary == nil || node.ScheduleSummary.LifecycleState != "memorized" {
		t.Fatalf("created tree node = %#v", node)
	}
	if node.SortRank == nil || *node.SortRank != 0 {
		t.Fatalf("created sort rank = %#v", node.SortRank)
	}

	sourceBytes, err := os.ReadFile(filepath.Join(config.ElementsRoot(), elementID+".sme"))
	if err != nil {
		t.Fatal(err)
	}
	var source Element
	if err = json.Unmarshal(sourceBytes, &source); err != nil {
		t.Fatal(err)
	}
	if source.ID != elementID || len(source.Relations) != 0 || len(source.Children) != 0 || source.Payload.Material == nil || source.Payload.Material.HTML != material.HTML {
		t.Fatalf("created source = %#v", source)
	}
	event := eventByID(t, mustEvents(t, config), eventID)
	if event.Type != "introduceElement" || event.ReviewKind != "introduceTopic" || event.BaseEventID != "" || event.ElementID != elementID || event.After.AdoptedTerminalID != eventID {
		t.Fatalf("created event = %#v", event)
	}
}

func TestCreateHTMLTopicOneHundredCompleteFixtures(t *testing.T) {
	engine, config := newFixtureEngine(t)
	previousElementID, previousEventID := newCreateHTMLTopicElementID, newCreateHTMLTopicEventID
	elementSequence := 0
	eventSequence := 0
	newCreateHTMLTopicElementID = func() string {
		id := fmt.Sprintf("20260723160000-t%06d", elementSequence)
		elementSequence++
		return id
	}
	newCreateHTMLTopicEventID = func() string {
		id := fmt.Sprintf("20260723160000-e%06d", eventSequence)
		eventSequence++
		return id
	}
	t.Cleanup(func() {
		newCreateHTMLTopicElementID = previousElementID
		newCreateHTMLTopicEventID = previousEventID
	})

	created := make([]CreateElementResult, 0, 100)
	for fixture := 0; fixture < 100; fixture++ {
		title := fmt.Sprintf("Complete Topic %03d", fixture)
		result, err := engine.CreateElement(t.Context(), CreateElementCommand{
			Kind: CreateElementAddNewTopic,
			AddNewTopic: AddNewTopicCommand{
				Title: title,
				HTML:  fmt.Sprintf(`<section><h2>Heading %03d</h2><p style="color: #%06d">Body %03d</p></section>`, fixture, fixture, fixture),
			},
		})
		if err != nil {
			t.Fatalf("fixture %d create failed: %v", fixture, err)
		}
		if result.Topic == nil || !result.CreateAccepted || !result.ReviewAccepted || result.ElementID == "" || result.EventID == "" || result.Topic.Title != title {
			t.Fatalf("fixture %d result = %#v", fixture, result)
		}
		query, err := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: result.ElementID})
		if err != nil || query.Element == nil || query.Element.ScheduleProjection == nil || query.Element.ScheduleProjection.AdoptedTerminalID != result.EventID {
			t.Fatalf("fixture %d query = %#v, err=%v", fixture, query.Element, err)
		}
		created = append(created, result)
	}

	eventCounts := map[string]int{}
	for _, event := range mustEvents(t, config) {
		eventCounts[event.EventID]++
	}
	for fixture, result := range created {
		if eventCounts[result.EventID] != 1 {
			t.Fatalf("fixture %d event %q count = %d", fixture, result.EventID, eventCounts[result.EventID])
		}
	}
}
