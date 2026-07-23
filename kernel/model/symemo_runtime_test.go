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

package model

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/88250/lute/parse"
	"github.com/siyuan-note/dejavu"
	"github.com/siyuan-note/dejavu/entity"
	"github.com/siyuan-note/siyuan/kernel/model/symemo"
	"github.com/siyuan-note/siyuan/kernel/treenode"
)

func TestSymemoRuntimeRejectsBeforeInitializationWithoutOpening(t *testing.T) {
	opens := 0
	runtime := newSymemoRuntime(func(context.Context) (*symemo.Engine, error) {
		opens++
		return nil, errors.New("unexpected open")
	})

	_, err := runtime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession})
	if !errors.Is(err, errSymemoRuntimeUninitialized) {
		t.Fatalf("pre-initialization query error = %v", err)
	}
	if opens != 0 {
		t.Fatalf("pre-initialization query opened %d Engines", opens)
	}
}

func TestSymemoRuntimeLatchesInitializationFailureWithoutRequestReopen(t *testing.T) {
	opens := 0
	runtime := newSymemoRuntime(func(context.Context) (*symemo.Engine, error) {
		opens++
		return nil, errors.New("injected construction failure")
	})

	if err := runtime.initialize(t.Context()); err == nil {
		t.Fatal("initialization succeeded despite injected construction failure")
	}
	for attempt := 0; attempt < 3; attempt++ {
		_, err := runtime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession})
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}
	if opens != 1 {
		t.Fatalf("failed Runtime opened %d Engines", opens)
	}
}

func TestInitSymemoLatchesSchedulerBootstrapFailure(t *testing.T) {
	root := t.TempDir()
	schedulerRoot := filepath.Join(root, "scheduler")
	if err := os.WriteFile(schedulerRoot, []byte("blocked"), 0644); err != nil {
		t.Fatal(err)
	}
	config := symemo.Config{
		StorageRoot:   filepath.Join(root, "storage"),
		IndexRoot:     filepath.Join(root, "temp", "siyuanmemo"),
		SchedulerRoot: schedulerRoot,
	}
	opens := 0
	hostRuntime := newSymemoRuntime(func(context.Context) (*symemo.Engine, error) {
		opens++
		return nil, errors.New("Engine must not open after bootstrap failure")
	})
	waits := 0
	if err := initializeSymemo(t.Context(), hostRuntime, config, func() { waits++ }); err == nil {
		t.Fatal("scheduler bootstrap unexpectedly succeeded")
	}
	for attempt := 0; attempt < 3; attempt++ {
		_, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession})
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}
	if opens != 0 || waits != 4 {
		t.Fatalf("opens=%d waits=%d", opens, waits)
	}
}

func TestSymemoRuntimeWaitsForStorageSyncBeforeTakingLease(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	runtime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		return symemo.NewEngine(ctx, config)
	})
	if err := runtime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	waiting := make(chan struct{})
	release := make(chan struct{})
	runtime.setStorageWaiter(func() {
		close(waiting)
		<-release
	})
	done := make(chan error, 1)
	go func() {
		_, err := runtime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession})
		done <- err
	}()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("query did not wait for synchronized storage")
	}
	runtime.mu.Lock()
	active := runtime.active
	runtime.mu.Unlock()
	if active != 0 {
		t.Fatalf("storage wait held %d Runtime leases", active)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSymemoRuntimeSyncDrainRejectsNewLeaseBeforeReplacement(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })

	drain := hostRuntime.beginSyncDrain()
	if !drain.acquired {
		t.Fatal("available Runtime did not begin sync drain")
	}
	if _, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err == nil {
		t.Fatal("sync drain admitted a new Runtime lease")
	} else {
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}
	if err := hostRuntime.rebuildAfterSyncDrain(t.Context(), drain); err != nil {
		t.Fatal(err)
	}
	if opens != 2 {
		t.Fatalf("Engine opens = %d, want replacement", opens)
	}
}

func TestSymemoRuntimeSyncDrainWaitsForActiveLeaseAndCanResume(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })

	operationStarted := make(chan struct{})
	releaseOperation := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseOperation) }) }
	t.Cleanup(release)
	operationDone := make(chan error, 1)
	go func() {
		operationDone <- hostRuntime.withEngine(func(*symemo.Engine) error {
			close(operationStarted)
			<-releaseOperation
			return nil
		})
	}()
	<-operationStarted
	drainDone := make(chan symemoSyncDrain, 1)
	go func() { drainDone <- hostRuntime.beginSyncDrain() }()
	select {
	case <-drainDone:
		t.Fatal("sync drain completed before active lease")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-operationDone; err != nil {
		t.Fatal(err)
	}
	drain := <-drainDone
	if !drain.acquired || !drain.resume {
		t.Fatal("available Runtime did not acquire sync drain")
	}
	hostRuntime.endSyncDrain(drain)
	if _, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err != nil {
		t.Fatalf("unchanged sync did not resume Runtime: %v", err)
	}
}

func TestSymemoRuntimeSyncDrainDoesNotResumeFailureFromDrainedLease(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })

	operationStarted := make(chan struct{})
	releaseOperation := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseOperation) }) }
	t.Cleanup(release)
	failure := &symemo.DomainError{Code: symemo.ErrProjectionRefreshFailed, Message: "injected projection failure"}
	operationDone := make(chan error, 1)
	go func() {
		operationDone <- hostRuntime.withEngine(func(*symemo.Engine) error {
			close(operationStarted)
			<-releaseOperation
			return failure
		})
	}()
	<-operationStarted

	drainDone := make(chan symemoSyncDrain, 1)
	go func() { drainDone <- hostRuntime.beginSyncDrain() }()
	drainDeadline := time.NewTimer(time.Second)
	defer drainDeadline.Stop()
	for {
		hostRuntime.mu.Lock()
		draining := hostRuntime.draining
		hostRuntime.mu.Unlock()
		if draining {
			break
		}
		select {
		case <-drainDeadline.C:
			t.Fatal("Runtime did not begin sync draining")
		default:
			runtime.Gosched()
		}
	}
	release()
	if err := <-operationDone; !errors.Is(err, failure) {
		t.Fatalf("drained lease error = %v", err)
	}
	drain := <-drainDone
	if !drain.acquired || !drain.resume {
		t.Fatalf("available Runtime drain = %#v", drain)
	}
	hostRuntime.endSyncDrain(drain)

	if _, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err == nil {
		t.Fatal("unchanged sync resumed a Runtime failed by an in-flight lease")
	} else {
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}
	hostRuntime.mu.Lock()
	state, storedFailure := hostRuntime.state, hostRuntime.failure
	hostRuntime.mu.Unlock()
	if state != symemoRuntimeUnavailable || !errors.Is(storedFailure, failure) {
		t.Fatalf("Runtime state=%d failure=%v", state, storedFailure)
	}
}

