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
	"path/filepath"
	"sync"

	"github.com/88250/lute/parse"
	"github.com/siyuan-note/siyuan/kernel/model/symemo"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

var errSymemoRuntimeUninitialized = errors.New("SiYuanMemo Runtime is not initialized")

type symemoRuntimeState uint8

const (
	symemoRuntimeUninitialized symemoRuntimeState = iota
	symemoRuntimeAvailable
	symemoRuntimeUnavailable
)

type symemoRuntime struct {
	mu              sync.Mutex
	cond            *sync.Cond
	state           symemoRuntimeState
	draining        bool
	active          int
	engine          *symemo.Engine
	failure         error
	open            func(context.Context) (*symemo.Engine, error)
	waitForStorages func()
	waitGeneration  uint64
}

func newSymemoRuntime(open func(context.Context) (*symemo.Engine, error)) *symemoRuntime {
	runtime := &symemoRuntime{open: open, waitForStorages: func() {}}
	runtime.cond = sync.NewCond(&runtime.mu)
	return runtime
}

func initializeSymemo(ctx context.Context, runtime *symemoRuntime, config symemo.Config, waitForStorages func()) error {
	waitForStorages()
	runtime.setStorageWaiter(waitForStorages)
	if err := config.BootstrapSchedulerConfig(); err != nil {
		runtime.latchFailure(err)
		return err
	}
	return runtime.initialize(ctx)
}

func (runtime *symemoRuntime) setStorageWaiter(waitForStorages func()) {
	runtime.mu.Lock()
	runtime.waitForStorages = waitForStorages
	runtime.waitGeneration++
	runtime.mu.Unlock()
}

func (runtime *symemoRuntime) latchFailure(err error) {
	runtime.mu.Lock()
	runtime.state = symemoRuntimeUnavailable
	runtime.failure = err
	runtime.mu.Unlock()
}

func (runtime *symemoRuntime) initialize(ctx context.Context) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.state == symemoRuntimeAvailable {
		return nil
	}
	if runtime.state == symemoRuntimeUnavailable {
		return projectionRebuildError(runtime.failure)
	}
	engine, err := runtime.open(ctx)
	if err != nil {
		runtime.state = symemoRuntimeUnavailable
		runtime.failure = err
		return err
	}
	runtime.engine = engine
	runtime.failure = nil
	runtime.state = symemoRuntimeAvailable
	return nil
}

func (runtime *symemoRuntime) query(ctx context.Context, query symemo.Query) (symemo.QueryResult, error) {
	engine, release, err := runtime.lease()
	if err != nil {
		return symemo.QueryResult{}, err
	}
	result, err := engine.Query(ctx, query)
	release(err)
	return result, err
}

func (runtime *symemoRuntime) learningAction(ctx context.Context, action symemo.LearningAction) (symemo.LearningResult, error) {
	engine, release, err := runtime.lease()
	if err != nil {
		return symemo.LearningResult{}, err
	}
	result, err := engine.RunLearningAction(ctx, action)
	release(err)
	return result, err
}

func (runtime *symemoRuntime) withEngine(operation func(*symemo.Engine) error) error {
	engine, release, err := runtime.lease()
	if err != nil {
		return err
	}
	err = operation(engine)
	release(err)
	return err
}

func (runtime *symemoRuntime) lease() (*symemo.Engine, func(error), error) {
	for {
		runtime.mu.Lock()
		waitForStorages := runtime.waitForStorages
		generation := runtime.waitGeneration
		runtime.mu.Unlock()
		waitForStorages()
		runtime.mu.Lock()
		if generation == runtime.waitGeneration {
			break
		}
		runtime.mu.Unlock()
	}
	if runtime.state == symemoRuntimeUninitialized {
		runtime.mu.Unlock()
		return nil, nil, errSymemoRuntimeUninitialized
	}
	if runtime.state != symemoRuntimeAvailable || runtime.draining || runtime.engine == nil {
		failure := runtime.failure
		runtime.mu.Unlock()
		return nil, nil, projectionRebuildError(failure)
	}
	engine := runtime.engine
	runtime.active++
	runtime.mu.Unlock()
	return engine, func(operationErr error) {
		runtime.mu.Lock()
		runtime.active--
		if domainErr, ok := symemo.AsDomainError(operationErr); ok && domainErr.Code == symemo.ErrProjectionRefreshFailed {
			runtime.state = symemoRuntimeUnavailable
			runtime.failure = operationErr
		}
		if runtime.active == 0 {
			runtime.cond.Broadcast()
		}
		runtime.mu.Unlock()
	}, nil
}

func projectionRebuildError(cause error) error {
	return &symemo.DomainError{Code: symemo.ErrProjectionRebuildFailed, Message: "Element projection rebuild failed", Cause: cause}
}

type siyuanBlockReferenceReader struct {
	lookupMany  func([]string) map[string]*treenode.BlockTree
	load        func(string) (*parse.Tree, error)
	isEncrypted func(string) bool
}

func newSiyuanBlockReferenceReader() *siyuanBlockReferenceReader {
	return &siyuanBlockReferenceReader{
		lookupMany:  treenode.GetBlockTrees,
		load:        loadSymemoBlockReference,
		isEncrypted: IsEncryptedBox,
	}
}

type symemoEncryptedBlockError struct {
	blockID string
	boxID   string
	path    string
}

func (err *symemoEncryptedBlockError) Error() string {
	return "encrypted SiYuanMemo block source is unsupported"
}

func loadSymemoBlockReference(blockID string) (*parse.Tree, error) {
	return loadTreeByBlockIDWithReindexInBoxGuarded(blockID, "", func(tree *treenode.BlockTree) error {
		if !IsEncryptedBox(tree.BoxID) {
			return nil
		}
		return &symemoEncryptedBlockError{blockID: blockID, boxID: tree.BoxID, path: tree.Path}
	})
}

