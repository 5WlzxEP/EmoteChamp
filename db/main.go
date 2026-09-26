package db

import (
	"database/sql"
	"embed"
	"errors"

	_ "modernc.org/sqlite"

	"io/fs"
	"strings"
)

var DB *Queries

func Init(DSN string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", DSN)
	if err != nil {
		return nil, errors.New("error opening db: " + err.Error())
	}

	err = db.Ping()
	if err != nil {
		db.Close()
		return nil, errors.New("error pinging db: " + err.Error())
	}

	err = check(db)
	if err != nil {
		db.Close()
		return nil, errors.New("error creating tables: " + err.Error())
	}

	DB = New(db)

	return db, err
}

func check(db *sql.DB) error {
	err := fs.WalkDir(tables, ".", func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return err
		}
		data, err := tables.ReadFile(path)
		if err != nil {
			return err
		}
		err = checkTables(db, string(data))
		return err
	})

	return err
}

func checkTables(db *sql.DB, table string) error {
	_, err := db.Exec(table)
	if err != nil {
		return err
	}

	return nil
}

//go:embed schema
var tables embed.FS

//go:generate go tool sqlc generate -f "./sqlc.yaml"