func TestSymemoRuntimeSyncDrainWaitsForActiveLeaseWhileUnavailable(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })

	operationStarted := make(chan struct{})
	releaseOperation := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseOperation) }) }
	t.Cleanup(release)
	operationDone := make(chan error, 1)
	go func() {
		operationDone <- hostRuntime.withEngine(func(*symemo.Engine) error {
			close(operationStarted)
			<-releaseOperation
			return nil
		})
	}()
	<-operationStarted
	failure := &symemo.DomainError{Code: symemo.ErrProjectionRefreshFailed, Message: "injected projection failure"}
	if err := hostRuntime.withEngine(func(*symemo.Engine) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("failing lease error = %v", err)
	}

	drainDone := make(chan symemoSyncDrain, 1)
	go func() { drainDone <- hostRuntime.beginSyncDrain() }()
	select {
	case <-drainDone:
		t.Fatal("unavailable Runtime drain completed before active lease")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-operationDone; err != nil {
		t.Fatal(err)
	}
	drain := <-drainDone
	if !drain.acquired || drain.resume {
		t.Fatal("unavailable Runtime did not acquire sync drain")
	}
	hostRuntime.endSyncDrain(drain)
	if _, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err == nil {
		t.Fatal("sync drain resumed a previously unavailable Runtime")
	} else {
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}
}

func TestSymemoRuntimeSyncDrainReleasesUnavailableRuntimeWithoutEngine(t *testing.T) {
	hostRuntime := newSymemoRuntime(func(context.Context) (*symemo.Engine, error) {
		return nil, errors.New("injected initialization failure")
	})
	if err := hostRuntime.initialize(t.Context()); err == nil {
		t.Fatal("Runtime initialization unexpectedly succeeded")
	}
	drain := hostRuntime.beginSyncDrain()
	if !drain.acquired || drain.resume {
		t.Fatalf("failed Runtime drain = %#v", drain)
	}
	hostRuntime.endSyncDrain(drain)
	hostRuntime.mu.Lock()
	draining, state := hostRuntime.draining, hostRuntime.state
	hostRuntime.mu.Unlock()
	if draining || state != symemoRuntimeUnavailable {
		t.Fatalf("failed Runtime after sync drain: draining=%v state=%d", draining, state)
	}
}

func TestSymemoRuntimeOrdinaryRebuildWaitsForSyncOwnedDrain(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })

	drain := hostRuntime.beginSyncDrain()
	if !drain.acquired {
		t.Fatal("available Runtime did not acquire sync drain")
	}
	rebuildStarted := make(chan struct{})
	rebuildDone := make(chan error, 1)
	go func() {
		close(rebuildStarted)
		rebuildDone <- hostRuntime.rebuild(t.Context())
	}()
	<-rebuildStarted
	select {
	case err := <-rebuildDone:
		t.Fatalf("ordinary rebuild crossed sync-owned drain: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	hostRuntime.mu.Lock()
	rebuilding := hostRuntime.rebuilding
	hostRuntime.mu.Unlock()
	if rebuilding || opens != 1 {
		t.Fatalf("ordinary rebuild entered replacement during sync drain: rebuilding=%v opens=%d", rebuilding, opens)
	}

	hostRuntime.endSyncDrain(drain)
	if err := <-rebuildDone; err != nil {
		t.Fatal(err)
	}
	if opens != 2 {
		t.Fatalf("ordinary rebuild opened %d Engines after sync drain", opens)
	}
}

func TestSymemoRuntimeStorageWaiterReplacementIsRaceFree(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })

	const operations = 100
	errorsFound := make(chan error, operations)
	var group sync.WaitGroup
	for operation := 0; operation < operations; operation++ {
		group.Add(2)
		go func() {
			defer group.Done()
			hostRuntime.setStorageWaiter(func() {})
		}()
		go func() {
			defer group.Done()
			_, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession})
			if err != nil {
				errorsFound <- err
			}
		}()
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
}

func TestSymemoRuntimeCloseDrainsActiveOperationAndRejectsNewWork(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}

	operationStarted := make(chan struct{})
	releaseOperation := make(chan struct{})
	operationDone := make(chan error, 1)
	go func() {
		operationDone <- hostRuntime.withEngine(func(*symemo.Engine) error {
			close(operationStarted)
			<-releaseOperation
			return nil
		})
	}()
	<-operationStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- hostRuntime.Close() }()
	for attempt := 0; attempt < 1000; attempt++ {
		hostRuntime.mu.Lock()
		draining := hostRuntime.draining
		hostRuntime.mu.Unlock()
		if draining {
			break
		}
		if attempt == 999 {
			t.Fatal("Runtime did not begin draining")
		}
		runtime.Gosched()
	}
	_, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession})
	assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	select {
	case err = <-closeDone:
		t.Fatalf("Runtime closed before active operation completed: %v", err)
	default:
	}
	close(releaseOperation)
	if err = <-operationDone; err != nil {
		t.Fatal(err)
	}
	if err = <-closeDone; err != nil {
		t.Fatal(err)
	}
	_, err = hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession})
	if !errors.Is(err, errSymemoRuntimeUninitialized) {
		t.Fatalf("closed Runtime query error = %v", err)
	}
}

func TestSymemoRuntimeRebuildWaitsForConcurrentClose(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}

	operationStarted := make(chan struct{})
	releaseOperation := make(chan struct{})
	operationDone := make(chan error, 1)
	go func() {
		operationDone <- hostRuntime.withEngine(func(*symemo.Engine) error {
			close(operationStarted)
			<-releaseOperation
			return nil
		})
	}()
	<-operationStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- hostRuntime.Close() }()
	for {
		hostRuntime.mu.Lock()
		draining := hostRuntime.draining
		hostRuntime.mu.Unlock()
		if draining {
			break
		}
		runtime.Gosched()
	}

	rebuildStarted := make(chan struct{})
	rebuildDone := make(chan error, 1)
	go func() {
		close(rebuildStarted)
		rebuildDone <- hostRuntime.rebuild(t.Context())
	}()
	<-rebuildStarted
	time.Sleep(100 * time.Millisecond)
	hostRuntime.mu.Lock()
	rebuildingDuringClose := hostRuntime.rebuilding
	hostRuntime.mu.Unlock()
	close(releaseOperation)
	if err := <-operationDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	rebuildErr := <-rebuildDone
	if rebuildingDuringClose {
		t.Fatal("rebuild entered replacement while Close was draining")
	}
	if !errors.Is(rebuildErr, errSymemoRuntimeUninitialized) {
		t.Fatalf("rebuild after Close error = %v", rebuildErr)
	}
	if opens != 1 {
		t.Fatalf("concurrent Close/rebuild opened %d Engines", opens)
	}
}

func TestSymemoRuntimeRebuildPublishesOnlySuccessfulReplacement(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		if opens == 2 {
			return nil, errors.New("injected replacement failure")
		}
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })
	hostRuntime.mu.Lock()
	first := hostRuntime.engine
	hostRuntime.mu.Unlock()

	if err := hostRuntime.rebuild(t.Context()); err == nil {
		t.Fatal("failed replacement rebuild succeeded")
	}
	_, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession})
	assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	if opens != 2 {
		t.Fatalf("failed rebuild opened %d Engines", opens)
	}

	if err = hostRuntime.rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err != nil {
		t.Fatal(err)
	}
	hostRuntime.mu.Lock()
	replacement := hostRuntime.engine
	hostRuntime.mu.Unlock()
	if replacement == nil || replacement == first || opens != 3 {
		t.Fatalf("replacement=%p first=%p opens=%d", replacement, first, opens)
	}
}

