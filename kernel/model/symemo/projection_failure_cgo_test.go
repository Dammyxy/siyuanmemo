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

//go:build cgo

package symemo

import "testing"

func installProjectionRefreshFailure(t *testing.T, engine *Engine, _ Config) func() {
	t.Helper()
	const trigger = "symemo_test_fail_projection_refresh"
	_, err := engine.index.db.Exec(`CREATE TRIGGER ` + trigger + `
BEFORE DELETE ON elements
BEGIN
    SELECT RAISE(ABORT, 'forced projection refresh failure');
END`)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		if _, dropErr := engine.index.db.Exec(`DROP TRIGGER ` + trigger); dropErr != nil {
			t.Fatal(dropErr)
		}
	}
	t.Cleanup(restore)
	return restore
}
