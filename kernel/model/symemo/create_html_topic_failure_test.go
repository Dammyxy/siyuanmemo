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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateHTMLTopicPartialAuthorityFailuresExposeActualState(t *testing.T) {
	for _, test := range []struct {
		name      string
		stage     string
		wantRoot  bool
		wantSort  bool
		elementID string
		eventID   string
	}{
		{name: "after root", stage: "root", wantRoot: true, elementID: "20260723120000-rootflt", eventID: "20260723120001-rootfev"},
		{name: "after sort", stage: "sort", wantRoot: true, wantSort: true, elementID: "20260723120002-sortflt", eventID: "20260723120003-sortfev"},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, config := newFixtureEngine(t)
			restoreIDs := withCreateHTMLTopicNodeIDs(t, test.elementID, test.eventID)
			defer restoreIDs()
			restoreFault := withCreateHTMLTopicAuthorityFault(t, test.stage, errors.New("injected "+test.stage+" failure"))
			defer restoreFault()

			result, err := engine.CreateElement(context.Background(), CreateElementCommand{Kind: CreateElementAddNewTopic, AddNewTopic: AddNewTopicCommand{Title: "Partial", HTML: "<p>Body</p>"}})
			domainErr, ok := AsDomainError(err)
			if !ok || domainErr.Code != ErrElementWritePartial || domainErr.CreateAccepted || domainErr.ReviewAccepted || domainErr.Retryable || domainErr.ElementID != test.elementID || domainErr.EventID != test.eventID || result.ElementID != test.elementID || result.EventID != test.eventID {
				t.Fatalf("partial failure error=%#v result=%#v", domainErr, result)
			}
			if _, queryErr := engine.Query(context.Background(), Query{Kind: QueryCurrentSession}); !hasCode(queryErr, ErrProjectionRebuildFailed) {
				t.Fatalf("partial failure did not latch Engine unavailable: %v", queryErr)
			}
			if _, statErr := os.Stat(filepath.Join(config.ElementsRoot(), test.elementID+".sme")); (statErr == nil) != test.wantRoot {
				t.Fatalf("root exists=%v, want %v, err=%v", statErr == nil, test.wantRoot, statErr)
			}
			sortRanks, sortDiagnostics := config.loadSortRanks()
			if len(sortDiagnostics) != 0 {
				t.Fatalf("sort diagnostics = %#v", sortDiagnostics)
			}
			_, sorted := sortRanks[test.elementID]
			if sorted != test.wantSort {
				t.Fatalf("sort rank exists=%v, want %v: %#v", sorted, test.wantSort, sortRanks)
			}
			if countEventsByID(t, config, test.eventID) != 0 {
				t.Fatalf("partial failure event was accepted")
			}
		})
	}
}

func TestCreateHTMLTopicInvalidCommandLeavesAuthorityUnchanged(t *testing.T) {
	engine, config := newFixtureEngine(t)
	before := snapshotFeature004Authority(t, config)
	before.ProjectionSources = nil

	_, err := engine.CreateElement(context.Background(), CreateElementCommand{Kind: CreateElementAddNewTopic, AddNewTopic: AddNewTopicCommand{Title: "Unsafe", HTML: "<script>bad()</script>"}})
	if !hasCode(err, ErrInvalidCreateCommand) {
		t.Fatalf("invalid create error = %v", err)
	}
	after := snapshotFeature004Authority(t, config)
	after.ProjectionSources = nil
	if string(marshalFeature004AuthoritySnapshot(t, after)) != string(marshalFeature004AuthoritySnapshot(t, before)) {
		t.Fatalf("invalid create changed authority")
	}
}

