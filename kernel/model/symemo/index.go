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

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

const projectionSchemaVersion = 6

var errProjectionNotFound = errors.New("projection not found")

type projectionIndex struct {
	path string
	db   *sql.DB
	mu   sync.RWMutex
}

type projectionSnapshot struct {
	Elements                 map[string]Element
	Tree                     []ElementTreeNode
	Projections              map[string]SchedulingProjection
	FinalDrillProjections    map[string]FinalDrillProjection
	HistoryEventFingerprints map[string]bool
	BlockedElementIDs        map[string]bool
}

func openProjectionIndex(path string) (*projectionIndex, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	index := &projectionIndex{path: path}
	if err := index.openAndValidate(); err != nil {
		index.close()
		removeSQLiteFiles(path)
		if err = index.openFresh(); err != nil {
			return nil, err
		}
	}
	return index, nil
}

func (index *projectionIndex) openAndValidate() error {
	if _, err := os.Stat(index.path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite3", index.path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return err
	}
	index.db = db
	var health string
	if err = db.QueryRow("PRAGMA quick_check").Scan(&health); err != nil || health != "ok" {
		return fmt.Errorf("projection database health check failed: %s: %w", health, err)
	}
	var version string
	if err = db.QueryRow("SELECT value FROM metadata WHERE key = 'schema_version'").Scan(&version); err != nil {
		return err
	}
	if version != strconv.Itoa(projectionSchemaVersion) {
		return fmt.Errorf("projection schema version %s is incompatible", version)
	}
	return nil
}

