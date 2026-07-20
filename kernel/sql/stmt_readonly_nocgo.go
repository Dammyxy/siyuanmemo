// SiYuan - Refactor your thinking
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build !cgo

package sql

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

func checkSQLiteReadonly(*sqlite3.SQLiteConn, string) error {
	return errors.New("SQL read-only validation requires CGO")
}
