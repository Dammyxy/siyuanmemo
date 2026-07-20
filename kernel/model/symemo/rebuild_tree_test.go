// SiYuan - Refactor your thinking
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package symemo

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchedulingLedgerDoesNotOwnElementTreeProjection(t *testing.T) {
	source, err := os.ReadFile("ledger.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"scanElements(", "buildElementTree(", "replaceAll("} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("SchedulingLedger owns Element projection operation %q", forbidden)
		}
	}
}

func TestProjectionRebuildRestoresCompleteStateWithoutChangingAuthority(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt", "schema mismatch"} {
		t.Run(damage, func(t *testing.T) {
			engine, config := newFixtureEngine(t)
			beforeProjection := publishedProjectionJSON(t, engine)
			beforeAuthority := authoritativeSymemoJSON(t, config)

			if damage == "schema mismatch" {
				writeProjectionSchemaMismatch(t, engine)
			}
			if err := engine.Close(); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "missing":
				removeSQLiteFiles(config.IndexPath())
			case "corrupt":
				removeSQLiteFiles(config.IndexPath())
				if err := os.WriteFile(config.IndexPath(), []byte("not a projection"), 0644); err != nil {
					t.Fatal(err)
				}
			}

			rebuilt, err := NewEngine(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rebuilt.Close() })
			if afterProjection := publishedProjectionJSON(t, rebuilt); string(afterProjection) != string(beforeProjection) {
				t.Fatalf("%s rebuild changed projection\nafter=%s\nbefore=%s", damage, afterProjection, beforeProjection)
			}
			if afterAuthority := authoritativeSymemoJSON(t, config); string(afterAuthority) != string(beforeAuthority) {
				t.Fatalf("%s rebuild changed authoritative bytes", damage)
			}
		})
	}
}

func TestProjectionRefreshFailurePreservesPublishedStateAndLocksEngine(t *testing.T) {
	engine, config := newFixtureEngine(t)
	before := publishedProjectionJSON(t, engine)
	installProjectionRefreshFailure(t, engine, config)

	if err := engine.refreshProjection(t.Context()); err == nil {
		t.Fatal("projection refresh succeeded despite injected publication failure")
	}
	if after := publishedProjectionJSON(t, engine); string(after) != string(before) {
		t.Fatalf("failed publication changed projection\nafter=%s\nbefore=%s", after, before)
	}
	if _, err := engine.Query(t.Context(), Query{Kind: QueryCurrentSession}); !hasCode(err, ErrProjectionRebuildFailed) {
		t.Fatalf("query after failed publication = %v", err)
	}
}