func (index *projectionIndex) openFresh() error {
	db, err := sql.Open("sqlite3", index.path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return err
	}
	index.db = db
	statements := []string{
		"CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)",
		"CREATE TABLE elements (id TEXT PRIMARY KEY, source_json BLOB NOT NULL)",
		"CREATE TABLE tree (key TEXT PRIMARY KEY, tree_json BLOB NOT NULL)",
		"CREATE TABLE projections (element_id TEXT PRIMARY KEY, projection_json BLOB NOT NULL)",
		"CREATE TABLE final_drill_projections (element_id TEXT PRIMARY KEY, projection_json BLOB NOT NULL)",
		"CREATE TABLE history_event_fingerprints (fingerprint TEXT PRIMARY KEY)",
		"CREATE TABLE diagnostics (event_id TEXT NOT NULL, payload_hash TEXT NOT NULL, diagnostic_json BLOB NOT NULL, PRIMARY KEY(event_id, payload_hash))",
		"CREATE TABLE source_diagnostics (source_path TEXT NOT NULL, code TEXT NOT NULL, element_id TEXT NOT NULL, diagnostic_json BLOB NOT NULL, PRIMARY KEY(source_path, code, element_id))",
		"INSERT INTO metadata(key, value) VALUES('schema_version', ?)",
	}
	for i, statement := range statements {
		if i == len(statements)-1 {
			_, err = db.Exec(statement, strconv.Itoa(projectionSchemaVersion))
		} else {
			_, err = db.Exec(statement)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (index *projectionIndex) replaceAll(ctx context.Context, build projectionBuild) error {
	index.mu.Lock()
	defer index.mu.Unlock()
	if index.db == nil {
		return errors.New("projection database is closed")
	}
	tx, err := index.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"elements", "tree", "projections", "final_drill_projections", "history_event_fingerprints", "diagnostics", "source_diagnostics"} {
		if _, err = tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	ids := make([]string, 0, len(build.Elements))
	for id := range build.Elements {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		data, marshalErr := json.Marshal(build.Elements[id])
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.Exec("INSERT INTO elements(id, source_json) VALUES(?, ?)", id, data); err != nil {
			return err
		}
	}
	treeData, err := json.Marshal(build.Tree)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO tree(key, tree_json) VALUES('default', ?)", treeData); err != nil {
		return err
	}
	projectionIDs := make([]string, 0, len(build.Projections))
	for id := range build.Projections {
		projectionIDs = append(projectionIDs, id)
	}
	sort.Strings(projectionIDs)
	for _, id := range projectionIDs {
		data, marshalErr := json.Marshal(build.Projections[id])
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.Exec("INSERT INTO projections(element_id, projection_json) VALUES(?, ?)", id, data); err != nil {
			return err
		}
	}
	finalDrillIDs := make([]string, 0, len(build.FinalDrillProjections))
	for id := range build.FinalDrillProjections {
		finalDrillIDs = append(finalDrillIDs, id)
	}
	sort.Strings(finalDrillIDs)
	for _, id := range finalDrillIDs {
		data, marshalErr := json.Marshal(build.FinalDrillProjections[id])
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.Exec("INSERT INTO final_drill_projections(element_id, projection_json) VALUES(?, ?)", id, data); err != nil {
			return err
		}
	}
	for _, fingerprint := range build.HistoryEventFingerprints {
		if _, err = tx.Exec("INSERT INTO history_event_fingerprints(fingerprint) VALUES(?)", fingerprint); err != nil {
			return err
		}
	}
	for _, diagnostic := range build.EventDiagnostics {
		data, marshalErr := json.Marshal(diagnostic)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.Exec("INSERT INTO diagnostics(event_id, payload_hash, diagnostic_json) VALUES(?, ?, ?)", diagnostic.EventID, diagnostic.PayloadHash, data); err != nil {
			return err
		}
	}
	for _, diagnostic := range build.SourceDiagnostics {
		data, marshalErr := json.Marshal(diagnostic)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.Exec("INSERT INTO source_diagnostics(source_path, code, element_id, diagnostic_json) VALUES(?, ?, ?, ?)", diagnostic.SourcePath, diagnostic.Code, diagnostic.ElementID, data); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (index *projectionIndex) sourceDiagnostics() ([]ElementSourceDiagnostic, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	rows, err := index.db.Query("SELECT diagnostic_json FROM source_diagnostics ORDER BY source_path, code, element_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var diagnostics []ElementSourceDiagnostic
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var diagnostic ElementSourceDiagnostic
		if err = json.Unmarshal(data, &diagnostic); err != nil {
			return nil, err
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics, rows.Err()
}

func (index *projectionIndex) tree() ([]ElementTreeNode, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	var data []byte
	if err := index.db.QueryRow("SELECT tree_json FROM tree WHERE key = 'default'").Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errProjectionNotFound
		}
		return nil, err
	}
	var nodes []ElementTreeNode
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (index *projectionIndex) projection(elementID string) (SchedulingProjection, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	var data []byte
	if err := index.db.QueryRow("SELECT projection_json FROM projections WHERE element_id = ?", elementID).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SchedulingProjection{}, errProjectionNotFound
		}
		return SchedulingProjection{}, err
	}
	var projection SchedulingProjection
	if err := json.Unmarshal(data, &projection); err != nil {
		return SchedulingProjection{}, err
	}
	return projection, nil
}

func (index *projectionIndex) finalDrillProjection(elementID string) (FinalDrillProjection, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	var data []byte
	if err := index.db.QueryRow("SELECT projection_json FROM final_drill_projections WHERE element_id = ?", elementID).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FinalDrillProjection{}, errProjectionNotFound
		}
		return FinalDrillProjection{}, err
	}
	var projection FinalDrillProjection
	if err := json.Unmarshal(data, &projection); err != nil {
		return FinalDrillProjection{}, err
	}
	return projection, nil
}

func (index *projectionIndex) element(elementID string) (Element, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	var data []byte
	if err := index.db.QueryRow("SELECT source_json FROM elements WHERE id = ?", elementID).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Element{}, errProjectionNotFound
		}
		return Element{}, err
	}
	var element Element
	if err := json.Unmarshal(data, &element); err != nil {
		return Element{}, err
	}
	return element, nil
}

