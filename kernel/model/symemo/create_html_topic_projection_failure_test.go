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
	"os"
	"path/filepath"
	"testing"
)

func assertCreateHTMLTopicAcceptedProjectionFailure(t *testing.T) {
	t.Helper()
	engine, config := newFixtureEngine(t)
	elementID := "20260723123000-accpfai"
	eventID := "20260723123001-accpevt"
	restoreIDs := withCreateHTMLTopicNodeIDs(t, elementID, eventID)
	defer restoreIDs()
	restoreProjection := installProjectionRefreshFailure(t, engine, config)

	result, err := engine.CreateElement(context.Background(), CreateElementCommand{Kind: CreateElementAddNewTopic, AddNewTopic: AddNewTopicCommand{Title: "Accepted Projection Failure", HTML: "<p>Body</p>"}})
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrProjectionRefreshFailed || !domainErr.CreateAccepted || !domainErr.ReviewAccepted || domainErr.Retryable || domainErr.ElementID != elementID || domainErr.EventID != eventID || domainErr.AcceptedEventID != eventID || result.ElementID != elementID || result.EventID != eventID {
		t.Fatalf("accepted projection failure error=%#v result=%#v", domainErr, result)
	}
	if countEventsByID(t, config, eventID) != 1 {
		t.Fatalf("accepted event count = %d", countEventsByID(t, config, eventID))
	}
	if _, err = os.Stat(filepath.Join(config.ElementsRoot(), elementID+".sme")); err != nil {
		t.Fatalf("created root missing after accepted failure: %v", err)
	}
	if _, err = engine.Query(context.Background(), Query{Kind: QueryCurrentSession}); !hasCode(err, ErrProjectionRebuildFailed) {
		t.Fatalf("accepted projection failure did not latch Engine unavailable: %v", err)
	}

	restoreProjection()
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recovered.Close() })
	projection, err := recovered.ledger.Snapshot(elementID)
	if err != nil || projection.AdoptedTerminalID != eventID {
		t.Fatalf("recovered projection = %#v, err=%v", projection, err)
	}
	if countEventsByID(t, config, eventID) != 1 {
		t.Fatalf("recovery duplicated event %q", eventID)
	}
}