func TestSymemoRuntimeAcceptedTriggerLatchesUntilExplicitRebuild(t *testing.T) {
	config := runtimeSymemoFixtureConfig(t)
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })

	if _, err := hostRuntime.learningAction(t.Context(), symemo.LearningAction{Kind: symemo.ActionStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := hostRuntime.learningAction(t.Context(), symemo.LearningAction{Kind: symemo.ActionShowAnswer, ElementID: runtimeSymemoFixtureElementID}); err != nil {
		t.Fatal(err)
	}
	restoreProjection := installRuntimeProjectionRefreshFailure(t, config)
	grade := 4
	eventID := "20260721110000-runtime-latch"
	_, err := hostRuntime.learningAction(t.Context(), symemo.LearningAction{Kind: symemo.ActionGradeItem, ElementID: runtimeSymemoFixtureElementID, RawGrade: &grade, EventID: eventID})
	domainErr, ok := symemo.AsDomainError(err)
	if !ok || domainErr.Code != symemo.ErrProjectionRefreshFailed || !domainErr.ReviewAccepted || !domainErr.Retryable || domainErr.AcceptedEventID != eventID || domainErr.Session == nil || domainErr.Session.PendingAcceptedEventID != eventID {
		t.Fatalf("accepted trigger error = %#v", err)
	}
	if opens != 1 {
		t.Fatalf("trigger opened %d Engines before explicit rebuild", opens)
	}
	if _, err = hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err == nil {
		t.Fatal("latched Runtime exposed prior projection")
	} else {
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}
	if opens != 1 {
		t.Fatalf("latched request reopened %d Engines", opens)
	}
	events, err := config.LoadEventFiles()
	if err != nil {
		t.Fatal(err)
	}
	acceptedCount := 0
	for _, event := range events {
		if event.EventID == eventID {
			acceptedCount++
		}
	}
	if acceptedCount != 1 {
		t.Fatalf("accepted event count = %d", acceptedCount)
	}

	restoreProjection()
	if err = hostRuntime.rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	if opens != 2 {
		t.Fatalf("successful rebuild opened %d Engines", opens)
	}
	if _, err = hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err != nil {
		t.Fatal(err)
	}
}

func TestSymemoRuntimeCreateElementUsesLeaseAndPublishedProjection(t *testing.T) {
	config := runtimeSymemoFixtureConfig(t)
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })

	result, err := hostRuntime.createElement(t.Context(), symemo.CreateElementCommand{
		Kind: symemo.CreateElementAddNewTopic,
		AddNewTopic: symemo.AddNewTopicCommand{
			Title: "Runtime Topic",
			HTML:  "<p>Runtime body</p>",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ElementID == "" || result.EventID == "" || !result.CreateAccepted || !result.ReviewAccepted || result.Topic == nil {
		t.Fatalf("Runtime create result = %#v", result)
	}
	hostRuntime.mu.Lock()
	active := hostRuntime.active
	state := hostRuntime.state
	hostRuntime.mu.Unlock()
	if active != 0 || state != symemoRuntimeAvailable || opens != 1 {
		t.Fatalf("Runtime create lease state active=%d state=%d opens=%d", active, state, opens)
	}
	query, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryElement, ElementID: result.ElementID})
	if err != nil || query.Element == nil || query.Element.Title != "Runtime Topic" || query.Element.ScheduleProjection == nil {
		t.Fatalf("Runtime create query = %#v, err=%v", query.Element, err)
	}
}

func TestSymemoRuntimePartialCreateReleasesLeaseDrainsAndPublishesReplacement(t *testing.T) {
	config := runtimeSymemoFixtureConfig(t)
	var opens atomic.Int32
	replacementStarted := make(chan struct{}, 1)
	var hostRuntime *symemoRuntime
	hostRuntime = newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		if opens.Add(1) > 1 {
			hostRuntime.mu.Lock()
			active := hostRuntime.active
			hostRuntime.mu.Unlock()
			if active != 0 {
				return nil, errors.New("replacement opened before Runtime leases drained")
			}
			replacementStarted <- struct{}{}
		}
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })
	hostRuntime.mu.Lock()
	first := hostRuntime.engine
	hostRuntime.mu.Unlock()

	sortDirectory := filepath.Join(config.StorageRoot, "elements", ".siyuan")
	if err := os.WriteFile(sortDirectory, []byte("blocks sort directory creation"), 0644); err != nil {
		t.Fatal(err)
	}
	operationStarted := make(chan struct{})
	releaseOperation := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseOperation) }) }
	t.Cleanup(release)
	operationDone := make(chan error, 1)
	go func() {
		operationDone <- hostRuntime.withEngine(func(engine *symemo.Engine) error {
			if engine != first {
				return errors.New("in-flight operation received replacement Engine")
			}
			close(operationStarted)
			<-releaseOperation
			return nil
		})
	}()
	<-operationStarted

	type createOutcome struct {
		result symemo.CreateElementResult
		err    error
	}
	createDone := make(chan createOutcome, 1)
	go func() {
		result, err := hostRuntime.createElement(t.Context(), symemo.CreateElementCommand{
			Kind:        symemo.CreateElementAddNewTopic,
			AddNewTopic: symemo.AddNewTopicCommand{Title: "Partial Runtime Topic", HTML: "<p>Body</p>"},
		})
		createDone <- createOutcome{result: result, err: err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		hostRuntime.mu.Lock()
		draining := hostRuntime.draining
		hostRuntime.mu.Unlock()
		if draining {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial create did not begin Runtime rebuild")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case outcome := <-createDone:
		t.Fatalf("partial create returned before in-flight lease drained: %#v", outcome)
	default:
	}
	select {
	case <-replacementStarted:
		t.Fatal("replacement opened before in-flight lease drained")
	default:
	}
	if _, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err == nil {
		t.Fatal("draining Runtime accepted new work")
	} else {
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}

	release()
	if err := <-operationDone; err != nil {
		t.Fatal(err)
	}
	outcome := <-createDone
	domainErr, ok := symemo.AsDomainError(outcome.err)
	if !ok || domainErr.Code != symemo.ErrElementWritePartial || outcome.result.ElementID == "" || outcome.result.EventID == "" {
		t.Fatalf("partial create outcome = %#v, err=%#v", outcome.result, domainErr)
	}
	<-replacementStarted
	if opens.Load() != 2 {
		t.Fatalf("Engine opens = %d", opens.Load())
	}
	hostRuntime.mu.Lock()
	replacement := hostRuntime.engine
	active := hostRuntime.active
	state := hostRuntime.state
	hostRuntime.mu.Unlock()
	if replacement == nil || replacement == first || active != 0 || state != symemoRuntimeAvailable {
		t.Fatalf("replacement=%p first=%p active=%d state=%d", replacement, first, active, state)
	}
	query, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryElement, ElementID: outcome.result.ElementID})
	if err != nil || query.Element == nil || query.Element.ScheduleProjection != nil {
		t.Fatalf("rebuilt partial root = %#v, err=%v", query.Element, err)
	}
	events, err := config.LoadEventFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.EventID == outcome.result.EventID {
			t.Fatalf("partial create accepted event %#v", event)
		}
	}
}