func (index *projectionIndex) snapshot() (projectionSnapshot, error) {
	index.mu.RLock()
	defer index.mu.RUnlock()
	snapshot := projectionSnapshot{Elements: map[string]Element{}, Projections: map[string]SchedulingProjection{}, FinalDrillProjections: map[string]FinalDrillProjection{}, HistoryEventFingerprints: map[string]bool{}, BlockedElementIDs: map[string]bool{}}
	elementRows, err := index.db.Query("SELECT id, source_json FROM elements ORDER BY id")
	if err != nil {
		return projectionSnapshot{}, err
	}
	defer elementRows.Close()
	for elementRows.Next() {
		var id string
		var data []byte
		if err = elementRows.Scan(&id, &data); err != nil {
			return projectionSnapshot{}, err
		}
		var element Element
		if err = json.Unmarshal(data, &element); err != nil {
			return projectionSnapshot{}, err
		}
		snapshot.Elements[id] = element
	}
	if err = elementRows.Err(); err != nil {
		return projectionSnapshot{}, err
	}
	projectionRows, err := index.db.Query("SELECT element_id, projection_json FROM projections ORDER BY element_id")
	if err != nil {
		return projectionSnapshot{}, err
	}
	defer projectionRows.Close()
	for projectionRows.Next() {
		var id string
		var data []byte
		if err = projectionRows.Scan(&id, &data); err != nil {
			return projectionSnapshot{}, err
		}
		var projection SchedulingProjection
		if err = json.Unmarshal(data, &projection); err != nil {
			return projectionSnapshot{}, err
		}
		snapshot.Projections[id] = projection
	}
	if err = projectionRows.Err(); err != nil {
		return projectionSnapshot{}, err
	}
	finalDrillRows, err := index.db.Query("SELECT element_id, projection_json FROM final_drill_projections ORDER BY element_id")
	if err != nil {
		return projectionSnapshot{}, err
	}
	defer finalDrillRows.Close()
	for finalDrillRows.Next() {
		var id string
		var data []byte
		if err = finalDrillRows.Scan(&id, &data); err != nil {
			return projectionSnapshot{}, err
		}
		var projection FinalDrillProjection
		if err = json.Unmarshal(data, &projection); err != nil {
			return projectionSnapshot{}, err
		}
		snapshot.FinalDrillProjections[id] = projection
	}
	if err = finalDrillRows.Err(); err != nil {
		return projectionSnapshot{}, err
	}
	historyRows, err := index.db.Query("SELECT fingerprint FROM history_event_fingerprints ORDER BY fingerprint")
	if err != nil {
		return projectionSnapshot{}, err
	}
	defer historyRows.Close()
	for historyRows.Next() {
		var fingerprint string
		if err = historyRows.Scan(&fingerprint); err != nil {
			return projectionSnapshot{}, err
		}
		snapshot.HistoryEventFingerprints[fingerprint] = true
	}
	if err = historyRows.Err(); err != nil {
		return projectionSnapshot{}, err
	}
	var treeData []byte
	if err = index.db.QueryRow("SELECT tree_json FROM tree WHERE key = 'default'").Scan(&treeData); err == nil {
		if err = json.Unmarshal(treeData, &snapshot.Tree); err != nil {
			return projectionSnapshot{}, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return projectionSnapshot{}, err
	}
	blockedRows, err := index.db.Query("SELECT element_id FROM source_diagnostics WHERE code = ? ORDER BY element_id", schedulingHistoryUnavailableCode)
	if err != nil {
		return projectionSnapshot{}, err
	}
	defer blockedRows.Close()
	for blockedRows.Next() {
		var id string
		if err = blockedRows.Scan(&id); err != nil {
			return projectionSnapshot{}, err
		}
		snapshot.BlockedElementIDs[id] = true
	}
	if err = blockedRows.Err(); err != nil {
		return projectionSnapshot{}, err
	}
	return snapshot, nil
}

func (index *projectionIndex) close() error {
	index.mu.Lock()
	defer index.mu.Unlock()
	if index.db == nil {
		return nil
	}
	err := index.db.Close()
	index.db = nil
	return err
}

func removeSQLiteFiles(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}
