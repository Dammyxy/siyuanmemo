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
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const (
	treeRootID      = "20260720010101-rootaaa"
	treeItemID      = "20260720010102-itemaaa"
	treeTopicID     = "20260720010103-topicaa"
	treeBlockID     = "20260720010104-blockaa"
	treeInvalidID   = "20260720010105-badblok"
	treeFutureID    = "20260720010106-futurex"
	treeConceptID   = "20260720010107-concept"
	treeChildRootID = "20260720010108-childrt"
	treeMissedID    = "20260720010109-missblk"
	treeEncryptedID = "20260720010110-encrypt"
	treeSourceBlock = "20260720123000-blockok"
	treeMissedBlock = "20260720123001-missblk"
	treeCryptoBlock = "20260720123002-encrypt"
	treeAbsentBlock = "20260720123003-absentx"
	treeUnsureBlock = "20260720123004-unsurex"
)

func TestElementSourceTypeNeutralEnvelopeAndBlockedMaterials(t *testing.T) {
	config := copyElementTreeFixtureWorkspace(t)
	scan, err := config.scanElements()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{treeRootID, treeItemID, treeTopicID, treeBlockID, treeMissedID, treeInvalidID, treeEncryptedID, treeFutureID, treeConceptID, treeChildRootID} {
		if _, ok := scan.Elements[id]; !ok {
			t.Fatalf("missing scanned Element %s", id)
		}
	}
	if scan.Elements[treeFutureID].Payload.Raw == nil || supportStatus(scan.Elements[treeFutureID]) != SupportStatusUnsupportedReadOnly {
		t.Fatalf("future Element was not retained read-only: %#v", scan.Elements[treeFutureID])
	}
	if scan.Elements[treeBlockID].Payload.Material == nil || scan.Elements[treeBlockID].Payload.Material.Kind != "siyuanBlock" || scan.Elements[treeBlockID].Payload.Material.BlockID != treeSourceBlock {
		t.Fatalf("canonical block-backed material was not decoded: %#v", scan.Elements[treeBlockID].Payload)
	}
	diagnostic := sourceDiagnosticByElement(scan.Diagnostics, treeInvalidID, materialInvalidBlock)
	if diagnostic == nil {
		t.Fatalf("invalid block reference diagnostic missing: %#v", scan.Diagnostics)
	}
	if _, ok := scan.Elements[treeInvalidID]; !ok {
		t.Fatal("invalid block reference excluded the referring Element")
	}
}

func TestElementSourceDiagnosticsFixtures(t *testing.T) {
	root := t.TempDir()
	copyTestTree(t, filepath.Join("testdata", "diagnostics"), filepath.Join(root, "elements"))
	config := Config{StorageRoot: root, IndexRoot: filepath.Join(root, "temp")}
	scan, err := config.scanElements()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		code string
		path string
	}{
		{sourceMalformedCode, "malformed.sme"},
		{sourceDuplicateCode, "20260720020101-dupeaaa.sme"},
		{sourceMissingParent, "20260720020102-missing.sme"},
	} {
		if diagnostic := sourceDiagnosticByPath(scan.Diagnostics, expected.path, expected.code); diagnostic == nil {
			t.Fatalf("missing diagnostic %s %s in %#v", expected.code, expected.path, scan.Diagnostics)
		}
	}
	if _, ok := scan.Elements["20260720020103-orphan"]; ok {
		t.Fatal("orphaned subtree entered the normal projection")
	}
}

func TestElementSourceRejectsNonIDRootAncestor(t *testing.T) {
	config := copyFixtureWorkspace(t)
	elementID := "20260720020601-badpath"
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), "not-an-id", elementID+".sme"), Element{
		Spec:            SupportedElementSpec,
		ID:              elementID,
		Type:            "topic",
		Title:           "Invalid ancestor",
		ProcessingState: "reading",
		PayloadSpec:     SupportedPayloadSpec,
		Payload:         ElementPayload{Material: &TopicMaterial{Kind: "html", HTML: "<p>invalid ancestor</p>"}},
	})
	scan, err := config.scanElements()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := scan.Elements[elementID]; ok {
		t.Fatal("Element under a non-ID root ancestor entered the projection")
	}
	wantPath := filepath.ToSlash(filepath.Join("not-an-id", elementID+".sme"))
	if diagnostic := sourceDiagnosticByPath(scan.Diagnostics, wantPath, sourceIdentityCode); diagnostic == nil {
		t.Fatalf("invalid root ancestor diagnostic missing: %#v", scan.Diagnostics)
	}
}