func (reader *siyuanBlockReferenceReader) LookupMany(ctx context.Context, blockIDs []string) (map[string]symemo.BlockReferenceResolution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trees := reader.lookupMany(blockIDs)
	resolutions := make(map[string]symemo.BlockReferenceResolution, len(blockIDs))
	for _, blockID := range blockIDs {
		resolution := symemo.BlockReferenceResolution{BlockID: blockID, Status: symemo.MaterialSourceUnresolved}
		if tree := trees[blockID]; tree != nil {
			resolution.Status = symemo.MaterialSourceAvailable
			resolution.CurrentNotebookID = tree.BoxID
			resolution.CurrentPath = tree.Path
			resolution.Encrypted = reader.isEncrypted(tree.BoxID)
		}
		resolutions[blockID] = resolution
	}
	return resolutions, nil
}

func (reader *siyuanBlockReferenceReader) Load(ctx context.Context, blockID string) (symemo.BlockReferenceResolution, error) {
	if err := ctx.Err(); err != nil {
		return symemo.BlockReferenceResolution{}, err
	}
	if tree := reader.lookupMany([]string{blockID})[blockID]; tree != nil && reader.isEncrypted(tree.BoxID) {
		return symemo.BlockReferenceResolution{BlockID: blockID, Status: symemo.MaterialSourceAvailable, CurrentNotebookID: tree.BoxID, CurrentPath: tree.Path, Encrypted: true}, nil
	}
	tree, err := reader.load(blockID)
	if err != nil {
		var encrypted *symemoEncryptedBlockError
		if errors.As(err, &encrypted) {
			return symemo.BlockReferenceResolution{
				BlockID:           encrypted.blockID,
				Status:            symemo.MaterialSourceAvailable,
				CurrentNotebookID: encrypted.boxID,
				CurrentPath:       encrypted.path,
				Encrypted:         true,
			}, nil
		}
		status := symemo.MaterialSourceUnresolved
		if errors.Is(err, ErrTreeNotFound) || errors.Is(err, ErrBlockNotFound) {
			status = symemo.MaterialSourceUnavailable
		}
		return symemo.BlockReferenceResolution{BlockID: blockID, Status: status}, nil
	}
	if tree == nil {
		return symemo.BlockReferenceResolution{BlockID: blockID, Status: symemo.MaterialSourceUnresolved}, nil
	}
	return symemo.BlockReferenceResolution{
		BlockID:           blockID,
		Status:            symemo.MaterialSourceAvailable,
		CurrentNotebookID: tree.Box,
		CurrentPath:       tree.Path,
		Encrypted:         reader.isEncrypted(tree.Box),
	}, nil
}

func (runtime *symemoRuntime) rebuild(ctx context.Context) error {
	runtime.mu.Lock()
	for runtime.draining {
		runtime.cond.Wait()
	}
	runtime.draining = true
	runtime.state = symemoRuntimeUnavailable
	for runtime.active > 0 {
		runtime.cond.Wait()
	}
	old := runtime.engine
	runtime.engine = nil
	runtime.mu.Unlock()

	if old != nil {
		if err := old.Close(); err != nil {
			runtime.finishRebuild(nil, err)
			return err
		}
	}
	replacement, err := runtime.open(ctx)
	runtime.finishRebuild(replacement, err)
	return err
}

func (runtime *symemoRuntime) finishRebuild(engine *symemo.Engine, err error) {
	runtime.mu.Lock()
	if err != nil {
		runtime.failure = err
		runtime.state = symemoRuntimeUnavailable
	} else {
		runtime.engine = engine
		runtime.failure = nil
		runtime.state = symemoRuntimeAvailable
	}
	runtime.draining = false
	runtime.cond.Broadcast()
	runtime.mu.Unlock()
}

func (runtime *symemoRuntime) Close() error {
	runtime.mu.Lock()
	for runtime.draining {
		runtime.cond.Wait()
	}
	runtime.draining = true
	runtime.state = symemoRuntimeUnavailable
	for runtime.active > 0 {
		runtime.cond.Wait()
	}
	engine := runtime.engine
	runtime.engine = nil
	runtime.mu.Unlock()
	var err error
	if engine != nil {
		err = engine.Close()
	}
	runtime.mu.Lock()
	runtime.state = symemoRuntimeUninitialized
	runtime.failure = nil
	runtime.draining = false
	runtime.cond.Broadcast()
	runtime.mu.Unlock()
	return err
}

func workspaceSymemoConfig() symemo.Config {
	return symemo.Config{
		StorageRoot:   filepath.Join(util.DataDir, "storage", "siyuanmemo"),
		IndexRoot:     filepath.Join(util.TempDir, "siyuanmemo"),
		SchedulerRoot: filepath.Join(util.DataDir, "storage", "siyuanmemo", "scheduler"),
		ReadOnly:      util.ReadOnly,
		BlockReader:   newSiyuanBlockReferenceReader(),
	}
}

var workspaceSymemoRuntime = newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
	return symemo.NewEngine(ctx, workspaceSymemoConfig())
})

func InitSymemo() error {
	return initializeSymemo(context.Background(), workspaceSymemoRuntime, workspaceSymemoConfig(), waitForSyncingStorages)
}

func QuerySymemo(ctx context.Context, query symemo.Query) (symemo.QueryResult, error) {
	return workspaceSymemoRuntime.query(ctx, query)
}

func RunSymemoLearningAction(ctx context.Context, action symemo.LearningAction) (symemo.LearningResult, error) {
	return workspaceSymemoRuntime.learningAction(ctx, action)
}

func CloseSymemoEngine() error {
	return workspaceSymemoRuntime.Close()
}