func TestSymemoRuntimeAcceptedCreateLatchesUntilCompleteReplacement(t *testing.T) {
	config := runtimeSymemoFixtureConfig(t)
	var opens atomic.Int32
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens.Add(1)
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })

	restoreProjection := installRuntimeProjectionRefreshFailure(t, config)
	result, err := hostRuntime.createElement(t.Context(), symemo.CreateElementCommand{
		Kind:        symemo.CreateElementAddNewTopic,
		AddNewTopic: symemo.AddNewTopicCommand{Title: "Accepted Runtime Topic", HTML: "<p>Body</p>"},
	})
	domainErr, ok := symemo.AsDomainError(err)
	if !ok || domainErr.Code != symemo.ErrProjectionRefreshFailed || !domainErr.CreateAccepted || !domainErr.ReviewAccepted || domainErr.Retryable || result.ElementID == "" || result.EventID == "" {
		t.Fatalf("accepted create outcome = %#v, err=%#v", result, domainErr)
	}
	if opens.Load() != 1 {
		t.Fatalf("accepted failure opened %d Engines", opens.Load())
	}
	if _, err = hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryElement, ElementID: result.ElementID}); err == nil {
		t.Fatal("accepted projection failure exposed stale Runtime")
	} else {
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}

	restoreProjection()
	if err = hostRuntime.rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 2 {
		t.Fatalf("replacement opened %d Engines", opens.Load())
	}
	query, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryElement, ElementID: result.ElementID})
	if err != nil || query.Element == nil || query.Element.ScheduleProjection == nil || query.Element.ScheduleProjection.AdoptedTerminalID != result.EventID {
		t.Fatalf("replacement created Topic = %#v, err=%v", query.Element, err)
	}
	events, err := config.LoadEventFiles()
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for _, event := range events {
		if event.EventID == result.EventID {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted create event count = %d", accepted)
	}
}

func TestSymemoRuntimeRebuildDrainsActiveOperationAndRejectsNewWork(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	triggerReturned := make(chan struct{})
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		if opens > 1 {
			select {
			case <-triggerReturned:
			default:
				return nil, errors.New("replacement construction started before trigger returned")
			}
		}
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })
	hostRuntime.mu.Lock()
	first := hostRuntime.engine
	hostRuntime.mu.Unlock()

	operationStarted := make(chan struct{})
	releaseOperation := make(chan struct{})
	operationDone := make(chan error, 1)
	go func() {
		operationDone <- hostRuntime.withEngine(func(engine *symemo.Engine) error {
			defer close(triggerReturned)
			if engine != first {
				return errors.New("active operation received the wrong Engine")
			}
			close(operationStarted)
			<-releaseOperation
			return &symemo.DomainError{Code: symemo.ErrProjectionRefreshFailed, ReviewAccepted: true, AcceptedEventID: "blocked-trigger"}
		})
	}()
	<-operationStarted
	rebuildDone := make(chan error, 1)
	go func() { rebuildDone <- hostRuntime.rebuild(t.Context()) }()
	for attempt := 0; attempt < 1000; attempt++ {
		hostRuntime.mu.Lock()
		draining := hostRuntime.draining
		hostRuntime.mu.Unlock()
		if draining {
			break
		}
		if attempt == 999 {
			t.Fatal("Runtime did not begin rebuild draining")
		}
		runtime.Gosched()
	}
	if _, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err == nil {
		t.Fatal("draining Runtime accepted new work")
	} else {
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}
	select {
	case err := <-rebuildDone:
		t.Fatalf("rebuild completed before active operation: %v", err)
	default:
	}
	close(releaseOperation)
	if err := <-operationDone; err == nil {
		t.Fatal("blocked trigger unexpectedly succeeded")
	} else {
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRefreshFailed)
	}
	if err := <-rebuildDone; err != nil {
		t.Fatal(err)
	}
	hostRuntime.mu.Lock()
	replacement := hostRuntime.engine
	hostRuntime.mu.Unlock()
	if replacement == nil || replacement == first {
		t.Fatalf("replacement=%p first=%p", replacement, first)
	}
}

func TestSymemoBlockReferenceReaderClassifiesNativeResolution(t *testing.T) {
	loadCalls := 0
	reader := &siyuanBlockReferenceReader{
		lookupMany: func(ids []string) map[string]*treenode.BlockTree {
			found := map[string]*treenode.BlockTree{}
			for _, id := range ids {
				switch id {
				case "20260721100000-ordinary":
					found[id] = &treenode.BlockTree{ID: id, BoxID: "ordinary-box", Path: "/ordinary.sy"}
				case "20260721100000-encryptd":
					found[id] = &treenode.BlockTree{ID: id, BoxID: "encrypted-box", Path: "/encrypted.sy"}
				}
			}
			return found
		},
		load: func(id string) (*parse.Tree, error) {
			loadCalls++
			switch id {
			case "20260721100000-ordinary":
				return &parse.Tree{Box: "moved-box", Path: "/moved.sy"}, nil
			case "20260721100000-absentx":
				return nil, ErrTreeNotFound
			case "20260721100000-lagging":
				return nil, nil
			case "20260721100000-lateenc":
				return nil, &symemoEncryptedBlockError{blockID: id, boxID: "encrypted-box", path: "/late-encrypted.sy"}
			default:
				return nil, ErrIndexing
			}
		},
		isEncrypted: func(boxID string) bool { return boxID == "encrypted-box" },
	}

	batch, err := reader.LookupMany(t.Context(), []string{"20260721100000-missingx", "20260721100000-encryptd", "20260721100000-ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	if batch["20260721100000-ordinary"].Status != symemo.MaterialSourceAvailable || batch["20260721100000-ordinary"].CurrentNotebookID != "ordinary-box" {
		t.Fatalf("ordinary batch resolution = %#v", batch["20260721100000-ordinary"])
	}
	if !batch["20260721100000-encryptd"].Encrypted || batch["20260721100000-encryptd"].Status != symemo.MaterialSourceAvailable {
		t.Fatalf("encrypted batch resolution = %#v", batch["20260721100000-encryptd"])
	}
	if batch["20260721100000-missingx"].Status != symemo.MaterialSourceUnresolved {
		t.Fatalf("missing batch resolution = %#v", batch["20260721100000-missingx"])
	}

	available, err := reader.Load(t.Context(), "20260721100000-ordinary")
	if err != nil || available.Status != symemo.MaterialSourceAvailable || available.CurrentNotebookID != "moved-box" || available.CurrentPath != "/moved.sy" {
		t.Fatalf("available load = %#v, err=%v", available, err)
	}
	absent, err := reader.Load(t.Context(), "20260721100000-absentx")
	if err != nil || absent.Status != symemo.MaterialSourceUnavailable {
		t.Fatalf("absent load = %#v, err=%v", absent, err)
	}
	uncertain, err := reader.Load(t.Context(), "20260721100000-uncerta")
	if err != nil || uncertain.Status != symemo.MaterialSourceUnresolved {
		t.Fatalf("uncertain load = %#v, err=%v", uncertain, err)
	}
	lagging, err := reader.Load(t.Context(), "20260721100000-lagging")
	if err != nil || lagging.Status != symemo.MaterialSourceUnresolved {
		t.Fatalf("lagging load = %#v, err=%v", lagging, err)
	}
	lateEncrypted, err := reader.Load(t.Context(), "20260721100000-lateenc")
	if err != nil || !lateEncrypted.Encrypted || lateEncrypted.Status != symemo.MaterialSourceAvailable || lateEncrypted.CurrentNotebookID != "encrypted-box" || lateEncrypted.CurrentPath != "/late-encrypted.sy" {
		t.Fatalf("late encrypted load = %#v, err=%v", lateEncrypted, err)
	}
	encrypted, err := reader.Load(t.Context(), "20260721100000-encryptd")
	if err != nil || !encrypted.Encrypted || loadCalls != 5 {
		t.Fatalf("encrypted load = %#v, loadCalls=%d, err=%v", encrypted, loadCalls, err)
	}
}

func TestInitSymemoWaitsBeforeBootstrapAndEngineOpen(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{
		StorageRoot:   filepath.Join(root, "storage"),
		IndexRoot:     filepath.Join(root, "temp", "siyuanmemo"),
		SchedulerRoot: filepath.Join(root, "storage", "scheduler"),
	}
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		if !config.LoadEffectiveSchedulerConfig().PersistedComplete {
			return nil, errors.New("Engine opened before scheduler bootstrap")
		}
		return symemo.NewEngine(ctx, config)
	})
	t.Cleanup(func() { _ = hostRuntime.Close() })
	waiting := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- initializeSymemo(t.Context(), hostRuntime, config, func() {
			close(waiting)
			<-release
		})
	}()
	<-waiting
	if opens != 0 {
		t.Fatalf("storage wait opened %d Engines", opens)
	}
	if _, err := os.Stat(config.SchedulerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scheduler bootstrap ran before storage wait completed: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if opens != 1 || !config.LoadEffectiveSchedulerConfig().PersistedComplete {
		t.Fatalf("opens=%d effective=%#v", opens, config.LoadEffectiveSchedulerConfig())
	}
}