func TestElementTreeMixedStorageProjection(t *testing.T) {
	engine, _ := newElementTreeFixtureEngine(t)
	nodes, err := engine.index.tree()
	if err != nil {
		t.Fatal(err)
	}
	root := findTreeNode(nodes, treeRootID)
	if root == nil {
		t.Fatalf("root not found in tree: %#v", nodes)
	}
	gotOrder := childIDs(*root)
	wantOrder := []string{treeItemID, treeChildRootID, treeTopicID, treeBlockID, treeMissedID, treeInvalidID, treeEncryptedID, treeFutureID, treeConceptID}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("mixed child order = %#v, want %#v", gotOrder, wantOrder)
	}
	childRoot := findTreeNode(root.Children, treeChildRootID)
	if childRoot == nil || childRoot.StorageKind != StorageKindRootDocument || childRoot.ParentElementID != treeRootID {
		t.Fatalf("child root projection = %#v", childRoot)
	}
	future := findTreeNode(root.Children, treeFutureID)
	if future == nil || future.SupportStatus != SupportStatusUnsupportedReadOnly || future.SourceMode != SourceModeOpaque {
		t.Fatalf("future node projection = %#v", future)
	}
	invalid := findTreeNode(root.Children, treeInvalidID)
	if invalid == nil || invalid.MaterialSourceDiagnostic == nil || invalid.MaterialSourceDiagnostic.Code != materialInvalidBlock || invalid.MaterialSourceStatus != nil {
		t.Fatalf("invalid block node = %#v", invalid)
	}
	encrypted := findTreeNode(root.Children, treeEncryptedID)
	if encrypted == nil || encrypted.MaterialSourceDiagnostic != nil || encrypted.MaterialSourceStatus == nil || *encrypted.MaterialSourceStatus != MaterialSourceUnresolved {
		t.Fatalf("encrypted block node before live overlay = %#v", encrypted)
	}
}

func TestDuplicateSiblingSortRanksAreDiagnosedAndIgnored(t *testing.T) {
	config := copyElementTreeFixtureWorkspace(t)
	sortPath := filepath.Join(config.ElementsRoot(), ".siyuan", "sort.json")
	writeTestJSON(t, sortPath, map[string]int{
		treeItemID:      1,
		treeChildRootID: 1,
		treeTopicID:     0,
	})
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	diagnostics, err := engine.index.sourceDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == sourceInvalidSort {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate ranks produced %d sort diagnostics: %#v", count, diagnostics)
	}
	nodes, err := engine.index.tree()
	if err != nil {
		t.Fatal(err)
	}
	root := findTreeNode(nodes, treeRootID)
	if root == nil {
		t.Fatalf("root not found: %#v", nodes)
	}
	wantOrder := []string{treeItemID, treeTopicID, treeBlockID, treeMissedID, treeInvalidID, treeEncryptedID, treeFutureID, treeConceptID, treeChildRootID}
	if got := childIDs(*root); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("duplicate-rank fallback order = %#v, want %#v", got, wantOrder)
	}
	for _, child := range root.Children {
		if child.SortRank != nil {
			t.Fatalf("unusable sibling sort metadata survived on %s: %d", child.ElementID, *child.SortRank)
		}
	}
}

func TestInternalOnlySiblingsIgnoreSortMetadata(t *testing.T) {
	config := copyElementTreeFixtureWorkspace(t)
	if err := os.RemoveAll(filepath.Join(config.ElementsRoot(), treeRootID)); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), ".siyuan", "sort.json"), map[string]int{
		treeItemID:  2,
		treeTopicID: 1,
	})
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	nodes, err := engine.index.tree()
	if err != nil {
		t.Fatal(err)
	}
	root := findTreeNode(nodes, treeRootID)
	if root == nil {
		t.Fatalf("root not found: %#v", nodes)
	}
	got := childIDs(*root)
	if len(got) < 2 || got[0] != treeItemID || got[1] != treeTopicID {
		t.Fatalf("internal-only children were reordered by sort metadata: %#v", got)
	}
}

