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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const fixtureElementID = "20260719010101-abcdefg"

func copyFixtureWorkspace(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join("testdata")
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if skipBaselineFixturePath(relative) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(root, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, time.July, 19, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	return Config{
		StorageRoot:   root,
		IndexRoot:     filepath.Join(root, "temp", "siyuanmemo"),
		SchedulerRoot: filepath.Join(root, "scheduler"),
		Now:           func() time.Time { return clock },
		Location:      time.FixedZone("CST", 8*60*60),
	}
}

func skipBaselineFixturePath(relative string) bool {
	relative = filepath.ToSlash(relative)
	if relative == "." || relative == "elements" {
		return false
	}
	if !strings.HasPrefix(relative, "elements/") {
		return false
	}
	return relative != "elements/"+fixtureElementID+".sme"
}

func copyElementTreeFixtureWorkspace(t *testing.T) Config {
	t.Helper()
	config := copyFixtureWorkspace(t)
	copyTestTree(t, filepath.Join("testdata", "elements"), config.ElementsRoot())
	return config
}

func newFixtureEngine(t *testing.T) (*Engine, Config) {
	t.Helper()
	config := copyFixtureWorkspace(t)
	if err := os.MkdirAll(config.SchedulerRoot, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"collection.json", "simple-v1.json", "fsrs-v1.json", "arena-v1.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", "scheduler", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(config.SchedulerRoot, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, config
}

type fakeBlockReferenceReader struct {
	lookupResults map[string]BlockReferenceResolution
	loadResults   map[string]BlockReferenceResolution
	lookupCalls   int
	loadCalls     int
	lookupIDs     []string
	loadIDs       []string
}

func (reader *fakeBlockReferenceReader) LookupMany(_ context.Context, blockIDs []string) (map[string]BlockReferenceResolution, error) {
	reader.lookupCalls++
	reader.lookupIDs = append(reader.lookupIDs, blockIDs...)
	resolved := make(map[string]BlockReferenceResolution, len(blockIDs))
	for _, id := range blockIDs {
		if resolution, ok := reader.lookupResults[id]; ok {
			resolved[id] = resolution
		} else {
			resolved[id] = BlockReferenceResolution{BlockID: id, Status: MaterialSourceUnresolved}
		}
	}
	return resolved, nil
}

func (reader *fakeBlockReferenceReader) Load(_ context.Context, blockID string) (BlockReferenceResolution, error) {
	reader.loadCalls++
	reader.loadIDs = append(reader.loadIDs, blockID)
	if resolution, ok := reader.loadResults[blockID]; ok {
		return resolution, nil
	}
	return BlockReferenceResolution{BlockID: blockID, Status: MaterialSourceUnresolved}, nil
}

func (reader *fakeBlockReferenceReader) sortedLookupIDs() []string {
	ids := append([]string(nil), reader.lookupIDs...)
	sort.Strings(ids)
	return ids
}
