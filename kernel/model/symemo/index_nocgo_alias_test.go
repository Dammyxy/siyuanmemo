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

//go:build !cgo

package symemo

import (
	"context"
	"testing"
)

func TestNonCGOQueryResultsDoNotAliasPublishedProjection(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	restoreIDs := withCreateHTMLTopicNodeIDs(t, "20260723225000-aliasxx", "20260723225001-aliasev")
	defer restoreIDs()
	created, err := engine.CreateElement(context.Background(), CreateElementCommand{
		Kind:        CreateElementAddNewTopic,
		AddNewTopic: AddNewTopicCommand{Title: "Alias", HTML: "<p>Original</p>"},
	})
	if err != nil {
		t.Fatal(err)
	}

	firstElement, err := engine.Query(context.Background(), Query{Kind: QueryElement, ElementID: created.ElementID})
	if err != nil || firstElement.Element == nil || firstElement.Element.Payload.Material == nil {
		t.Fatalf("first Element query = %#v, err=%v", firstElement.Element, err)
	}
	originalHTML := firstElement.Element.Payload.Material.HTML
	firstElement.Element.Payload.Material.HTML = "mutated"
	secondElement, err := engine.Query(context.Background(), Query{Kind: QueryElement, ElementID: created.ElementID})
	if err != nil || secondElement.Element == nil || secondElement.Element.Payload.Material == nil || secondElement.Element.Payload.Material.HTML != originalHTML {
		t.Fatalf("mutated Element query polluted projection: %#v, err=%v", secondElement.Element, err)
	}

	firstTree, err := engine.Query(context.Background(), Query{Kind: QueryElementTree})
	if err != nil {
		t.Fatal(err)
	}
	createdNode, ok := projectedTreeNode(firstTree.Nodes, created.ElementID)
	if !ok || createdNode.SortRank == nil {
		t.Fatalf("created tree node = %#v", createdNode)
	}
	originalRank := *createdNode.SortRank
	*createdNode.SortRank = originalRank + 1000
	secondTree, err := engine.Query(context.Background(), Query{Kind: QueryElementTree})
	if err != nil {
		t.Fatal(err)
	}
	createdNode, ok = projectedTreeNode(secondTree.Nodes, created.ElementID)
	if !ok || createdNode.SortRank == nil || *createdNode.SortRank != originalRank {
		t.Fatalf("mutated tree query polluted projection: %#v", createdNode)
	}
}
