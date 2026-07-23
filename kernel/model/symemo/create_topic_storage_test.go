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

func TestCreateTopicStoragePreflightRejectsMalformedSortAndEventAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, Config)
	}{
		{
			name: "malformed sort",
			mutate: func(t *testing.T, config Config) {
				path := filepath.Join(config.ElementsRoot(), ".siyuan", "sort.json")
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{"), 0644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "malformed event file",
			mutate: func(t *testing.T, config Config) {
				if err := os.WriteFile(filepath.Join(config.ReviewsRoot(), "2026-07.smr"), []byte("{"), 0644); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, config := newFixtureEngine(t)
			test.mutate(t, config)
			before := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))

			_, err := engine.CreateElement(context.Background(), CreateElementCommand{
				Kind:        CreateElementAddNewTopic,
				AddNewTopic: AddNewTopicCommand{Title: "Blocked", HTML: "<p>Body</p>"},
			})
			if !hasCode(err, ErrHistoryRequiresRepair) {
				t.Fatalf("preflight error = %v", err)
			}
			after := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))
			if string(after) != string(before) {
				t.Fatalf("preflight changed authority\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}

func TestCreateTopicStorageRejectsGeneratedIdentityCollisionsBeforeWriting(t *testing.T) {
	engine, config := newFixtureEngine(t)
	existingEvent := mustEvents(t, config)[0].EventID
	restore := withCreateHTMLTopicNodeIDs(t, fixtureElementID, existingEvent)
	defer restore()
	before := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))

	_, err := engine.CreateElement(context.Background(), CreateElementCommand{
		Kind:        CreateElementAddNewTopic,
		AddNewTopic: AddNewTopicCommand{Title: "Collision", HTML: "<p>Body</p>"},
	})
	if !hasCode(err, ErrInvalidCreateCommand) {
		t.Fatalf("collision error = %v", err)
	}
	after := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))
	if string(after) != string(before) {
		t.Fatalf("collision changed authority\nbefore=%s\nafter=%s", before, after)
	}
}

func TestCreateTopicStorageRejectsExhaustedTopLevelSortRankBeforeWriting(t *testing.T) {
	engine, config := newFixtureEngine(t)
	ranks, err := config.loadSortRanksForCreate()
	if err != nil {
		t.Fatal(err)
	}
	ranks[fixtureElementID] = int(^uint(0) >> 1)
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), ".siyuan", "sort.json"), ranks)
	previousElementID, previousEventID := newCreateHTMLTopicElementID, newCreateHTMLTopicEventID
	generatedIdentities := 0
	newCreateHTMLTopicElementID = func() string {
		generatedIdentities++
		return "20260723223000-overflw"
	}
	newCreateHTMLTopicEventID = func() string {
		generatedIdentities++
		return "20260723223001-overevt"
	}
	t.Cleanup(func() {
		newCreateHTMLTopicElementID = previousElementID
		newCreateHTMLTopicEventID = previousEventID
	})
	before := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))

	_, err = engine.CreateElement(context.Background(), CreateElementCommand{
		Kind:        CreateElementAddNewTopic,
		AddNewTopic: AddNewTopicCommand{Title: "Overflow", HTML: "<p>Body</p>"},
	})
	if !hasCode(err, ErrHistoryRequiresRepair) {
		t.Fatalf("exhausted sort rank error = %v", err)
	}
	if generatedIdentities != 0 {
		t.Fatalf("exhausted sort rank generated %d identities before rejection", generatedIdentities)
	}
	after := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))
	if string(after) != string(before) {
		t.Fatalf("exhausted sort rank changed authority\nbefore=%s\nafter=%s", before, after)
	}
}

func TestCreateTopicStorageReadOnlyAndSchedulerPreflightLeaveAllFilesUnchanged(t *testing.T) {
	for _, test := range []struct {
		name     string
		wantCode ErrorCode
		mutate   func(*testing.T, *Engine, Config)
	}{
		{
			name:     "read-only",
			wantCode: ErrUnsupportedOperation,
			mutate: func(_ *testing.T, engine *Engine, _ Config) {
				engine.config.ReadOnly = true
			},
		},
		{
			name:     "missing scheduler authority",
			wantCode: ErrHistoryRequiresRepair,
			mutate: func(t *testing.T, _ *Engine, config Config) {
				if err := os.Remove(filepath.Join(config.SchedulerRoot, "collection.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "invalid scheduler authority",
			wantCode: ErrHistoryRequiresRepair,
			mutate: func(t *testing.T, _ *Engine, config Config) {
				if err := os.WriteFile(filepath.Join(config.SchedulerRoot, "topic-afactor-v1.json"), []byte("{"), 0644); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, config := newFixtureEngine(t)
			test.mutate(t, engine, config)
			before := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))

			_, err := engine.CreateElement(t.Context(), CreateElementCommand{
				Kind:        CreateElementAddNewTopic,
				AddNewTopic: AddNewTopicCommand{Title: "Blocked", HTML: "<p>Body</p>"},
			})
			if !hasCode(err, test.wantCode) {
				t.Fatalf("preflight error = %v, want %s", err, test.wantCode)
			}
			after := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))
			if string(after) != string(before) {
				t.Fatalf("preflight changed files\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}

func TestValidCreatedHTMLRootWithoutInitializationIsQueryableButUnscheduled(t *testing.T) {
	config := copyFixtureWorkspace(t)
	rootID := "20260723110000-orphanx"
	writeTestJSON(t, filepath.Join(config.ElementsRoot(), rootID+".sme"), Element{
		Spec:            SupportedElementSpec,
		ID:              rootID,
		Type:            "topic",
		Title:           "Orphaned create",
		ProcessingState: "new",
		PayloadSpec:     SupportedPayloadSpec,
		Payload: ElementPayload{Material: &TopicMaterial{
			Kind:                  "html",
			HTML:                  `<p data-symemo-node-id="20260723110001-nodeaaa">Body</p>`,
			CleaningPolicyVersion: topicHTMLCleaningPolicyVersion,
		}},
	})
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	element, err := engine.Query(context.Background(), Query{Kind: QueryElement, ElementID: rootID})
	if err != nil || element.Element == nil || element.Element.ScheduleProjection != nil {
		t.Fatalf("orphaned root query = %#v, err=%v", element.Element, err)
	}
	due, err := engine.Query(context.Background(), Query{Kind: QueryElementSubset, Subset: "due"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range due.Items {
		if item.ElementID == rootID {
			t.Fatalf("orphaned root entered due queue: %#v", due.Items)
		}
	}
	diagnostics, err := engine.Query(context.Background(), Query{Kind: QueryElementSourceDiagnostics, ElementID: rootID})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics.Diagnostics) != 1 || diagnostics.Diagnostics[0].Code != missingTopicInitializationCode {
		t.Fatalf("orphaned root diagnostics = %#v", diagnostics.Diagnostics)
	}
}
