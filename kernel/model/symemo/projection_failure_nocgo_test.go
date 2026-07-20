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

//go:build !cgo

package symemo

import (
	"os"
	"path/filepath"
	"testing"
)

func installProjectionRefreshFailure(t *testing.T, _ *Engine, config Config) func() {
	t.Helper()
	indexPath := config.IndexPath()
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(indexPath, 0755); err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		if err := os.RemoveAll(indexPath); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(restore)
	return restore
}
