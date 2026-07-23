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