func TestProjectionSchemaStoresTree(t *testing.T) {
	engine, _ := newElementTreeFixtureEngine(t)
	nodes, err := engine.index.tree()
	if err != nil {
		t.Fatal(err)
	}
	if findTreeNode(nodes, treeRootID) == nil {
		t.Fatalf("projection tree did not persist Element root: %#v", nodes)
	}
}

func TestProjectionPreservesOpaqueFuturePayloadWithKnownKeys(t *testing.T) {
	config := copyFixtureWorkspace(t)
	elementID := "20260720020301-futurep"
	source := []byte(`{
  "spec": 1,
  "id": "20260720020301-futurep",
  "type": "futureKind",
  "title": "Opaque aliases",
  "processingState": "new",
  "payloadSpec": 9,
  "payload": {
    "kind": "future-shape",
    "prompt": "opaque prompt",
    "answer": "opaque answer",
    "material": {"kind": "future-material", "uri": "opaque://source"},
    "extra": {"kept": true}
  },
  "children": []
}`)
	if err := os.WriteFile(filepath.Join(config.ElementsRoot(), elementID+".sme"), source, 0644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	projected, err := engine.index.element(elementID)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(projected.Payload.Raw, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kind", "prompt", "answer", "material", "extra"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("opaque payload lost %q after projection publication: %#v", key, payload)
		}
	}
	material, ok := payload["material"].(map[string]any)
	if !ok || material["uri"] != "opaque://source" {
		t.Fatalf("opaque material fields changed after projection publication: %#v", payload["material"])
	}
}

func TestUnsupportedTopicMaterialRemainsVisibleAndOpaque(t *testing.T) {
	config := copyFixtureWorkspace(t)
	elementID := "20260720020701-futurem"
	source := []byte(`{
  "spec": 1,
  "id": "20260720020701-futurem",
  "type": "topic",
  "title": "Future material",
  "processingState": "new",
  "payloadSpec": 1,
  "payload": {
    "material": {
      "kind": "audio-v2",
      "uri": "assets/future.opus",
      "codec": "opus"
    }
  },
  "children": []
}`)
	if err := os.WriteFile(filepath.Join(config.ElementsRoot(), elementID+".sme"), source, 0644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	nodes, err := engine.index.tree()
	if err != nil {
		t.Fatal(err)
	}
	node := findTreeNode(nodes, elementID)
	if node == nil || node.MaterialSourceDiagnostic == nil || node.MaterialSourceDiagnostic.Code != sourcePayloadCode || node.MaterialSourceStatus != nil {
		t.Fatalf("unsupported Topic material projection = %#v", node)
	}
	projected, err := engine.index.element(elementID)
	if err != nil {
		t.Fatal(err)
	}
	projectedJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err = json.Unmarshal(projectedJSON, &roundTrip); err != nil {
		t.Fatal(err)
	}
	payload := roundTrip["payload"].(map[string]any)
	material := payload["material"].(map[string]any)
	if material["uri"] != "assets/future.opus" || material["codec"] != "opus" {
		t.Fatalf("unsupported Topic material lost opaque fields: %#v", material)
	}
}

func TestUnreadableElementSubtreeIsIsolated(t *testing.T) {
	config := copyFixtureWorkspace(t)
	unreadableID := "20260720020801-unreada"
	unreadablePath := filepath.Join(config.ElementsRoot(), unreadableID)
	if err := os.MkdirAll(unreadablePath, 0755); err != nil {
		t.Fatal(err)
	}
	injectedErr := errors.New("injected subtree read failure")
	scan, err := config.scanElementsWithWalker(func(root string, visit fs.WalkDirFunc) error {
		return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return visit(path, entry, walkErr)
			}
			if filepath.Clean(path) == filepath.Clean(unreadablePath) {
				if err := visit(path, entry, injectedErr); err != nil {
					return err
				}
				return fs.SkipDir
			}
			return visit(path, entry, nil)
		})
	})
	if err != nil {
		t.Fatalf("scan failed because one subtree was unreadable: %v", err)
	}
	if _, ok := scan.Elements[fixtureElementID]; !ok {
		t.Fatalf("readable sibling Element was not rebuilt: %#v", scan.Elements)
	}
	found := false
	for _, diagnostic := range scan.Diagnostics {
		if diagnostic.SourcePath == unreadableID && diagnostic.Code == sourceUnreadableCode {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("unreadable subtree diagnostic missing: %#v", scan.Diagnostics)
	}

	_, err = config.scanElementsWithWalker(func(root string, visit fs.WalkDirFunc) error {
		return visit(root, nil, injectedErr)
	})
	if !errors.Is(err, injectedErr) {
		t.Fatalf("root traversal error = %v, want injected error", err)
	}
}

