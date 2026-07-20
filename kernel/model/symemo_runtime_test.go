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
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/88250/lute/parse"
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
		"scheduler/arena-v1.json",
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
