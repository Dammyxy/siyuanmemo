// SiYuan - Refactor your thinking
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build !cgo

package model

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/siyuan-note/siyuan/kernel/model/symemo"
)

func installRuntimeProjectionRefreshFailure(t *testing.T, config symemo.Config) func() {
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