func TestProjectionImplementationsPreserveReadAndRecoverySemantics(t *testing.T) {
	const (
		absentElementID = "20260720030101-parityx"
		unsureElementID = "20260720030102-parityx"
	)
	config := copyElementTreeFixtureWorkspace(t)
	writeBlockTopicElement(t, config, absentElementID, treeAbsentBlock)
	writeBlockTopicElement(t, config, unsureElementID, treeUnsureBlock)
	reader := &fakeBlockReferenceReader{
		lookupResults: map[string]BlockReferenceResolution{
			treeSourceBlock: {BlockID: treeSourceBlock, Status: MaterialSourceAvailable, CurrentNotebookID: "20260720000000-movednb", CurrentPath: "/moved/doc.sy"},
			treeCryptoBlock: {BlockID: treeCryptoBlock, Status: MaterialSourceAvailable, CurrentNotebookID: "20260720000000-cryptnb", CurrentPath: "/encrypted/doc.sy", Encrypted: true},
		},
		loadResults: map[string]BlockReferenceResolution{
			treeSourceBlock: {BlockID: treeSourceBlock, Status: MaterialSourceAvailable, CurrentNotebookID: "20260720000000-movednb", CurrentPath: "/moved/doc.sy"},
			treeMissedBlock: {BlockID: treeMissedBlock, Status: MaterialSourceAvailable, CurrentNotebookID: "20260720000000-recover", CurrentPath: "/recovered/doc.sy"},
			treeCryptoBlock: {BlockID: treeCryptoBlock, Status: MaterialSourceAvailable, CurrentNotebookID: "20260720000000-cryptnb", CurrentPath: "/encrypted/doc.sy", Encrypted: true},
			treeAbsentBlock: {BlockID: treeAbsentBlock, Status: MaterialSourceUnavailable},
			treeUnsureBlock: {BlockID: treeUnsureBlock, Status: MaterialSourceUnresolved},
		},
	}
	config.BlockReader = reader
	engine, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	baseline := projectionRecoveryContractJSON(t, engine, absentElementID, unsureElementID)
	if got, want := fmt.Sprintf("%x", sha256.Sum256(baseline)), "01cecfef9735ccca8e32a6654e811b450a307fa0efd6d5bbd4c3536ec26a5768"; got != want {
		t.Fatalf("projection recovery contract digest = %s, want %s", got, want)
	}
	assertProjectionRecoveryContract(t, engine, absentElementID, unsureElementID)
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}
	removeSQLiteFiles(config.IndexPath())

	rebuilt, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rebuilt.Close() })
	if after := projectionRecoveryContractJSON(t, rebuilt, absentElementID, unsureElementID); string(after) != string(baseline) {
		t.Fatalf("projection rebuild changed contract\nafter=%s\nbefore=%s", after, baseline)
	}

	published := publishedProjectionJSON(t, rebuilt)
	restoreProjection := installProjectionRefreshFailure(t, rebuilt, config)
	if err = rebuilt.refreshProjection(t.Context()); err == nil {
		t.Fatal("projection refresh succeeded despite injected publication failure")
	}
	if after := publishedProjectionJSON(t, rebuilt); string(after) != string(published) {
		t.Fatalf("failed publication changed the published projection\nafter=%s\nbefore=%s", after, published)
	}
	if _, err = rebuilt.Query(t.Context(), Query{Kind: QueryElementTree}); !hasCode(err, ErrProjectionRebuildFailed) {
		t.Fatalf("query after publication failure = %v", err)
	}

	restoreProjection()
	if err = rebuilt.Close(); err != nil {
		t.Fatal(err)
	}
	retry, err := NewEngine(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retry.Close() })
	if after := projectionRecoveryContractJSON(t, retry, absentElementID, unsureElementID); string(after) != string(baseline) {
		t.Fatalf("retry after publication failure changed contract\nafter=%s\nbefore=%s", after, baseline)
	}
}

func assertProjectionRecoveryContract(t *testing.T, engine *Engine, absentElementID, unsureElementID string) {
	t.Helper()
	tree, err := engine.Query(t.Context(), Query{Kind: QueryElementTree, RootElementID: treeRootID})
	if err != nil {
		t.Fatal(err)
	}
	future := findTreeNode(tree.Nodes, treeFutureID)
	if future == nil || future.SupportStatus != SupportStatusUnsupportedReadOnly || future.SourceMode != SourceModeOpaque {
		t.Fatalf("unknown future tree node = %#v", future)
	}
	moved := findTreeNode(tree.Nodes, treeBlockID)
	if moved == nil || moved.ElementID != treeBlockID || moved.SourceNotebookID != "20260720000000-sourceb" || moved.CurrentNotebookID != "20260720000000-movednb" || moved.MaterialSourceStatus == nil || *moved.MaterialSourceStatus != MaterialSourceAvailable {
		t.Fatalf("moved Block-backed tree node = %#v", moved)
	}
	missed := findTreeNode(tree.Nodes, treeMissedID)
	if missed == nil || missed.MaterialSourceStatus == nil || *missed.MaterialSourceStatus != MaterialSourceUnresolved {
		t.Fatalf("batch miss tree node = %#v", missed)
	}

	for elementID, wantStatus := range map[string]MaterialSourceStatus{
		treeMissedID:    MaterialSourceAvailable,
		absentElementID: MaterialSourceUnavailable,
		unsureElementID: MaterialSourceUnresolved,
	} {
		result, queryErr := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: elementID})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if result.Element == nil || result.Element.MaterialSourceStatus == nil || *result.Element.MaterialSourceStatus != wantStatus {
			t.Fatalf("single-target Element %s = %#v, want %s", elementID, result.Element, wantStatus)
		}
	}
	for elementID, wantDiagnostic := range map[string]string{
		treeInvalidID:   materialInvalidBlock,
		treeEncryptedID: materialEncrypted,
	} {
		result, queryErr := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: elementID})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if result.Element == nil || result.Element.MaterialSourceStatus != nil || result.Element.MaterialSourceDiagnostic == nil || result.Element.MaterialSourceDiagnostic.Code != wantDiagnostic {
			t.Fatalf("blocked-material Element %s = %#v", elementID, result.Element)
		}
	}
	diagnostics, err := engine.Query(t.Context(), Query{Kind: QueryElementSourceDiagnostics})
	if err != nil {
		t.Fatal(err)
	}
	if sourceDiagnosticByElement(diagnostics.Diagnostics, treeInvalidID, materialInvalidBlock) == nil {
		t.Fatalf("invalid Block diagnostic missing: %#v", diagnostics.Diagnostics)
	}
}

