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
	"math/rand"
	"path/filepath"
	"testing"
)

func TestElementProjectionIsIndependentOfFilesystemEnumerationOrder(t *testing.T) {
	root := t.TempDir()
	config := Config{StorageRoot: root, IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	copyTestTree(t, filepath.Join("testdata", "diagnostics"), config.ElementsRoot())
	secondOrphanID := "20260720020105-orphanb"
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), "20260720020102-missing", secondOrphanID+".sme"), Element{
		Spec:            SupportedElementSpec,
		ID:              secondOrphanID,
		Type:            "topic",
		ProcessingState: "reading",
		PayloadSpec:     SupportedPayloadSpec,
		Payload:         ElementPayload{Material: &TopicMaterial{Kind: "html", HTML: "<p>orphan</p>"}},
	})
	wantMissingParents := map[string]int{
		"20260720020102-missing.sme":                        1,
		"20260720020204-parenta.sme":                        1,
		"20260720020204-parenta/20260720020205-parentb.sme": 1,
	}

	var expected []byte
	for seed := int64(0); seed < 20; seed++ {
		scan, err := config.scanElementsWithWalker(permutedElementWalker(t, seed))
		if err != nil {
			t.Fatal(err)
		}
		missingParents := map[string]int{}
		for _, diagnostic := range scan.Diagnostics {
			if diagnostic.Code == sourceMissingParent {
				missingParents[diagnostic.SourcePath]++
			}
		}
		for path, want := range wantMissingParents {
			if missingParents[path] != want {
				t.Fatalf("permutation %d missing-parent diagnostics[%s] = %d, want %d: %#v", seed, path, missingParents[path], want, scan.Diagnostics)
			}
		}
		if len(missingParents) != len(wantMissingParents) {
			t.Fatalf("permutation %d missing-parent paths = %#v, want %#v", seed, missingParents, wantMissingParents)
		}
		projection := struct {
			Tree        []ElementTreeNode         `json:"tree"`
			Diagnostics []ElementSourceDiagnostic `json:"diagnostics"`
		}{Tree: buildElementTree(scan.Records, nil, true), Diagnostics: scan.Diagnostics}
		actual, err := json.Marshal(projection)
		if err != nil {
			t.Fatal(err)
		}
		if seed == 0 {
			expected = actual
			continue
		}
		if string(actual) != string(expected) {
			t.Fatalf("projection differs for filesystem permutation %d\nactual=%s\nexpected=%s", seed, actual, expected)
		}
	}
}

func permutedElementWalker(t *testing.T, seed int64) func(string, fs.WalkDirFunc) error {
	t.Helper()
	return func(root string, visit fs.WalkDirFunc) error {
		type entryAtPath struct {
			path  string
			entry fs.DirEntry
		}
		var files []entryAtPath
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				files = append(files, entryAtPath{path: path, entry: entry})
			}
			return nil
		}); err != nil {
			return err
		}
		rand.New(rand.NewSource(seed)).Shuffle(len(files), func(i, j int) {
			files[i], files[j] = files[j], files[i]
		})
		for _, file := range files {
			if err := visit(file.path, file.entry, nil); err != nil {
				return err
			}
		}
		return nil
	}
}
