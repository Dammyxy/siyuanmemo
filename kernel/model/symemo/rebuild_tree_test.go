// SiYuan - Refactor your thinking
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package symemo

import (
	"encoding/json"
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