func TestElementTreePreservesEnrolledConceptScheduleProfile(t *testing.T) {
	config := copyElementTreeFixtureWorkspace(t)
	unenrolledID := "20260720020501-concept"
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), unenrolledID+".sme"), Element{
		Spec:            SupportedElementSpec,
		ID:              unenrolledID,
		Type:            "concept",
		Title:           "Unenrolled Concept",
		ProcessingState: "processed",
		PayloadSpec:     SupportedPayloadSpec,
		Payload:         ElementPayload{},
	})
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	reviewData, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	var reviewFile map[string]any
	if err = json.Unmarshal(reviewData, &reviewFile); err != nil {
		t.Fatal(err)
	}
	events := reviewFile["events"].([]any)
	event := events[0].(map[string]any)
	event["eventId"] = "20260716080000-concept"
	event["elementId"] = treeConceptID
	before := event["before"].(map[string]any)
	before["elementId"] = treeConceptID
	after := event["after"].(map[string]any)
	after["elementId"] = treeConceptID
	after["adoptedTerminalEventId"] = event["eventId"]
	after["scheduleProfile"] = "topic-afactor-v1"
	after["acceptedReviewAction"] = "NextTopic"
	reviewFile["events"] = []any{event}
	writeTestJSON(t, reviewPath, reviewFile)

	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	result, err := engine.Query(t.Context(), Query{Kind: QueryElementTree, IncludeScheduleSummary: true})
	if err != nil {
		t.Fatal(err)
	}
	enrolled := findTreeNode(result.Nodes, treeConceptID)
	if enrolled == nil || enrolled.ScheduleSummary == nil || enrolled.ScheduleSummary.ScheduleProfile != "topic-afactor-v1" || enrolled.ScheduleSummary.AcceptedReviewAction != "NextTopic" || enrolled.ScheduleSummary.LifecycleState != "memorized" || enrolled.ScheduleSummary.DueAt == nil {
		t.Fatalf("enrolled Concept schedule summary = %#v", enrolled)
	}
	unenrolled := findTreeNode(result.Nodes, unenrolledID)
	if unenrolled == nil || unenrolled.ScheduleSummary == nil || unenrolled.ScheduleSummary.ScheduleProfile != "none" || unenrolled.ScheduleSummary.AcceptedReviewAction != "" || unenrolled.ScheduleSummary.LifecycleState != "" || unenrolled.ScheduleSummary.DueAt != nil {
		t.Fatalf("unenrolled Concept schedule summary = %#v", unenrolled)
	}
}

func TestElementTreePreservesUnknownScheduleProfile(t *testing.T) {
	config := copyElementTreeFixtureWorkspace(t)
	reviewPath := filepath.Join(config.ReviewsRoot(), "2026-07.smr")
	reviewData, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	var reviewFile map[string]any
	if err = json.Unmarshal(reviewData, &reviewFile); err != nil {
		t.Fatal(err)
	}
	event := reviewFile["events"].([]any)[0].(map[string]any)
	event["eventId"] = "20260716080000-future"
	event["elementId"] = treeFutureID
	event["before"].(map[string]any)["elementId"] = treeFutureID
	after := event["after"].(map[string]any)
	after["elementId"] = treeFutureID
	after["adoptedTerminalEventId"] = event["eventId"]
	after["scheduleProfile"] = "future-schedule-v2"
	after["acceptedReviewAction"] = "FutureReview"
	reviewFile["events"] = []any{event}
	writeTestJSON(t, reviewPath, reviewFile)

	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	result, err := engine.Query(t.Context(), Query{Kind: QueryElementTree, IncludeScheduleSummary: true})
	if err != nil {
		t.Fatal(err)
	}
	future := findTreeNode(result.Nodes, treeFutureID)
	if future == nil || future.ScheduleSummary == nil || future.ScheduleSummary.ScheduleProfile != "future-schedule-v2" || future.ScheduleSummary.AcceptedReviewAction != "FutureReview" || future.ScheduleSummary.LifecycleState != "memorized" || future.ScheduleSummary.DueAt == nil {
		t.Fatalf("future Element schedule summary = %#v", future)
	}
}

