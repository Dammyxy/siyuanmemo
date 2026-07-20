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
	"reflect"
	"testing"
)

func TestElementTreeQueryUsesUnifiedTreeAndOneBlockLookup(t *testing.T) {
	engine, reader := newElementTreeFixtureEngine(t)
	result, err := engine.Query(t.Context(), Query{Kind: QueryElementTree, RootElementID: treeRootID, IncludeScheduleSummary: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].ElementID != treeRootID {
		t.Fatalf("scoped tree result = %#v", result.Nodes)
	}
	root := result.Nodes[0]
	wantOrder := []string{treeItemID, treeChildRootID, treeTopicID, treeBlockID, treeMissedID, treeInvalidID, treeEncryptedID, treeFutureID, treeConceptID}
	if got := childIDs(root); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("tree order = %#v, want %#v", got, wantOrder)
	}
	block := findTreeNode(root.Children, treeBlockID)
	if block == nil || block.MaterialSourceStatus == nil || *block.MaterialSourceStatus != MaterialSourceAvailable || block.CurrentNotebookID != "20260720000000-movednb" {
		t.Fatalf("block-backed overlay = %#v", block)
	}
	missed := findTreeNode(root.Children, treeMissedID)
	if missed == nil || missed.MaterialSourceStatus == nil || *missed.MaterialSourceStatus != MaterialSourceUnresolved {
		t.Fatalf("missed block-backed overlay = %#v", missed)
	}
	invalid := findTreeNode(root.Children, treeInvalidID)
	if invalid == nil || invalid.MaterialSourceDiagnostic == nil || invalid.MaterialSourceStatus != nil {
		t.Fatalf("invalid block material should be visible but blocked: %#v", invalid)
	}
	encrypted := findTreeNode(root.Children, treeEncryptedID)
	if encrypted == nil || encrypted.MaterialSourceDiagnostic == nil || encrypted.MaterialSourceDiagnostic.Code != materialEncrypted || encrypted.MaterialSourceStatus != nil {
		t.Fatalf("encrypted block material should be visible but blocked: %#v", encrypted)
	}
	if reader.lookupCalls != 1 || !reflect.DeepEqual(reader.sortedLookupIDs(), []string{treeSourceBlock, treeMissedBlock, treeCryptoBlock}) || reader.loadCalls != 0 {
		t.Fatalf("block reader calls lookup=%d ids=%#v load=%d ids=%#v", reader.lookupCalls, reader.lookupIDs, reader.loadCalls, reader.loadIDs)
	}
}

func TestElementTreeQueryNormalizesUnavailableBatchResult(t *testing.T) {
	engine, reader := newElementTreeFixtureEngine(t)
	reader.lookupResults[treeMissedBlock] = BlockReferenceResolution{BlockID: treeMissedBlock, Status: MaterialSourceUnavailable}
	result, err := engine.Query(t.Context(), Query{Kind: QueryElementTree, RootElementID: treeRootID})
	if err != nil {
		t.Fatal(err)
	}
	missed := findTreeNode(result.Nodes, treeMissedID)
	if missed == nil || missed.MaterialSourceStatus == nil || *missed.MaterialSourceStatus != MaterialSourceUnresolved {
		t.Fatalf("tree batch absence was not normalized to unresolved: %#v", missed)
	}
	if reader.loadCalls != 0 {
		t.Fatalf("tree query attempted %d detailed loads", reader.loadCalls)
	}
}

func TestElementTreeQueryIgnoresStaleSortEntriesAndFallsBackForMissingEntries(t *testing.T) {
	config := copyElementTreeFixtureWorkspace(t)
	staleID := "20260720999999-staleid"
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), ".siyuan", "sort.json"), map[string]int{
		staleID:     0,
		treeTopicID: 1,
	})
	config.BlockReader = &fakeBlockReferenceReader{}
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	result, err := engine.Query(t.Context(), Query{Kind: QueryElementTree, RootElementID: treeRootID})
	if err != nil {
		t.Fatal(err)
	}
	if findTreeNode(result.Nodes, staleID) != nil {
		t.Fatal("stale sort entry created an Element node")
	}
	want := []string{treeTopicID, treeItemID, treeBlockID, treeMissedID, treeInvalidID, treeEncryptedID, treeFutureID, treeConceptID, treeChildRootID}
	if got := childIDs(result.Nodes[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("missing sort-entry fallback = %#v, want %#v", got, want)
	}
}

func TestElementTreeQueryHonorsScheduleSummaryFlagWithoutMutatingProjection(t *testing.T) {
	engine, _ := newElementTreeFixtureEngine(t)

	without, err := engine.Query(t.Context(), Query{Kind: QueryElementTree})
	if err != nil {
		t.Fatal(err)
	}
	if node := findTreeNode(without.Nodes, treeRootID); node == nil || treeContainsScheduleSummary(*node) {
		t.Fatalf("whole tree returned schedule summaries when omitted: %#v", node)
	}

	with, err := engine.Query(t.Context(), Query{Kind: QueryElementTree, RootElementID: treeRootID, IncludeScheduleSummary: true})
	if err != nil {
		t.Fatal(err)
	}
	if node := findTreeNode(with.Nodes, treeRootID); node == nil || node.ScheduleSummary == nil {
		t.Fatalf("scoped tree omitted requested schedule summary: %#v", node)
	}

	withoutAgain, err := engine.Query(t.Context(), Query{Kind: QueryElementTree, RootElementID: treeRootID})
	if err != nil {
		t.Fatal(err)
	}
	if node := findTreeNode(withoutAgain.Nodes, treeRootID); node == nil || treeContainsScheduleSummary(*node) {
		t.Fatalf("scoped tree returned cached schedule summaries when false: %#v", node)
	}
}

func TestElementTreeQueryRejectsMissingScopedRoot(t *testing.T) {
	engine, _ := newElementTreeFixtureEngine(t)
	result, err := engine.Query(t.Context(), Query{Kind: QueryElementTree, RootElementID: "20260720999999-missing"})
	if err == nil {
		t.Fatalf("missing scoped root returned successful result: %#v", result)
	}
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrElementNotFound || domainErr.Retryable {
		t.Fatalf("missing scoped root error = %#v", err)
	}
}

func treeContainsScheduleSummary(node ElementTreeNode) bool {
	if node.ScheduleSummary != nil {
		return true
	}
	for _, child := range node.Children {
		if treeContainsScheduleSummary(child) {
			return true
		}
	}
	return false
}