func TestCreateHTMLTopicZeroWriteFailureDoesNotLatchEngine(t *testing.T) {
	engine, config := newFixtureEngine(t)
	restoreIDs := withCreateHTMLTopicNodeIDs(t, "20260723224000-zeroerr", "20260723224001-zeroevt")
	defer restoreIDs()
	restoreFault := withCreateHTMLTopicAuthorityFault(t, "before-root", errors.New("injected pre-write failure"))
	defer restoreFault()

	result, err := engine.CreateElement(context.Background(), CreateElementCommand{
		Kind:        CreateElementAddNewTopic,
		AddNewTopic: AddNewTopicCommand{Title: "No write", HTML: "<p>Body</p>"},
	})
	if !hasCode(err, ErrDurableWriteFailed) || result.CreateAccepted || result.ReviewAccepted {
		t.Fatalf("zero-write failure = %#v, result=%#v", err, result)
	}
	if _, err = engine.Query(context.Background(), Query{Kind: QueryCurrentSession}); err != nil {
		t.Fatalf("zero-write failure latched Engine: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(config.ElementsRoot(), result.ElementID+".sme")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("zero-write failure left root source: %v", statErr)
	}
}

func TestCreateHTMLTopicIndeterminateRootWriteFailsClosed(t *testing.T) {
	engine, config := newFixtureEngine(t)
	restoreIDs := withCreateHTMLTopicNodeIDs(t, "20260723233000-staterr", "20260723233001-statevt")
	defer restoreIDs()
	restoreFault := withCreateHTMLTopicAuthorityFault(t, "root", errors.New("injected post-write failure"))
	defer restoreFault()
	previousStat := statCreateHTMLTopicRoot
	statCreateHTMLTopicRoot = func(string) (os.FileInfo, error) { return nil, os.ErrPermission }
	t.Cleanup(func() { statCreateHTMLTopicRoot = previousStat })

	result, err := engine.CreateElement(context.Background(), CreateElementCommand{
		Kind:        CreateElementAddNewTopic,
		AddNewTopic: AddNewTopicCommand{Title: "Unknown write", HTML: "<p>Body</p>"},
	})
	if !hasCode(err, ErrElementWritePartial) || result.CreateAccepted || result.ReviewAccepted {
		t.Fatalf("indeterminate root write = %#v, result=%#v", err, result)
	}
	if _, statErr := os.Stat(filepath.Join(config.ElementsRoot(), result.ElementID+".sme")); statErr != nil {
		t.Fatalf("injected root write did not reach authority: %v", statErr)
	}
	if _, queryErr := engine.Query(context.Background(), Query{Kind: QueryCurrentSession}); !hasCode(queryErr, ErrProjectionRebuildFailed) {
		t.Fatalf("indeterminate root write left Engine available: %v", queryErr)
	}
}

func TestCreateHTMLTopicSerializationFailureBeforeEventEnvelopeLeavesAuthorityUnchanged(t *testing.T) {
	engine, config := newFixtureEngine(t)
	before := snapshotFeature004Authority(t, config)
	previousMarshal := marshalCreateHTMLTopicAuthorityJSON
	marshalCalls := 0
	marshalCreateHTMLTopicAuthorityJSON = func(value any, prefix, indent string) ([]byte, error) {
		marshalCalls++
		if marshalCalls == 3 {
			return nil, errors.New("injected event envelope serialization failure")
		}
		return json.MarshalIndent(value, prefix, indent)
	}
	t.Cleanup(func() { marshalCreateHTMLTopicAuthorityJSON = previousMarshal })

	_, err := engine.CreateElement(context.Background(), CreateElementCommand{
		Kind:        CreateElementAddNewTopic,
		AddNewTopic: AddNewTopicCommand{Title: "Serialization", HTML: "<p>Body</p>"},
	})
	if !hasCode(err, ErrInvalidCreateCommand) {
		t.Fatalf("serialization error = %v", err)
	}
	after := snapshotFeature004Authority(t, config)
	if string(marshalFeature004AuthoritySnapshot(t, after)) != string(marshalFeature004AuthoritySnapshot(t, before)) {
		t.Fatal("serialization failure changed authority or projection")
	}
}
