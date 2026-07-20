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
	"encoding/json"
	"os"
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

func TestElementQueryReturnsKnownAndOpaqueFutureElements(t *testing.T) {
	engine, _ := newElementTreeFixtureEngine(t)

	known, err := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: fixtureElementID})
	if err != nil {
		t.Fatal(err)
	}
	if known.Element == nil || known.Element.ID != fixtureElementID || known.Element.SupportStatus != SupportStatusSupported || known.Element.SourcePath != fixtureElementID+".sme" || known.Element.Payload.Prompt == "" || known.Element.Payload.Answer == "" || known.Element.ScheduleProjection == nil {
		t.Fatalf("known Element result = %#v", known.Element)
	}

	future, err := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: treeFutureID})
	if err != nil {
		t.Fatal(err)
	}
	if future.Element == nil || future.Element.ID != treeFutureID || future.Element.SupportStatus != SupportStatusUnsupportedReadOnly || future.Element.SourceMode != SourceModeOpaque {
		t.Fatalf("future Element result = %#v", future.Element)
	}
	data, err := json.Marshal(future.Element)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["supportStatus"] != string(SupportStatusUnsupportedReadOnly) || decoded["sourcePath"] != "20260720010101-rootaaa.sme" {
		t.Fatalf("future Element read metadata missing from JSON: %#v", decoded)
	}
	payload := decoded["payload"].(map[string]any)
	opaque, ok := payload["opaque"].(map[string]any)
	if !ok || opaque["kept"] != true {
		t.Fatalf("future Element payload lost opaque data: %#v", payload)
	}
}

func TestElementQueryReturnsNotFoundForUnknownIdentity(t *testing.T) {
	engine, _ := newElementTreeFixtureEngine(t)
	result, err := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: "20260720999999-unknown"})
	if !hasCode(err, ErrElementNotFound) || result.Element != nil {
		t.Fatalf("unknown Element result=%#v err=%v", result, err)
	}
}

func TestElementQueryUsesSingleTargetBlockResolution(t *testing.T) {
	engine, reader := newElementTreeFixtureEngine(t)
	before := snapshotPaths(t, engine.config.StorageRoot)

	moved, err := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: treeBlockID})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Element == nil || moved.Element.MaterialSourceStatus == nil || *moved.Element.MaterialSourceStatus != MaterialSourceAvailable || moved.Element.CurrentNotebookID != "20260720000000-movednb" || moved.Element.CurrentPath != "/moved/doc.sy" {
		t.Fatalf("moved Block-backed Element = %#v", moved.Element)
	}
	recovered, err := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: treeMissedID})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Element == nil || recovered.Element.MaterialSourceStatus == nil || *recovered.Element.MaterialSourceStatus != MaterialSourceAvailable || recovered.Element.CurrentNotebookID != "20260720000000-recover" {
		t.Fatalf("recovered Block-backed Element = %#v", recovered.Element)
	}
	invalid, err := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: treeInvalidID})
	if err != nil {
		t.Fatal(err)
	}
	if invalid.Element == nil || invalid.Element.MaterialSourceDiagnostic == nil || invalid.Element.MaterialSourceDiagnostic.Code != materialInvalidBlock || invalid.Element.MaterialSourceStatus != nil {
		t.Fatalf("invalid Block-backed Element = %#v", invalid.Element)
	}
	encrypted, err := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: treeEncryptedID})
	if err != nil {
		t.Fatal(err)
	}
	if encrypted.Element == nil || encrypted.Element.MaterialSourceDiagnostic == nil || encrypted.Element.MaterialSourceDiagnostic.Code != materialEncrypted || encrypted.Element.MaterialSourceStatus != nil || encrypted.Element.Payload.Material == nil || encrypted.Element.Payload.Material.HTML != "" {
		t.Fatalf("encrypted Block-backed Element = %#v", encrypted.Element)
	}
	if reader.lookupCalls != 0 || !reflect.DeepEqual(reader.loadIDs, []string{treeSourceBlock, treeMissedBlock, treeCryptoBlock}) {
		t.Fatalf("detailed resolution calls lookup=%d loadIDs=%#v", reader.lookupCalls, reader.loadIDs)
	}
	if after := snapshotPaths(t, engine.config.StorageRoot); !equalStringMaps(before, after) {
		t.Fatalf("detailed resolution changed authoritative storage\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestElementQueryPreservesUnavailableAndUnresolvedBlockStates(t *testing.T) {
	config := copyElementTreeFixtureWorkspace(t)
	absentID := "20260720030101-absentq"
	unsureID := "20260720030102-unsureq"
	writeBlockTopicElement(t, config, absentID, treeAbsentBlock)
	writeBlockTopicElement(t, config, unsureID, treeUnsureBlock)
	reader := &fakeBlockReferenceReader{loadResults: map[string]BlockReferenceResolution{
		treeAbsentBlock: {BlockID: treeAbsentBlock, Status: MaterialSourceUnavailable},
		treeUnsureBlock: {BlockID: treeUnsureBlock, Status: MaterialSourceUnresolved},
	}}
	config.BlockReader = reader
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	for elementID, wantStatus := range map[string]MaterialSourceStatus{absentID: MaterialSourceUnavailable, unsureID: MaterialSourceUnresolved} {
		result, queryErr := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: elementID})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if result.Element == nil || result.Element.MaterialSourceStatus == nil || *result.Element.MaterialSourceStatus != wantStatus || result.Element.MaterialSourceDiagnostic != nil {
			t.Fatalf("Element %s resolution = %#v, want %s", elementID, result.Element, wantStatus)
		}
	}
	if reader.lookupCalls != 0 || len(reader.loadIDs) != 2 {
		t.Fatalf("detailed state calls lookup=%d loadIDs=%#v", reader.lookupCalls, reader.loadIDs)
	}
}

