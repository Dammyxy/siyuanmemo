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
	"testing"
)

func TestCreateHTMLTopicRebuildsEquivalentAuthorityAfterProjectionLoss(t *testing.T) {
	engine, config := newFixtureEngine(t)
	restoreIDs := withCreateHTMLTopicNodeIDs(t, "20260723110000-rebuild", "20260723110001-rebevnt")
	defer restoreIDs()

	created, err := engine.CreateElement(context.Background(), CreateElementCommand{Kind: CreateElementAddNewTopic, AddNewTopic: AddNewTopicCommand{Title: "Rebuildable", HTML: `<p data-symemo-node-id="caller">Body</p>`}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := createHTMLTopicProjectionFingerprint(t, engine, created.ElementID)
	if err != nil {
		t.Fatal(err)
	}
	beforeAuthoritySnapshot := snapshotFeature004Authority(t, config)
	beforeAuthoritySnapshot.ProjectionSources = nil
	beforeAuthority := marshalFeature004AuthoritySnapshot(t, beforeAuthoritySnapshot)
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}

	removeSQLiteFiles(config.IndexPath())
	reopened, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	after, err := createHTMLTopicProjectionFingerprint(t, reopened, created.ElementID)
	if err != nil {
		t.Fatal(err)
	}
	afterAuthoritySnapshot := snapshotFeature004Authority(t, config)
	afterAuthoritySnapshot.ProjectionSources = nil
	afterAuthority := marshalFeature004AuthoritySnapshot(t, afterAuthoritySnapshot)
	if before != after {
		t.Fatalf("projection fingerprint after rebuild = %s, want %s", after, before)
	}
	if string(afterAuthority) != string(beforeAuthority) {
		t.Fatalf("rebuild rewrote authority\nbefore=%s\nafter=%s", beforeAuthority, afterAuthority)
	}
	if countEventsByID(t, config, created.EventID) != 1 {
		t.Fatalf("created event count = %d", countEventsByID(t, config, created.EventID))
	}
}

func createHTMLTopicProjectionFingerprint(t *testing.T, engine *Engine, elementID string) (string, error) {
	t.Helper()
	element, err := engine.Query(context.Background(), Query{Kind: QueryElement, ElementID: elementID})
	if err != nil {
		return "", err
	}
	tree, err := engine.Query(context.Background(), Query{Kind: QueryElementTree, IncludeScheduleSummary: true})
	if err != nil {
		return "", err
	}
	diagnostics, err := engine.Query(context.Background(), Query{Kind: QueryElementSourceDiagnostics, ElementID: elementID})
	if err != nil {
		return "", err
	}
	projection, err := engine.ledger.Snapshot(elementID)
	if err != nil {
		return "", err
	}
	return canonicalHash(struct {
		Element     *ElementReadView
		TreeNode    ElementTreeNode
		Projection  SchedulingProjection
		Diagnostics []ElementSourceDiagnostic
	}{
		Element:     element.Element,
		TreeNode:    mustProjectedTreeNode(t, tree.Nodes, elementID),
		Projection:  projection,
		Diagnostics: diagnostics.Diagnostics,
	})
}

func mustProjectedTreeNode(t *testing.T, nodes []ElementTreeNode, elementID string) ElementTreeNode {
	t.Helper()
	node, ok := projectedTreeNode(nodes, elementID)
	if !ok {
		t.Fatalf("tree node %s not found in %#v", elementID, nodes)
	}
	return node
}
