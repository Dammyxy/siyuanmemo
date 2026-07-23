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
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateHTMLTopicHasNoHostDocumentAssetHistoryOrSessionSideEffects(t *testing.T) {
	for _, test := range []struct {
		name         string
		setup        func(*testing.T)
		wantError    ErrorCode
		wantAccepted bool
	}{
		{name: "success", wantAccepted: true},
		{name: "partial", wantError: ErrElementWritePartial, setup: func(t *testing.T) {
			withCreateHTMLTopicAuthorityFault(t, "root", errors.New("side-effect audit fault"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, config := newFixtureEngine(t)
			if test.setup != nil {
				test.setup(t)
			}
			restoreIDs := withCreateHTMLTopicNodeIDs(t, "20260723130000-sidefxs", "20260723130001-sideevt")
			defer restoreIDs()
			result, err := engine.CreateElement(context.Background(), CreateElementCommand{Kind: CreateElementAddNewTopic, AddNewTopic: AddNewTopicCommand{Title: "Side Effects", HTML: `<p><img src="https://example.com/image.png" alt="remote"></p>`}})
			if test.wantError == "" {
				if err != nil || result.CreateAccepted != test.wantAccepted || result.ReviewAccepted != test.wantAccepted || result.Topic == nil {
					t.Fatalf("successful side-effect audit result=%#v, err=%v", result, err)
				}
			} else if !hasCode(err, test.wantError) || result.CreateAccepted || result.ReviewAccepted || result.Topic != nil {
				t.Fatalf("failed side-effect audit result=%#v, err=%v", result, err)
			}
			assertNoCreateHTMLTopicHostSideEffects(t, config.StorageRoot)
		})
	}
}

func assertNoCreateHTMLTopicHostSideEffects(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(relative)
		lower := strings.ToLower(slash)
		for _, forbidden := range []string{".sy", ".sya"} {
			if strings.HasSuffix(lower, forbidden) {
				t.Fatalf("create produced host document file %s", slash)
			}
		}
		for _, forbidden := range []string{"assets/", "history/", "sessions/", "sync-conflict", "conflict", "operation", "journal"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("create produced forbidden side-effect path %s", slash)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