func projectionRecoveryContractJSON(t *testing.T, engine *Engine, absentElementID, unsureElementID string) []byte {
	t.Helper()
	tree, err := engine.Query(t.Context(), Query{Kind: QueryElementTree})
	if err != nil {
		t.Fatal(err)
	}
	diagnostics, err := engine.Query(t.Context(), Query{Kind: QueryElementSourceDiagnostics})
	if err != nil {
		t.Fatal(err)
	}
	elements := make(map[string]*ElementReadView)
	for _, elementID := range []string{treeFutureID, treeBlockID, treeMissedID, absentElementID, unsureElementID, treeInvalidID, treeEncryptedID} {
		result, queryErr := engine.Query(t.Context(), Query{Kind: QueryElement, ElementID: elementID})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		elements[elementID] = result.Element
	}
	data, err := json.Marshal(struct {
		Tree        []ElementTreeNode           `json:"tree"`
		Diagnostics []ElementSourceDiagnostic   `json:"diagnostics"`
		Elements    map[string]*ElementReadView `json:"elements"`
	}{Tree: tree.Nodes, Diagnostics: diagnostics.Diagnostics, Elements: elements})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func publishedProjectionJSON(t *testing.T, engine *Engine) []byte {
	t.Helper()
	tree, err := engine.index.tree()
	if err != nil {
		t.Fatal(err)
	}
	element, err := engine.index.element(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics, err := engine.index.sourceDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := engine.ledger.Snapshot(fixtureElementID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(struct {
		Tree        []ElementTreeNode         `json:"tree"`
		Element     Element                   `json:"element"`
		Diagnostics []ElementSourceDiagnostic `json:"diagnostics"`
		Schedule    SchedulingProjection      `json:"schedule"`
	}{Tree: tree, Element: element, Diagnostics: diagnostics, Schedule: schedule})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func authoritativeSymemoJSON(t *testing.T, config Config) []byte {
	t.Helper()
	files := map[string][]byte{}
	for label, root := range map[string]string{
		"elements":  config.ElementsRoot(),
		"reviews":   config.ReviewsRoot(),
		"scheduler": config.SchedulerRoot,
	} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files[label+"/"+filepath.ToSlash(relative)], err = os.ReadFile(path)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProjectionRefreshFailureLatchesEngineUnavailable(t *testing.T) {
	engine, config := newFixtureEngine(t)
	if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	installProjectionRefreshFailure(t, engine, config)
	grade := 4
	eventID := "20260721100000-latched"
	_, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: eventID})
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrProjectionRefreshFailed || !domainErr.ReviewAccepted || domainErr.AcceptedEventID != eventID {
		t.Fatalf("accepted trigger error = %#v", err)
	}
	if _, err = engine.Query(t.Context(), Query{Kind: QueryCurrentSession}); !hasCode(err, ErrProjectionRebuildFailed) {
		t.Fatalf("query after publication failure = %v", err)
	}
	if _, err = engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); !hasCode(err, ErrProjectionRebuildFailed) {
		t.Fatalf("learning action after publication failure = %v", err)
	}
	events, err := config.LoadEventFiles()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.EventID == eventID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("accepted event count = %d", count)
	}
}