func TestSymemoServingEntrypointsPreserveRuntimeStartupBoundary(t *testing.T) {
	entrypoints := []struct {
		path string
		body func(*testing.T, *ast.File) *ast.BlockStmt
	}{
		{path: filepath.Join("..", "mobile", "kernel.go"), body: func(t *testing.T, file *ast.File) *ast.BlockStmt {
			return mustGoFunctionBody(t, file, "StartKernel")
		}},
		{path: filepath.Join("..", "harmony", "kernel.go"), body: func(t *testing.T, file *ast.File) *ast.BlockStmt {
			return mustGoFunctionBody(t, file, "StartKernel")
		}},
		{path: filepath.Join("..", "cli", "cmd", "serve.go"), body: func(t *testing.T, file *ast.File) *ast.BlockStmt {
			return mustCommandRunBody(t, file, "serveCmd")
		}},
	}
	startupOrder := []string{
		"model.BootSyncData",
		"model.InitBoxes",
		"model.InitSymemo",
		"model.LoadFlashcards",
		"util.SetBooted",
	}
	for _, entrypoint := range entrypoints {
		file := parseGoTestFile(t, entrypoint.path)
		calls := goCallNames(entrypoint.body(t, file))
		assertGoCallOrder(t, entrypoint.path, calls, startupOrder)
		if count := countGoCall(calls, "model.InitSymemo"); count != 1 {
			t.Errorf("%s initializes SiYuanMemo %d times", entrypoint.path, count)
		}
	}

	commandRoot := filepath.Join("..", "cli", "cmd")
	entries, err := os.ReadDir(commandRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || entry.Name() == "serve.go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(commandRoot, entry.Name())
		if countGoCall(goCallNames(parseGoTestFile(t, path)), "model.InitSymemo") != 0 {
			t.Errorf("one-shot command %s initializes SiYuanMemo", path)
		}
	}

	syncFile := parseGoTestFile(t, "sync.go")
	if countGoCall(goCallNames(mustGoFunctionBody(t, syncFile, "BootSyncData")), "InitSymemo") != 0 {
		t.Fatal("BootSyncData initializes SiYuanMemo")
	}

	runtimeFile := parseGoTestFile(t, "symemo_runtime.go")
	initBody := mustGoFunctionBody(t, runtimeFile, "InitSymemo")
	if !hasGoCallWithArgument(initBody, "initializeSymemo", "waitForSyncingStorages") {
		t.Fatal("InitSymemo bypasses synchronized-storage waiting")
	}
	for _, declaration := range runtimeFile.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.IsExported() && goResultIncludesSymemoEngine(function.Type.Results) {
			t.Errorf("model Runtime exposes a naked Engine through %s", function.Name.Name)
		}
	}
	for _, imported := range runtimeFile.Imports {
		for _, forbidden := range []string{"github.com/siyuan-note/riff", "github.com/siyuan-note/filelock", "kernel/filesys"} {
			if strings.Contains(imported.Path.Value, forbidden) {
				t.Errorf("model Runtime imports forbidden workflow or authoritative writer %q", forbidden)
			}
		}
	}
	for _, forbidden := range []string{"os.WriteFile", "filelock.WriteFile", "filesys.WriteTree"} {
		if countGoCall(goCallNames(runtimeFile), forbidden) != 0 {
			t.Errorf("model Runtime calls forbidden authoritative writer %s", forbidden)
		}
	}
	if countGoCall(goCallNames(mustGoFunctionBody(t, runtimeFile, "loadSymemoBlockReference")), "loadTreeByBlockIDWithReindexInBoxGuarded") != 1 {
		t.Fatal("production BlockReferenceReader does not retain the guarded native read/reindex path")
	}
}

func TestSyncMergeProcessesSymemoAuthorityBeforeStorageGateOpens(t *testing.T) {
	repositoryFile := parseGoTestFile(t, "repository.go")
	calls := goCallNames(mustGoFunctionBody(t, repositoryFile, "processSyncMergeResult"))
	if count := countGoCall(calls, "symemoSync.finish"); count != 2 {
		t.Fatalf("processSyncMergeResult has %d guarded SiYuanMemo sync finishes", count)
	}
	finishCalls := goCallNames(mustGoFunctionBody(t, repositoryFile, "finishSymemoRepositorySync"))
	if count := countGoCall(finishCalls, "rebuildSymemoAfterSyncMerge"); count != 1 {
		t.Fatalf("finishSymemoRepositorySync rebuilds SiYuanMemo %d times", count)
	}
}

func TestRepositorySyncDrainsRuntimeBeforeAuthorityMerge(t *testing.T) {
	repositoryFile := parseGoTestFile(t, "repository.go")
	beginCalls := goCallNames(mustGoFunctionBody(t, repositoryFile, "beginSymemoRepositorySync"))
	assertGoCallOrder(t, "beginSymemoRepositorySync", beginCalls, []string{"activeSymemoRepositorySyncs.Add", "workspaceSymemoRuntime.beginSyncDrain"})
	for _, test := range []struct {
		function string
		merge    string
	}{
		{function: "syncRepo", merge: "repo.Sync"},
		{function: "syncRepoDownload", merge: "repo.SyncDownload"},
	} {
		calls := goCallNames(mustGoFunctionBody(t, repositoryFile, test.function))
		assertGoCallOrder(t, test.function, calls, []string{"beginSymemoRepositorySync", "symemoSync.finish", test.merge, "symemoSync.recordMergeResult", "processSyncMergeResult"})
		if count := countGoCall(calls, "symemoSync.finish"); count != 2 {
			t.Errorf("%s has %d guarded SiYuanMemo sync finishes", test.function, count)
		}
	}
}