func TestBlockReferenceFixtureCoversDetailedLoadOutcomes(t *testing.T) {
	_, reader := newElementTreeFixtureEngine(t)
	want := map[string]MaterialSourceStatus{
		treeMissedBlock: MaterialSourceAvailable,
		treeAbsentBlock: MaterialSourceUnavailable,
		treeUnsureBlock: MaterialSourceUnresolved,
	}
	for blockID, wantStatus := range want {
		resolution, err := reader.Load(t.Context(), blockID)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Status != wantStatus {
			t.Fatalf("Load(%s) status = %s, want %s", blockID, resolution.Status, wantStatus)
		}
	}
}

func copyTestTree(t *testing.T, source, target string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0644)
	}); err != nil {
		t.Fatal(err)
	}
}

func newElementTreeFixtureEngine(t *testing.T) (*Engine, *fakeBlockReferenceReader) {
	t.Helper()
	reader := &fakeBlockReferenceReader{
		lookupResults: map[string]BlockReferenceResolution{
			treeSourceBlock: {
				BlockID:           treeSourceBlock,
				Status:            MaterialSourceAvailable,
				CurrentNotebookID: "20260720000000-movednb",
				CurrentPath:       "/moved/doc.sy",
			},
			treeCryptoBlock: {
				BlockID:           treeCryptoBlock,
				Status:            MaterialSourceAvailable,
				CurrentNotebookID: "20260720000000-cryptnb",
				CurrentPath:       "/encrypted/doc.sy",
				Encrypted:         true,
			},
		},
		loadResults: map[string]BlockReferenceResolution{
			treeSourceBlock: {BlockID: treeSourceBlock, Status: MaterialSourceAvailable, CurrentNotebookID: "20260720000000-movednb", CurrentPath: "/moved/doc.sy"},
			treeMissedBlock: {BlockID: treeMissedBlock, Status: MaterialSourceAvailable, CurrentNotebookID: "20260720000000-recover", CurrentPath: "/recovered/doc.sy"},
			treeCryptoBlock: {BlockID: treeCryptoBlock, Status: MaterialSourceAvailable, CurrentNotebookID: "20260720000000-cryptnb", CurrentPath: "/encrypted/doc.sy", Encrypted: true},
			treeAbsentBlock: {BlockID: treeAbsentBlock, Status: MaterialSourceUnavailable},
			treeUnsureBlock: {BlockID: treeUnsureBlock, Status: MaterialSourceUnresolved},
		},
	}
	config := copyElementTreeFixtureWorkspace(t)
	config.BlockReader = reader
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, reader
}

func sourceDiagnosticByElement(diagnostics []ElementSourceDiagnostic, elementID, code string) *ElementSourceDiagnostic {
	for i := range diagnostics {
		if diagnostics[i].ElementID == elementID && diagnostics[i].Code == code {
			return &diagnostics[i]
		}
	}
	return nil
}

func sourceDiagnosticByPath(diagnostics []ElementSourceDiagnostic, sourcePath, code string) *ElementSourceDiagnostic {
	for i := range diagnostics {
		if diagnostics[i].SourcePath == sourcePath && diagnostics[i].Code == code {
			return &diagnostics[i]
		}
	}
	return nil
}

func findTreeNode(nodes []ElementTreeNode, id string) *ElementTreeNode {
	for i := range nodes {
		if nodes[i].ElementID == id {
			return &nodes[i]
		}
		if node := findTreeNode(nodes[i].Children, id); node != nil {
			return node
		}
	}
	return nil
}

func childIDs(node ElementTreeNode) []string {
	ids := make([]string, 0, len(node.Children))
	for _, child := range node.Children {
		ids = append(ids, child.ElementID)
	}
	return ids
}
