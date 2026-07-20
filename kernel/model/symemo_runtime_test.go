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
	"testing"

	"github.com/siyuan-note/siyuan/kernel/model/symemo"
)

func TestSymemoRuntimeCloseResetsEngine(t *testing.T) {
	root := t.TempDir()
	config := symemo.Config{
		StorageRoot: filepath.Join(root, "storage"),
		IndexRoot:   filepath.Join(root, "temp", "siyuanmemo"),
	}
	runtime := newSymemoRuntime(func(ctx context.Context) (*symemo.Engine, error) {
		return symemo.NewEngine(ctx, config)
	})

	first, err := runtime.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if same, err := runtime.Get(context.Background()); err != nil || same != first {
		t.Fatalf("runtime should reuse its open Engine: engine=%p err=%v", same, err)
	}
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("runtime reused an Engine after Close")
	}
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
}