func TestPanickedRepositorySyncRebuildsAuthorityAndReopensGate(t *testing.T) {
	config := runtimeSymemoFixtureConfig(t)
	var opens atomic.Int32
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens.Add(1)
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	original := workspaceSymemoRuntime
	workspaceSymemoRuntime = hostRuntime
	t.Cleanup(func() {
		syncingStorages.Store(false)
		activeSymemoRepositorySyncs.Store(0)
		workspaceSymemoRuntime = original
		_ = hostRuntime.Close()
	})

	panicMarker := &struct{}{}
	func() {
		defer func() {
			if recovered := recover(); recovered != panicMarker {
				t.Fatalf("repository sync panic = %#v", recovered)
			}
		}()
		repositorySync := beginSymemoRepositorySync()
		defer repositorySync.finish()
		rewriteRuntimeFixturePrompt(t, config, "panic merge")
		panic(panicMarker)
	}()

	if isSyncingStorages() {
		t.Fatal("panicked repository sync left the storage gate closed")
	}
	if opens.Load() != 2 {
		t.Fatalf("panicked repository sync opened %d Engines", opens.Load())
	}
	assertRuntimeFixturePrompt(t, hostRuntime, "panic merge")
}

func TestOverlappingRepositorySyncGuardsKeepStorageGateClosed(t *testing.T) {
	config := runtimeSymemoFixtureConfig(t)
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	original := workspaceSymemoRuntime
	workspaceSymemoRuntime = hostRuntime
	t.Cleanup(func() {
		syncingStorages.Store(false)
		activeSymemoRepositorySyncs.Store(0)
		workspaceSymemoRuntime = original
		_ = hostRuntime.Close()
	})

	first := beginSymemoRepositorySync()
	secondDone := make(chan *symemoRepositorySync, 1)
	go func() { secondDone <- beginSymemoRepositorySync() }()
	waitDeadline := time.NewTimer(time.Second)
	defer waitDeadline.Stop()
	for activeSymemoRepositorySyncs.Load() != 2 {
		select {
		case <-waitDeadline.C:
			first.recordMergeResult(nil)
			first.finish()
			t.Fatal("overlapping repository sync did not register its storage-gate ownership")
		default:
			runtime.Gosched()
		}
	}

	first.recordMergeResult(nil)
	first.finish()
	var second *symemoRepositorySync
	select {
	case second = <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second repository sync did not acquire the Runtime drain")
	}
	gateClosedAfterFirst := isSyncingStorages()
	ownersAfterFirst := activeSymemoRepositorySyncs.Load()
	second.recordMergeResult(nil)
	second.finish()

	if !gateClosedAfterFirst || ownersAfterFirst != 1 {
		t.Fatalf("storage gate after first finish: closed=%t owners=%d", gateClosedAfterFirst, ownersAfterFirst)
	}
	if isSyncingStorages() || activeSymemoRepositorySyncs.Load() != 0 {
		t.Fatal("final repository sync owner did not reopen the storage gate")
	}
	if _, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err != nil {
		t.Fatalf("Runtime did not resume after all repository sync owners finished: %v", err)
	}
}

func TestFailedRepositorySyncRebuildsChangedSymemoAuthorityBeforeGateOpens(t *testing.T) {
	config := runtimeSymemoFixtureConfig(t)
	var opens atomic.Int32
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		openCount := opens.Add(1)
		if openCount > 1 && !isSyncingStorages() {
			t.Error("failed sync opened storage gate before Runtime replacement")
		}
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	original := workspaceSymemoRuntime
	workspaceSymemoRuntime = hostRuntime
	var repositorySync *symemoRepositorySync
	t.Cleanup(func() {
		repositorySync.finish()
		syncingStorages.Store(false)
		activeSymemoRepositorySyncs.Store(0)
		workspaceSymemoRuntime = original
		_ = hostRuntime.Close()
	})

	repositorySync = beginSymemoRepositorySync()
	if !repositorySync.drained.acquired {
		t.Fatal("available Runtime did not acquire failed sync drain")
	}
	rewriteRuntimeFixturePrompt(t, config, "failed merge")
	repositorySync.recordMergeResult(&dejavu.MergeResult{Upserts: []*entity.File{{Path: "/storage/siyuanmemo/elements/" + runtimeSymemoFixtureElementID + ".sme"}}})
	repositorySync.finish()
	if isSyncingStorages() {
		t.Fatal("failed sync left storage gate closed after replacement")
	}
	if opens.Load() != 2 {
		t.Fatalf("failed sync opened %d Engines", opens.Load())
	}
	assertRuntimeFixturePrompt(t, hostRuntime, "failed merge")
}

func TestSyncRebuildDefersUntilRuntimeInitialization(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		return symemo.NewEngine(ctx, config)
	})
	original := workspaceSymemoRuntime
	workspaceSymemoRuntime = hostRuntime
	t.Cleanup(func() {
		workspaceSymemoRuntime = original
		_ = hostRuntime.Close()
	})

	rebuildSymemoAfterSyncMerge(&dejavu.MergeResult{Upserts: []*entity.File{{Path: "/storage/siyuanmemo/reviews/2026-07.smr"}}}, symemoSyncDrain{})
	if opens != 0 || hostRuntime.state != symemoRuntimeUninitialized {
		t.Fatalf("startup sync opened Runtime before initialization: opens=%d state=%d", opens, hostRuntime.state)
	}
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatalf("initialization opens = %d, want 1", opens)
	}
}

func TestRebuildSymemoAfterSyncMergeFiltersAuthorityPaths(t *testing.T) {
	tests := []struct {
		name        string
		mergeResult *dejavu.MergeResult
		wantRebuild bool
	}{
		{name: "upsert", mergeResult: &dejavu.MergeResult{Upserts: []*entity.File{{Path: "/storage/siyuanmemo/reviews/2026-07.smr"}}}, wantRebuild: true},
		{name: "remove", mergeResult: &dejavu.MergeResult{Removes: []*entity.File{{Path: "/storage/siyuanmemo/elements/root.sme"}}}, wantRebuild: true},
		{name: "conflict", mergeResult: &dejavu.MergeResult{Conflicts: []*entity.File{{Path: "/storage/siyuanmemo/scheduler/learning-day.json"}}}, wantRebuild: true},
		{name: "other storage", mergeResult: &dejavu.MergeResult{Upserts: []*entity.File{{Path: "/storage/riff/deck.json"}}}},
		{name: "similar prefix", mergeResult: &dejavu.MergeResult{Removes: []*entity.File{{Path: "/storage/siyuanmemo-old/reviews/2026-07.smr"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
			opens := 0
			hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
				opens++
				return symemo.NewEngine(ctx, config)
			})
			if err := hostRuntime.initialize(t.Context()); err != nil {
				t.Fatal(err)
			}
			original := workspaceSymemoRuntime
			workspaceSymemoRuntime = hostRuntime
			t.Cleanup(func() {
				workspaceSymemoRuntime = original
				_ = hostRuntime.Close()
			})

			rebuildSymemoAfterSyncMerge(test.mergeResult, symemoSyncDrain{})
			wantOpens := 1
			if test.wantRebuild {
				wantOpens = 2
			}
			if opens != wantOpens {
				t.Fatalf("Engine opens = %d, want %d", opens, wantOpens)
			}
		})
	}
}

