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
	"strings"
	"testing"
)

func TestEngineContractClosedVariants(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	if _, err := engine.CreateElement(context.Background(), CreateElementCommand{Kind: "CreateItem"}); !hasCode(err, ErrUnsupportedOperation) {
		t.Fatalf("CreateElement error = %v", err)
	}
	if _, err := engine.ChangeElement(context.Background(), ChangeElementCommand{Kind: "RenameElement"}); !hasCode(err, ErrUnsupportedOperation) {
		t.Fatalf("ChangeElement error = %v", err)
	}
	if _, err := engine.SendToNote(context.Background(), SendToNoteCommand{Kind: "Send"}); !hasCode(err, ErrUnsupportedOperation) {
		t.Fatalf("SendToNote error = %v", err)
	}
	if _, err := engine.Query(context.Background(), Query{Kind: QueryElementSubset, Subset: "all"}); !hasCode(err, ErrUnsupportedOperation) {
		t.Fatalf("Query error = %v", err)
	}
	if _, err := engine.RunLearningAction(context.Background(), LearningAction{Kind: LearningActionKind("UnsupportedAction")}); !hasCode(err, ErrUnsupportedOperation) {
		t.Fatalf("RunLearningAction error = %v", err)
	}
}

func TestCreateElementAddNewTopicContractFields(t *testing.T) {
	engine, config := newFixtureEngine(t)
	before := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))

	result, err := engine.CreateElement(context.Background(), CreateElementCommand{
		Kind: CreateElementAddNewTopic,
		AddNewTopic: AddNewTopicCommand{
			Title: "  Contract Topic  ",
			HTML:  `<p data-symemo-node-id="caller">Body</p>`,
		},
	})
	if err != nil {
		t.Fatalf("AddNewTopic failed: %v", err)
	}
	if result.ElementID == "" || result.EventID == "" || !result.CreateAccepted || !result.ReviewAccepted || result.Retryable || result.Topic == nil {
		t.Fatalf("AddNewTopic result = %#v", result)
	}
	if result.Topic.ElementID != result.ElementID || result.Topic.Title != "Contract Topic" || result.Topic.ProcessingState != "new" || result.Topic.ScheduleProfile != topicAFactorV1ID || result.Topic.AcceptedReviewAction != "NextTopic" {
		t.Fatalf("created topic summary = %#v", result.Topic)
	}
	if result.Topic.PriorityPosition == nil || *result.Topic.PriorityPosition != 0 {
		t.Fatalf("created topic priority = %#v", result.Topic)
	}
	if string(before) == string(marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))) {
		t.Fatal("AddNewTopic did not change authority")
	}
}

func TestCreateElementInvalidAddNewTopicIsNonAcceptedAndLeavesAuthorityUnchanged(t *testing.T) {
	engine, config := newFixtureEngine(t)
	before := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))

	result, err := engine.CreateElement(context.Background(), CreateElementCommand{
		Kind:        CreateElementAddNewTopic,
		AddNewTopic: AddNewTopicCommand{Title: "  ", HTML: "<p>Body</p>"},
	})
	domainErr, ok := AsDomainError(err)
	if !ok || domainErr.Code != ErrInvalidCreateCommand || domainErr.CreateAccepted || domainErr.ReviewAccepted || domainErr.Retryable || domainErr.ElementID != "" || domainErr.EventID != "" {
		t.Fatalf("invalid AddNewTopic error = %#v result=%#v", domainErr, result)
	}
	after := marshalFeature004AuthoritySnapshot(t, snapshotFeature004Authority(t, config))
	if string(after) != string(before) {
		t.Fatalf("invalid AddNewTopic changed authority\nbefore=%s\nafter=%s", before, after)
	}
}

func TestEngineContractErrorCodesAreStableAndDistinct(t *testing.T) {
	codes := []ErrorCode{
		ErrUnsupportedOperation,
		ErrInvalidSessionPhase,
		ErrTargetMismatch,
		ErrUnsupportedGrade,
		ErrAuthoritativeElementUnavailable,
		ErrUnsupportedAlgorithmState,
		ErrInvalidAlgorithmOutput,
		ErrDurableWriteFailed,
		ErrProjectionRefreshFailed,
		ErrQueueAdvanceFailed,
		ErrHistoryRequiresRepair,
		ErrInvalidCreateCommand,
		ErrElementWritePartial,
		ErrElementNotFound,
		ErrElementSourceUnavailable,
		ErrElementSourceAmbiguous,
		ErrProjectionRebuildFailed,
	}
	seen := map[ErrorCode]bool{}
	for _, code := range codes {
		if code == "" || seen[code] {
			t.Fatalf("error code is empty or duplicated: %q", code)
		}
		seen[code] = true
		err := domainError(code, string(code), nil)
		resolved, ok := AsDomainError(err)
		if !ok || resolved.Code != code {
			t.Fatalf("error code %q does not round trip", code)
		}
	}
}

func TestEngineContractExcludesUnavailableAuthoritativeElement(t *testing.T) {
	config := copyFixtureWorkspace(t)
	path := filepath.Join(config.ElementsRoot(), fixtureElementID+".sme")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"payloadSpec": 1`, `"payloadSpec": 99`, 1))
	if err = os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	due, err := engine.Query(context.Background(), Query{Kind: QueryElementSubset, Subset: "due"})
	if err != nil || len(due.Items) != 0 {
		t.Fatalf("unavailable Item due query = %#v, err=%v", due, err)
	}
	diagnostics, err := engine.index.sourceDiagnostics()
	if err != nil || len(diagnostics) != 1 || diagnostics[0].Code != sourcePayloadCode {
		t.Fatalf("unavailable Item diagnostics = %#v, err=%v", diagnostics, err)
	}
}

func TestSymemoDoesNotImportRiffWorkflow(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		source := string(data)
		for _, forbidden := range []string{"github.com/siyuan-note/riff", "kernel/model/flashcard", "kernel/conf/flashcard"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s imports forbidden host flashcard workflow %q", entry.Name(), forbidden)
			}
		}
	}
}

func hasCode(err error, code ErrorCode) bool {
	domainErr, ok := AsDomainError(err)
	return ok && domainErr.Code == code
}
