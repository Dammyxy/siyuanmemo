// SiYuan - Refactor your thinking
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build cgo

package sql

import (
	"errors"
	"fmt"

	"github.com/mattn/go-sqlite3"
)

func checkSQLiteReadonly(conn *sqlite3.SQLiteConn, stmt string) error {
	driverStmt, err := conn.Prepare(stmt)
	if err != nil {
		return err
	}
	defer driverStmt.Close()

	sqliteStmt, ok := driverStmt.(*sqlite3.SQLiteStmt)
	if !ok {
		return fmt.Errorf("SQL driver statement type is unexpected: %T", driverStmt)
	}
	if !sqliteStmt.Readonly() {
		return errors.New("SQL statement is not read-only")
	}
	return nil
}
