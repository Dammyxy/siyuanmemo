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
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestCreateHTMLTopicLedgerCompatibilityAcrossInputPermutations(t *testing.T) {
	engine, config := newFixtureEngine(t)
	topicID := "20260723140000-permtop"
	introID := "20260723140001-permint"
	restoreIDs := withCreateHTMLTopicNodeIDs(t, topicID, introID)
	defer restoreIDs()
	if _, err := engine.CreateElement(context.Background(), CreateElementCommand{Kind: CreateElementAddNewTopic, AddNewTopic: AddNewTopicCommand{Title: "Permutation Topic", HTML: "<p>Body</p>"}}); err != nil {
		t.Fatal(err)
	}
	intro := eventByID(t, mustEvents(t, config), introID)
	target := ReviewTarget{Kind: "element.topic", ElementID: topicID, Prompt: "Permutation Topic", PriorityPosition: 0, ObservedBaseSchedulingEvent: introID, ObservedProjection: intro.After, LearningDate: intro.LearningDate, LearningDayID: intro.LearningDayID}
	engine.scheduler.config.Now = func() time.Time { return intro.OccurredAt.Add(1 * time.Hour) }
	nextA, err := engine.scheduler.ApplyTopicNext(context.Background(), target, "20260723141000-nextaaa", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	engine.scheduler.config.Now = func() time.Time { return intro.OccurredAt.Add(2 * time.Hour) }
	nextB, err := engine.scheduler.ApplyTopicNext(context.Background(), target, "20260723142000-nextbbb", "session-b")
	if err != nil {
		t.Fatal(err)
	}

	events := mustEvents(t, config)
	nextBDuplicate := eventByID(t, events, nextB.Event.EventID)
	events = append(events, nextBDuplicate)
	conflictA := intro
	conflictA.EventID = "20260723143000-conflic"
	conflictB := conflictA
	conflictB.ElementID = fixtureElementID
	conflictB.After.ElementID = fixtureElementID
	invalidBase := nextA.Event
	invalidBase.EventID = "20260723144000-badbase"
	invalidBase.BaseEventID = "missing-base"
	invalidBase.After.AdoptedTerminalID = invalidBase.EventID
	events = append(events, conflictA, conflictB, invalidBase)

	var expected string
	for iteration := 0; iteration < 20; iteration++ {
		projections, finalDrill, diagnostics := projectSchedulingTruth(permutedCreateTopicEvents(events, iteration), "2026-07-19")
		if projection := projections[topicID]; projection.AdoptedTerminalID != nextB.Event.EventID || projection.AcceptedReviewAction != "NextTopic" {
			t.Fatalf("iteration %d Topic projection = %#v diagnostics=%#v", iteration, projection, diagnostics)
		}
		if projection := projections[fixtureElementID]; projection.ElementID != fixtureElementID {
			t.Fatalf("iteration %d lost existing Item projection: %#v", iteration, projections)
		}
		if diagnostic := diagnosticByID(diagnostics, nextB.Event.EventID); diagnostic.Classification != "duplicate" || diagnostic.DuplicateCount != 2 {
			t.Fatalf("iteration %d duplicate diagnostic = %#v", iteration, diagnostic)
		}
		if diagnostic := diagnosticByID(diagnostics, conflictA.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != "conflicting-event-identity" {
			t.Fatalf("iteration %d conflict diagnostic = %#v", iteration, diagnostic)
		}
		if diagnostic := diagnosticByID(diagnostics, invalidBase.EventID); diagnostic.Classification != "invalid" || diagnostic.Reason != "missing-base" {
			t.Fatalf("iteration %d invalid-base diagnostic = %#v", iteration, diagnostic)
		}
		actual, err := canonicalHash(struct {
			Projections map[string]SchedulingProjection
			FinalDrill  map[string]FinalDrillProjection
			Diagnostics []EventDiagnostic
		}{Projections: projections, FinalDrill: finalDrill, Diagnostics: diagnostics})
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			expected = actual
			continue
		}
		if actual != expected {
			t.Fatalf("iteration %d projection hash = %s, want %s", iteration, actual, expected)
		}
	}
}

func TestCreateHTMLTopicRebuildAcrossFilesystemAndEventInputPermutations(t *testing.T) {
	sourceEngine, sourceConfig := newFixtureEngine(t)
	if _, err := sourceEngine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceEngine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	lowGrade := 2
	if _, err := sourceEngine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &lowGrade, EventID: "20260723160000-itemlow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceEngine.RunLearningAction(t.Context(), LearningAction{Kind: ActionAcceptStageTransition, Stage: StageFinalDrill}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceEngine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceEngine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeDrill, ElementID: fixtureElementID, RawGrade: &lowGrade, EventID: "20260723160001-drilllo"}); err != nil {
		t.Fatal(err)
	}

	topicID := "20260723161000-fspermt"
	introID := "20260723161001-fspermi"
	restoreIDs := withCreateHTMLTopicNodeIDs(t, topicID, introID)
	defer restoreIDs()
	if _, err := sourceEngine.CreateElement(t.Context(), CreateElementCommand{
		Kind:        CreateElementAddNewTopic,
		AddNewTopic: AddNewTopicCommand{Title: "Filesystem Permutation Topic", HTML: "<h2>Heading</h2><p>Body</p>"},
	}); err != nil {
		t.Fatal(err)
	}
	intro := eventByID(t, mustEvents(t, sourceConfig), introID)
	target := ReviewTarget{Kind: "element.topic", ElementID: topicID, Prompt: "Filesystem Permutation Topic", PriorityPosition: 0, ObservedBaseSchedulingEvent: introID, ObservedProjection: intro.After, LearningDate: intro.LearningDate, LearningDayID: intro.LearningDayID}
	sourceEngine.scheduler.config.Now = func() time.Time { return intro.OccurredAt.Add(time.Hour) }
	nextA, err := sourceEngine.scheduler.ApplyTopicNext(t.Context(), target, "20260723162000-fsnexta", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	sourceEngine.scheduler.config.Now = func() time.Time { return intro.OccurredAt.Add(2 * time.Hour) }
	nextB, err := sourceEngine.scheduler.ApplyTopicNext(t.Context(), target, "20260723163000-fsnextb", "session-b")
	if err != nil {
		t.Fatal(err)
	}

	events := mustEvents(t, sourceConfig)
	events = append(events, eventByID(t, events, nextB.Event.EventID))
	conflictA := intro
	conflictA.EventID = "20260723164000-fsconfl"
	conflictB := conflictA
	conflictB.ElementID = fixtureElementID
	conflictB.After.ElementID = fixtureElementID
	invalidBase := nextA.Event
	invalidBase.EventID = "20260723165000-fsbadba"
	invalidBase.BaseEventID = "missing-base"
	invalidBase.After.AdoptedTerminalID = invalidBase.EventID
	events = append(events, conflictA, conflictB, invalidBase)

	elementFiles := snapshotDirectoryFiles(t, sourceConfig.ElementsRoot())
	schedulerFiles := snapshotDirectoryFiles(t, sourceConfig.SchedulerRoot)
	var expectedHash string
	for iteration := 0; iteration < 20; iteration++ {
		root := t.TempDir()
		authority := map[string][]byte{}
		for relative, data := range elementFiles {
			authority[filepath.ToSlash(filepath.Join("elements", relative))] = data
		}
		for relative, data := range schedulerFiles {
			authority[filepath.ToSlash(filepath.Join("scheduler", relative))] = data
		}
		eventFile := EventFile{Spec: SupportedEventSpec, Month: "2026-07", Events: permutedCreateTopicEvents(events, iteration)}
		eventBytes, err := json.MarshalIndent(eventFile, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		authority["reviews/2026-07.smr"] = append(eventBytes, '\n')
		writePermutedCreateTopicAuthorityFiles(t, root, authority, iteration)

		config := Config{
			StorageRoot:   root,
			IndexRoot:     filepath.Join(root, "temp", "siyuanmemo"),
			SchedulerRoot: filepath.Join(root, "scheduler"),
			Now:           sourceConfig.Now,
			Location:      sourceConfig.Location,
		}
		rebuilt, err := NewEngine(t.Context(), config)
		if err != nil {
			t.Fatalf("iteration %d rebuild failed: %v", iteration, err)
		}
		snapshot, err := rebuilt.index.snapshot()
		if err != nil {
			_ = rebuilt.Close()
			t.Fatal(err)
		}
		topic := snapshot.Elements[topicID]
		topicProjection := snapshot.Projections[topicID]
		itemProjection := snapshot.Projections[fixtureElementID]
		drillProjection, drillFound := snapshot.FinalDrillProjections[fixtureElementID]
		if topic.ID != topicID || topic.Title != "Filesystem Permutation Topic" || topic.Payload.Material == nil || topic.Payload.Material.CleaningPolicyVersion != topicHTMLCleaningPolicyVersion {
			_ = rebuilt.Close()
			t.Fatalf("iteration %d Topic source = %#v", iteration, topic)
		}
		if topicProjection.AdoptedTerminalID != nextB.Event.EventID || topicProjection.AcceptedReviewAction != "NextTopic" {
			_ = rebuilt.Close()
			t.Fatalf("iteration %d Topic projection = %#v", iteration, topicProjection)
		}
		if itemProjection.ElementID != fixtureElementID || itemProjection.AdoptedTerminalID != "20260723160000-itemlow" {
			_ = rebuilt.Close()
			t.Fatalf("iteration %d Item projection = %#v", iteration, itemProjection)
		}
		if !drillFound || !drillProjection.Member || drillProjection.AdoptedTerminalEventID != "20260723160001-drilllo" {
			_ = rebuilt.Close()
			t.Fatalf("iteration %d Drill projection = %#v", iteration, drillProjection)
		}
		actualHash, err := canonicalHash(snapshot)
		if closeErr := rebuilt.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			expectedHash = actualHash
		} else if actualHash != expectedHash {
			t.Fatalf("iteration %d rebuilt projection hash = %s, want %s", iteration, actualHash, expectedHash)
		}
	}
}

func permutedCreateTopicEvents(events []SchedulingEvent, iteration int) []SchedulingEvent {
	permuted := append([]SchedulingEvent(nil), events...)
	if len(permuted) == 0 {
		return permuted
	}
	shift := iteration % len(permuted)
	permuted = append(permuted[shift:], permuted[:shift]...)
	if iteration%2 == 1 {
		for left, right := 0, len(permuted)-1; left < right; left, right = left+1, right-1 {
			permuted[left], permuted[right] = permuted[right], permuted[left]
		}
	}
	return permuted
}

func writePermutedCreateTopicAuthorityFiles(t *testing.T, root string, files map[string][]byte, iteration int) {
	t.Helper()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) != 0 {
		shift := iteration % len(paths)
		paths = append(paths[shift:], paths[:shift]...)
		if iteration%2 == 1 {
			for left, right := 0, len(paths)-1; left < right; left, right = left+1, right-1 {
				paths[left], paths[right] = paths[right], paths[left]
			}
		}
	}
	for _, relative := range paths {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, files[relative], 0644); err != nil {
			t.Fatal(err)
		}
	}
}