func TestRebuildSymemoAfterSyncMergePublishesChangedAuthority(t *testing.T) {
	for _, test := range []struct {
		name        string
		mergeResult func() *dejavu.MergeResult
		change      func(t *testing.T, config symemo.Config)
		verify      func(t *testing.T, runtime *symemoRuntime)
	}{
		{
			name: "upsert",
			mergeResult: func() *dejavu.MergeResult {
				return &dejavu.MergeResult{Upserts: []*entity.File{{Path: "/storage/siyuanmemo/elements/" + runtimeSymemoFixtureElementID + ".sme"}}}
			},
			change: func(t *testing.T, config symemo.Config) { rewriteRuntimeFixturePrompt(t, config, "synced upsert") },
			verify: func(t *testing.T, runtime *symemoRuntime) { assertRuntimeFixturePrompt(t, runtime, "synced upsert") },
		},
		{
			name: "conflict",
			mergeResult: func() *dejavu.MergeResult {
				return &dejavu.MergeResult{Conflicts: []*entity.File{{Path: "/storage/siyuanmemo/elements/" + runtimeSymemoFixtureElementID + ".sme"}}}
			},
			change: func(t *testing.T, config symemo.Config) { rewriteRuntimeFixturePrompt(t, config, "synced conflict") },
			verify: func(t *testing.T, runtime *symemoRuntime) { assertRuntimeFixturePrompt(t, runtime, "synced conflict") },
		},
		{
			name: "remove",
			mergeResult: func() *dejavu.MergeResult {
				return &dejavu.MergeResult{Removes: []*entity.File{{Path: "/storage/siyuanmemo/elements/" + runtimeSymemoFixtureElementID + ".sme"}}}
			},
			change: func(t *testing.T, config symemo.Config) {
				if err := os.Remove(filepath.Join(config.StorageRoot, "elements", runtimeSymemoFixtureElementID+".sme")); err != nil {
					t.Fatal(err)
				}
			},
			verify: func(t *testing.T, runtime *symemoRuntime) {
				_, err := runtime.query(t.Context(), symemo.Query{Kind: symemo.QueryElement, ElementID: runtimeSymemoFixtureElementID})
				assertSymemoRuntimeCode(t, err, symemo.ErrElementSourceUnavailable)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := runtimeSymemoFixtureConfig(t)
			hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) { return symemo.NewEngine(ctx, config) })
			if err := hostRuntime.initialize(t.Context()); err != nil {
				t.Fatal(err)
			}
			original := workspaceSymemoRuntime
			workspaceSymemoRuntime = hostRuntime
			t.Cleanup(func() {
				workspaceSymemoRuntime = original
				_ = hostRuntime.Close()
			})
			test.change(t, config)
			rebuildSymemoAfterSyncMerge(test.mergeResult(), symemoSyncDrain{})
			test.verify(t, hostRuntime)
		})
	}
}

func TestRebuildSymemoAfterSyncMergePublishesChangedCreatedHTMLTopic(t *testing.T) {
	config := runtimeSymemoFixtureConfig(t)
	var opens atomic.Int32
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens.Add(1)
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	original := workspaceSymemoRuntime
	workspaceSymemoRuntime = hostRuntime
	t.Cleanup(func() {
		workspaceSymemoRuntime = original
		_ = hostRuntime.Close()
	})

	created, err := hostRuntime.createElement(t.Context(), symemo.CreateElementCommand{
		Kind:        symemo.CreateElementAddNewTopic,
		AddNewTopic: symemo.AddNewTopicCommand{Title: "Before Sync", HTML: "<p>Body</p>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.StorageRoot, "elements", created.ElementID+".sme")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var element symemo.Element
	if err = json.Unmarshal(data, &element); err != nil {
		t.Fatal(err)
	}
	element.Title = "After Sync"
	data, err = json.MarshalIndent(element, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	rebuildSymemoAfterSyncMerge(&dejavu.MergeResult{Upserts: []*entity.File{{Path: "/storage/siyuanmemo/elements/" + created.ElementID + ".sme"}}}, symemoSyncDrain{})
	if opens.Load() != 2 {
		t.Fatalf("sync replacement opened %d Engines", opens.Load())
	}
	query, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryElement, ElementID: created.ElementID})
	if err != nil || query.Element == nil || query.Element.Title != "After Sync" || query.Element.ScheduleProjection == nil || query.Element.ScheduleProjection.AdoptedTerminalID != created.EventID {
		t.Fatalf("sync replacement Topic = %#v, err=%v", query.Element, err)
	}
}

func TestRebuildSymemoAfterSyncMergeDrainsLeaseAndExcludesStaleEngine(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	hostRuntime.mu.Lock()
	first := hostRuntime.engine
	hostRuntime.mu.Unlock()
	original := workspaceSymemoRuntime
	workspaceSymemoRuntime = hostRuntime
	t.Cleanup(func() {
		workspaceSymemoRuntime = original
		_ = hostRuntime.Close()
	})

	operationStarted := make(chan struct{})
	releaseOperation := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseOperation) }) }
	t.Cleanup(release)
	operationDone := make(chan error, 1)
	go func() {
		operationDone <- hostRuntime.withEngine(func(engine *symemo.Engine) error {
			if engine != first {
				return errors.New("active operation received stale replacement")
			}
			close(operationStarted)
			<-releaseOperation
			return nil
		})
	}()
	<-operationStarted
	rebuildDone := make(chan struct{})
	go func() {
		rebuildSymemoAfterSyncMerge(&dejavu.MergeResult{Upserts: []*entity.File{{Path: "/storage/siyuanmemo/reviews/2026-07.smr"}}}, symemoSyncDrain{})
		close(rebuildDone)
	}()
	for attempt := 0; attempt < 1000; attempt++ {
		hostRuntime.mu.Lock()
		draining := hostRuntime.draining
		hostRuntime.mu.Unlock()
		if draining {
			break
		}
		if attempt == 999 {
			t.Fatal("sync rebuild did not begin draining")
		}
		runtime.Gosched()
	}
	if _, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err == nil {
		t.Fatal("sync draining admitted stale Engine work")
	} else {
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}
	select {
	case <-rebuildDone:
		t.Fatal("sync rebuild completed before active lease drained")
	default:
	}
	release()
	if err := <-operationDone; err != nil {
		t.Fatal(err)
	}
	<-rebuildDone
	hostRuntime.mu.Lock()
	replacement := hostRuntime.engine
	hostRuntime.mu.Unlock()
	if replacement == nil || replacement == first || opens != 2 {
		t.Fatalf("replacement=%p first=%p opens=%d", replacement, first, opens)
	}
}

