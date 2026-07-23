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
	"sync/atomic"
	"testing"
	"time"
)

func TestCreateHTMLTopicQueuedBehindPartialFailureObservesUnavailableEngine(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	restoreIDs := withCreateHTMLTopicNodeIDs(t, "20260723150000-queueaa", "20260723150001-queueev")
	defer restoreIDs()

	var lockEntries atomic.Int32
	secondAtLock := make(chan struct{})
	engine.beforeCreateHTMLTopicLock = func() {
		if lockEntries.Add(1) == 2 {
			close(secondAtLock)
		}
	}
	rootWritten := make(chan struct{})
	releaseRoot := make(chan struct{})
	previousFault := createHTMLTopicAuthorityFault
	createHTMLTopicAuthorityFault = func(stage string) error {
		if stage != "root" {
			return nil
		}
		select {
		case <-rootWritten:
		default:
			close(rootWritten)
		}
		<-releaseRoot
		return errors.New("injected root failure")
	}
	t.Cleanup(func() { createHTMLTopicAuthorityFault = previousFault })

	command := CreateElementCommand{Kind: CreateElementAddNewTopic, AddNewTopic: AddNewTopicCommand{Title: "Queued", HTML: "<p>Body</p>"}}
	firstDone := make(chan error, 1)
	go func() {
		_, err := engine.CreateElement(context.Background(), command)
		firstDone <- err
	}()
	<-rootWritten

	secondDone := make(chan error, 1)
	go func() {
		_, err := engine.CreateElement(context.Background(), command)
		secondDone <- err
	}()
	<-secondAtLock
	close(releaseRoot)

	if err := <-firstDone; !hasCode(err, ErrElementWritePartial) {
		t.Fatalf("first create error = %v", err)
	}
	if err := <-secondDone; !hasCode(err, ErrProjectionRebuildFailed) {
		t.Fatalf("queued create error = %v", err)
	}
}

func TestProjectionRefreshCannotPublishOlderSnapshotAfterCreate(t *testing.T) {
	engine, _ := newFixtureEngine(t)
	restoreIDs := withCreateHTMLTopicNodeIDs(t, "20260723151000-seriala", "20260723151001-seriale")
	defer restoreIDs()

	var refreshes atomic.Int32
	firstReady := make(chan struct{})
	secondReady := make(chan struct{})
	releaseFirst := make(chan struct{})
	engine.beforeProjectionPublish = func() {
		switch refreshes.Add(1) {
		case 1:
			close(firstReady)
			<-releaseFirst
		case 2:
			close(secondReady)
		}
	}

	oldRefreshDone := make(chan error, 1)
	go func() { oldRefreshDone <- engine.refreshProjection(context.Background()) }()
	<-firstReady

	createDone := make(chan struct {
		result CreateElementResult
		err    error
	}, 1)
	go func() {
		result, err := engine.CreateElement(context.Background(), CreateElementCommand{
			Kind:        CreateElementAddNewTopic,
			AddNewTopic: AddNewTopicCommand{Title: "Serialized", HTML: "<p>Body</p>"},
		})
		createDone <- struct {
			result CreateElementResult
			err    error
		}{result: result, err: err}
	}()

	var created struct {
		result CreateElementResult
		err    error
	}
	select {
	case <-secondReady:
		created = <-createDone
		close(releaseFirst)
	case <-time.After(100 * time.Millisecond):
		close(releaseFirst)
		created = <-createDone
	}
	if err := <-oldRefreshDone; err != nil {
		t.Fatal(err)
	}
	if created.err != nil || created.result.Topic == nil {
		t.Fatalf("create result = %#v, err=%v", created.result, created.err)
	}
	query, err := engine.Query(context.Background(), Query{Kind: QueryElement, ElementID: created.result.ElementID})
	if err != nil || query.Element == nil || query.Element.ScheduleProjection == nil || query.Element.ScheduleProjection.AdoptedTerminalID != created.result.EventID {
		t.Fatalf("published created Topic = %#v, err=%v", query.Element, err)
	}
}

func TestCreateHTMLTopicAndLearningWritesShareCollectionLease(t *testing.T) {
	engine, config := newFixtureEngine(t)
	if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionShowAnswer, ElementID: fixtureElementID}); err != nil {
		t.Fatal(err)
	}
	restoreIDs := withCreateHTMLTopicNodeIDs(t, "20260723230000-lockaaa", "20260723230001-lockevt")
	defer restoreIDs()

	createPublishing := make(chan struct{})
	releaseCreate := make(chan struct{})
	var createReleased atomic.Bool
	releaseCreateOnce := func() {
		if createReleased.CompareAndSwap(false, true) {
			close(releaseCreate)
		}
	}
	defer releaseCreateOnce()
	var blocked atomic.Bool
	engine.beforeProjectionPublish = func() {
		if blocked.CompareAndSwap(false, true) {
			close(createPublishing)
			<-releaseCreate
		}
	}

	createDone := make(chan error, 1)
	go func() {
		_, err := engine.CreateElement(t.Context(), CreateElementCommand{
			Kind:        CreateElementAddNewTopic,
			AddNewTopic: AddNewTopicCommand{Title: "Serialized", HTML: "<p>Body</p>"},
		})
		createDone <- err
	}()
	<-createPublishing

	grade := 4
	gradeEventID := "20260723230002-gradeaa"
	gradeWaiting := make(chan struct{})
	engine.onLearningActionLockContended = func() { close(gradeWaiting) }
	gradeDone := make(chan error, 1)
	go func() {
		_, err := engine.RunLearningAction(t.Context(), LearningAction{Kind: ActionGradeItem, ElementID: fixtureElementID, RawGrade: &grade, EventID: gradeEventID})
		gradeDone <- err
	}()
	select {
	case <-gradeWaiting:
	case <-time.After(time.Second):
		t.Fatal("learning action did not contend on the collection scheduling-write mutex")
	}
	if count := countEventsByID(t, config, gradeEventID); count != 0 {
		t.Fatalf("learning write committed %d events before create publication", count)
	}
	releaseCreateOnce()
	if err := <-createDone; err != nil {
		t.Fatal(err)
	}
	if err := <-gradeDone; err != nil {
		t.Fatal(err)
	}
}