func TestElementQueryClassifiesAmbiguousAndUnavailableSources(t *testing.T) {
	diagnosticEngine := newSourceDiagnosticFixtureEngine(t)
	for elementID, wantCode := range map[string]ErrorCode{
		"20260720020101-dupeaaa": ErrElementSourceAmbiguous,
		"20260720020102-missing": ErrElementSourceUnavailable,
		"20260720020202-baditem": ErrElementSourceUnavailable,
		"20260720020203-incompl": ErrElementSourceUnavailable,
	} {
		result, err := diagnosticEngine.Query(t.Context(), Query{Kind: QueryElement, ElementID: elementID})
		if !hasCode(err, wantCode) || result.Element != nil {
			t.Fatalf("Element %s result=%#v err=%v, want %s", elementID, result, err, wantCode)
		}
	}

	engine, _, missingPath, _ := newTwoItemFixtureEngine(t)
	if err := os.Remove(missingPath); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: secondFixtureElementID})
	if !hasCode(err, ErrElementSourceUnavailable) || result.Element != nil {
		t.Fatalf("historically scheduled missing Element result=%#v err=%v", result, err)
	}
}

func TestElementQueryGivesAmbiguityPrecedenceAcrossDiagnosedSources(t *testing.T) {
	root := t.TempDir()
	elementsRoot := filepath.Join(root, "elements")
	copyTestTree(t, filepath.Join("testdata", "diagnostics"), elementsRoot)
	malformedPath := filepath.Join(elementsRoot, "00000000000000-shadow", "20260720020101-dupeaaa.sme")
	if err := os.MkdirAll(filepath.Dir(malformedPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(malformedPath, []byte(`{"spec":`), 0644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(t.Context(), Config{StorageRoot: root, IndexRoot: filepath.Join(root, "temp", "siyuanmemo")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	result, err := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: "20260720020101-dupeaaa"})
	if !hasCode(err, ErrElementSourceAmbiguous) || result.Element != nil {
		t.Fatalf("ambiguous Element with another diagnosed source result=%#v err=%v", result, err)
	}
}

func TestSourceDiagnosticQueryFiltersProjectedDiagnostics(t *testing.T) {
	engine := newSourceDiagnosticFixtureEngine(t)
	all, err := engine.Query(t.Context(), Query{Kind: QueryElementSourceDiagnostics})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Diagnostics) == 0 {
		t.Fatal("diagnostic query returned no diagnostics")
	}

	byElement, err := engine.Query(t.Context(), Query{Kind: QueryElementSourceDiagnostics, ElementID: "20260720020101-dupeaaa"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byElement.Diagnostics) != 1 || byElement.Diagnostics[0].Code != sourceDuplicateCode || len(byElement.Diagnostics[0].RelatedPaths) != 2 {
		t.Fatalf("Element diagnostic filter = %#v", byElement.Diagnostics)
	}

	missingPath := "20260720020102-missing.sme"
	byPath, err := engine.Query(t.Context(), Query{Kind: QueryElementSourceDiagnostics, SourcePath: missingPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(byPath.Diagnostics) != 1 || byPath.Diagnostics[0].SourcePath != missingPath || byPath.Diagnostics[0].Code != sourceMissingParent {
		t.Fatalf("source-path diagnostic filter = %#v", byPath.Diagnostics)
	}

	none, err := engine.Query(t.Context(), Query{Kind: QueryElementSourceDiagnostics, SourcePath: "../../outside.sme"})
	if err != nil || len(none.Diagnostics) != 0 {
		t.Fatalf("unmatched safe filter result=%#v err=%v", none.Diagnostics, err)
	}
}

func TestSourceDiagnosticQueryReportsHistoricalSchedulerAuthority(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		config := copyFixtureWorkspace(t)
		if err := os.RemoveAll(config.SchedulerRoot); err != nil {
			t.Fatal(err)
		}
		engine, err := NewEngine(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = engine.Close() })
		result, err := engine.Query(t.Context(), Query{Kind: QueryElementSourceDiagnostics, SourcePath: "scheduler/collection.json"})
		if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "missing-scheduler-config" {
			t.Fatalf("missing scheduler diagnostics=%#v err=%v", result.Diagnostics, err)
		}
		if element, queryErr := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: fixtureElementID}); queryErr != nil || element.Element == nil {
			t.Fatalf("read failed with missing scheduler authority: result=%#v err=%v", element, queryErr)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		config := copyFixtureWorkspace(t)
		writeTestJSON(t, filepath.Join(config.SchedulerRoot, "simple-v1.json"), SchedulerConfig{Spec: 1, Algorithm: "wrong", IntervalRule: "item-simple-v1"})
		engine, err := NewEngine(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = engine.Close() })
		result, err := engine.Query(t.Context(), Query{Kind: QueryElementSourceDiagnostics, SourcePath: "scheduler/simple-v1.json"})
		if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "invalid-scheduler-config" {
			t.Fatalf("invalid scheduler diagnostics=%#v err=%v", result.Diagnostics, err)
		}
	})
}

func newSourceDiagnosticFixtureEngine(t *testing.T) *Engine {
	t.Helper()
	root := t.TempDir()
	copyTestTree(t, filepath.Join("testdata", "diagnostics"), filepath.Join(root, "elements"))
	engine, err := NewEngine(t.Context(), Config{StorageRoot: root, IndexRoot: filepath.Join(root, "temp", "siyuanmemo")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func writeBlockTopicElement(t *testing.T, config Config, elementID, blockID string) {
	t.Helper()
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), elementID+".sme"), Element{
		Spec:            SupportedElementSpec,
		ID:              elementID,
		Type:            "topic",
		Title:           "Block-backed Topic",
		ProcessingState: "reading",
		PayloadSpec:     SupportedPayloadSpec,
		Payload:         ElementPayload{Material: &TopicMaterial{Kind: "siyuanBlock", BlockID: blockID}},
	})
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
