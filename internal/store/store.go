// Package store owns Boombrick's SQLite database and its schema migrations.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mattn/go-sqlite3"
)

// Store is the open database.
type Store struct {
	DB *sql.DB
	// SetAside names where a damaged database was moved before a new one was
	// made in its place; "" when none was.
	SetAside string
}

// Open opens or creates the database at path and brings its schema up to date.
// Each connection keeps 512 KiB of pages rather than SQLite's 2 MiB: the
// cache is C memory, outside Go's memory limit.
//
// A database too damaged to open - on a FAT card, a power cut mid-write can
// leave it short of its own pages - is moved aside, kept for recovery, and a
// new one made: the library is scanned again, and Boombrick starts instead of
// failing on every launch.
func Open(path string) (*Store, error) {
	s, err := open(path)
	if err == nil || !corrupt(err) {
		return s, err
	}
	aside := fmt.Sprintf("%s.corrupt-%s", path, time.Now().Format("20060102-150405"))
	if rerr := os.Rename(path, aside); rerr != nil {
		return nil, errors.Join(err, rerr)
	}
	for _, x := range []string{"-wal", "-shm"} {
		os.Rename(path+x, aside+x)
	}
	s, err = open(path)
	if s != nil {
		s.SetAside = aside
	}
	return s, err
}

// corrupt reports whether err is SQLite finding the file damaged or not a
// database at all.
func corrupt(err error) bool {
	var se sqlite3.Error
	return errors.As(err, &se) && (se.Code == sqlite3.ErrCorrupt || se.Code == sqlite3.ErrNotADB)
}

func open(path string) (*Store, error) {
	dsn := path + "?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_cache_size=-512"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return &Store{DB: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.DB.Close()
}
