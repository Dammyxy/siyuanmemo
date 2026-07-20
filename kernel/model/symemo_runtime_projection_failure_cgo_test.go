// SiYuan - Refactor your thinking
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build cgo

package model

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/siyuan-note/siyuan/kernel/conf"
	"github.com/siyuan-note/siyuan/kernel/model/symemo"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
	"golang.org/x/time/rate"
)

func TestSymemoBlockReferenceReaderRejectsEncryptedReindexBeforeLoad(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	originalDataDir := util.DataDir
	originalBlockTreeDBPath := util.BlockTreeDBPath
	originalConf := Conf
	originalLimiter := searchTreeLimiter
	util.DataDir = dataDir
	util.BlockTreeDBPath = filepath.Join(t.TempDir(), "blocktree.db")
	Conf = NewAppConf()
	Conf.FileTree = conf.NewFileTree()
	searchTreeLimiter = rate.NewLimiter(rate.Inf, 1)
	t.Cleanup(func() {
		treenode.CloseDatabase()
		searchTreeLimiter = originalLimiter
		Conf = originalConf
		util.BlockTreeDBPath = originalBlockTreeDBPath
		util.DataDir = originalDataDir
	})

	const (
		boxID   = "20260721100000-cryptnb"
		rootID  = "20260721100000-rootdoc"
		blockID = "20260721100000-targetx"
	)
	boxConf := conf.NewBoxConf()
	boxConf.Encrypted = true
	boxConf.Closed = false
	if err := (&Box{ID: boxID}).SaveConf(boxConf); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(dataDir, boxID, rootID+".sy")
	source := []byte(`{"id":"` + rootID + `","child":"` + blockID + `"}`)
	if err := os.WriteFile(sourcePath, source, 0644); err != nil {
		t.Fatal(err)
	}
	treenode.CloseDatabase()
	treenode.InitBlockTree(true)

	reader := &siyuanBlockReferenceReader{
		lookupMany:  func([]string) map[string]*treenode.BlockTree { return nil },
		load:        loadSymemoBlockReference,
		isEncrypted: IsEncryptedBox,
	}
	resolution, err := reader.Load(t.Context(), blockID)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != symemo.MaterialSourceAvailable || !resolution.Encrypted || resolution.CurrentNotebookID != boxID || resolution.CurrentPath != "/"+rootID+".sy" {
		t.Fatalf("encrypted reindex resolution = %#v", resolution)
	}
	if indexed := treenode.GetBlockTree(blockID); indexed != nil {
		t.Fatalf("encrypted reindex published block tree %#v", indexed)
	}
	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, source) {
		t.Fatal("encrypted reindex mutated source")
	}
}

func installRuntimeProjectionRefreshFailure(t *testing.T, config symemo.Config) func() {
	t.Helper()
	db, err := sql.Open("sqlite3", config.IndexPath()+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const trigger = "symemo_runtime_test_fail_projection_refresh"
	if _, err = db.Exec(`CREATE TRIGGER ` + trigger + `
BEFORE DELETE ON elements
BEGIN
    SELECT RAISE(ABORT, 'forced Runtime projection refresh failure');
END`); err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		restoreDB, openErr := sql.Open("sqlite3", config.IndexPath()+"?_busy_timeout=5000")
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer restoreDB.Close()
		if _, dropErr := restoreDB.Exec(`DROP TRIGGER ` + trigger); dropErr != nil {
			t.Fatal(dropErr)
		}
	}
	t.Cleanup(restore)
	return restore
}
