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
	"path/filepath"
	"sync"

	"github.com/siyuan-note/siyuan/kernel/model/symemo"
	"github.com/siyuan-note/siyuan/kernel/util"
)

type symemoRuntime struct {
	mu     sync.Mutex
	engine *symemo.Engine
	open   func(context.Context) (*symemo.Engine, error)
}

func newSymemoRuntime(open func(context.Context) (*symemo.Engine, error)) *symemoRuntime {
	return &symemoRuntime{open: open}
}

func (runtime *symemoRuntime) Get(ctx context.Context) (*symemo.Engine, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.engine != nil {
		return runtime.engine, nil
	}
	engine, err := runtime.open(ctx)
	if err != nil {
		return nil, err
	}
	runtime.engine = engine
	return engine, nil
}

func (runtime *symemoRuntime) Close() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	engine := runtime.engine
	runtime.engine = nil
	if engine == nil {
		return nil
	}
	return engine.Close()
}

var workspaceSymemoRuntime = newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
	return symemo.NewEngine(ctx, symemo.Config{
		StorageRoot: filepath.Join(util.DataDir, "storage", "siyuanmemo"),
		IndexRoot:   filepath.Join(util.TempDir, "siyuanmemo"),
	})
})

func GetSymemoEngine() (*symemo.Engine, error) {
	return workspaceSymemoRuntime.Get(context.Background())
}

func CloseSymemoEngine() error {
	return workspaceSymemoRuntime.Close()
}