func TestRebuildSymemoAfterSyncMergeFailureLeavesRuntimeUnavailable(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{StorageRoot: filepath.Join(root, "storage"), IndexRoot: filepath.Join(root, "temp", "siyuanmemo")}
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		if opens == 2 {
			return nil, errors.New("injected sync replacement failure")
		}
		return symemo.NewEngine(ctx, config)
	})
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	original := workspaceSymemoRuntime
	workspaceSymemoRuntime = hostRuntime
	t.Cleanup(func() {
		workspaceSymemoRuntime = original
		_ = hostRuntime.Close()
	})

	rebuildSymemoAfterSyncMerge(&dejavu.MergeResult{Conflicts: []*entity.File{{Path: "/storage/siyuanmemo/elements/root.sme"}}}, symemoSyncDrain{})
	if _, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); err == nil {
		t.Fatal("failed sync rebuild left Runtime available")
	} else {
		assertSymemoRuntimeCode(t, err, symemo.ErrProjectionRebuildFailed)
	}
	if opens != 2 {
		t.Fatalf("failed sync rebuild opened %d Engines", opens)
	}
}

func parseGoTestFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func mustGoFunctionBody(t *testing.T, file *ast.File, name string) *ast.BlockStmt {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function.Body
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func mustCommandRunBody(t *testing.T, file *ast.File, variable string) *ast.BlockStmt {
	t.Helper()
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != variable || len(value.Values) != 1 {
				continue
			}
			address, ok := value.Values[0].(*ast.UnaryExpr)
			if !ok {
				continue
			}
			literal, ok := address.X.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, element := range literal.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, keyOK := field.Key.(*ast.Ident)
				run, runOK := field.Value.(*ast.FuncLit)
				if keyOK && runOK && key.Name == "Run" {
					return run.Body
				}
			}
		}
	}
	t.Fatalf("command %s Run function not found", variable)
	return nil
}

func goCallNames(node ast.Node) []string {
	var names []string
	ast.Inspect(node, func(current ast.Node) bool {
		if call, ok := current.(*ast.CallExpr); ok {
			if name := goExpressionName(call.Fun); name != "" {
				names = append(names, name)
			}
		}
		return true
	})
	return names
}

func goExpressionName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		prefix := goExpressionName(value.X)
		if prefix == "" {
			return value.Sel.Name
		}
		return prefix + "." + value.Sel.Name
	case *ast.ParenExpr:
		return goExpressionName(value.X)
	}
	return ""
}

func assertGoCallOrder(t *testing.T, path string, calls, expected []string) {
	t.Helper()
	next := 0
	for _, call := range calls {
		if next < len(expected) && call == expected[next] {
			next++
		}
	}
	if next != len(expected) {
		t.Errorf("%s startup calls = %#v, missing ordered suffix %#v", path, calls, expected[next:])
	}
}

func countGoCall(calls []string, expected string) int {
	count := 0
	for _, call := range calls {
		if call == expected {
			count++
		}
	}
	return count
}

func hasGoCallWithArgument(node ast.Node, callName, argumentName string) bool {
	found := false
	ast.Inspect(node, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok || goExpressionName(call.Fun) != callName {
			return true
		}
		for _, argument := range call.Args {
			if goExpressionName(argument) == argumentName {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func goResultIncludesSymemoEngine(results *ast.FieldList) bool {
	if results == nil {
		return false
	}
	for _, field := range results.List {
		pointer, ok := field.Type.(*ast.StarExpr)
		if ok && goExpressionName(pointer.X) == "symemo.Engine" {
			return true
		}
	}
	return false
}

func assertSymemoRuntimeCode(t *testing.T, err error, expected symemo.ErrorCode) {
	t.Helper()
	domainErr, ok := symemo.AsDomainError(err)
	if !ok || domainErr.Code != expected {
		t.Fatalf("Runtime error = %v, want %s", err, expected)
	}
}

const runtimeSymemoFixtureElementID = "20260719010101-abcdefg"

func runtimeSymemoFixtureConfig(t *testing.T) symemo.Config {
	t.Helper()
	root := t.TempDir()
	for _, relative := range []string{
		"elements/" + runtimeSymemoFixtureElementID + ".sme",
		"reviews/2026-07.smr",
		"scheduler/collection.json",
		"scheduler/simple-v1.json",
		"scheduler/fsrs-v1.json",
		"scheduler/topic-afactor-v1.json",
		"scheduler/arena-v1.json",
		"scheduler/learning-day.json",
	} {
		source := filepath.Join("symemo", "testdata", filepath.FromSlash(relative))
		target := filepath.Join(root, filepath.FromSlash(relative))
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	location := time.FixedZone("CST", 8*60*60)
	clock := time.Date(2026, time.July, 19, 9, 0, 0, 0, location)
	return symemo.Config{
		StorageRoot:   root,
		IndexRoot:     filepath.Join(root, "temp", "siyuanmemo"),
		SchedulerRoot: filepath.Join(root, "scheduler"),
		Now:           func() time.Time { return clock },
		Location:      location,
	}
}

func rewriteRuntimeFixturePrompt(t *testing.T, config symemo.Config, prompt string) {
	t.Helper()
	path := filepath.Join(config.StorageRoot, "elements", runtimeSymemoFixtureElementID+".sme")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var element symemo.Element
	if err = json.Unmarshal(data, &element); err != nil {
		t.Fatal(err)
	}
	element.Payload.Prompt = prompt
	data, err = json.Marshal(element)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func assertRuntimeFixturePrompt(t *testing.T, runtime *symemoRuntime, prompt string) {
	t.Helper()
	result, err := runtime.query(t.Context(), symemo.Query{Kind: symemo.QueryElement, ElementID: runtimeSymemoFixtureElementID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Element == nil || result.Element.Payload.Prompt != prompt {
		t.Fatalf("rebuilt Element = %#v, want prompt %q", result.Element, prompt)
	}
}

func TestSymemoRuntimeCloseRequiresExplicitReinitialization(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{
		StorageRoot: filepath.Join(root, "storage"),
		IndexRoot:   filepath.Join(root, "temp", "siyuanmemo"),
	}
	opens := 0
	hostRuntime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		opens++
		return symemo.NewEngine(ctx, config)
	})

	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	hostRuntime.mu.Lock()
	first := hostRuntime.engine
	hostRuntime.mu.Unlock()
	if err := hostRuntime.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := hostRuntime.query(t.Context(), symemo.Query{Kind: symemo.QueryCurrentSession}); !errors.Is(err, errSymemoRuntimeUninitialized) {
		t.Fatalf("closed Runtime query error = %v", err)
	}
	if opens != 1 {
		t.Fatalf("closed Runtime reopened %d Engines", opens)
	}
	if err := hostRuntime.initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	hostRuntime.mu.Lock()
	second := hostRuntime.engine
	hostRuntime.mu.Unlock()
	if second == nil || second == first || opens != 2 {
		t.Fatalf("explicit reinitialization engine=%p first=%p opens=%d", second, first, opens)
	}
	t.Cleanup(func() { _ = hostRuntime.Close() })
}
