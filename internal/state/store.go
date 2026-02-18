// Package state manages the SQLite database that tracks sync metadata between
// Apple Reminders and Home Assistant todo lists.
//
// Only this package may open or query the database. All other packages receive
// a [*Store] and call its methods.
package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

const schema = `
CREATE TABLE IF NOT EXISTS sync_items (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    reminders_uid      TEXT    NOT NULL DEFAULT '',
    ha_uid             TEXT    NOT NULL DEFAULT '',
    list_name          TEXT    NOT NULL,
    title              TEXT    NOT NULL,
    last_sync_hash     TEXT    NOT NULL DEFAULT '',
    reminders_modified TEXT    NOT NULL DEFAULT '',
    ha_modified        TEXT    NOT NULL DEFAULT '',
    last_synced_at     TEXT    NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_reminders_uid ON sync_items (reminders_uid) WHERE reminders_uid != '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_ha_uid         ON sync_items (ha_uid)         WHERE ha_uid != '';
CREATE INDEX        IF NOT EXISTS idx_list_name      ON sync_items (list_name);
`

// Item represents a single tracked task in the state database.
type Item struct {
	ID                int64
	RemindersUID      string
	HAUID             string
	ListName          string
	Title             string
	LastSyncHash      string
	RemindersModified time.Time
	HAModified        time.Time
	LastSyncedAt      time.Time
}

// Store is the SQLite-backed state repository.
type Store struct {
	db *sql.DB
}

// DefaultDBPath returns the default path for the state database:
// ~/.local/share/reminderrelay/state.db
func DefaultDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "reminderrelay", "state.db"), nil
}

// Open opens (or creates) the SQLite database at path, applies the schema, and
// configures WAL mode for better concurrent read performance.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}

	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("opening database %q: %w", path, err)
	}

	// Single writer to avoid SQLITE_BUSY under WAL.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate applies the schema DDL idempotently (CREATE IF NOT EXISTS).
func migrate(db *sql.DB) error {
	_, err := db.Exec(schema)
	return err
}

// GetItemByRemindersUID returns the item with the given Reminders UID,
// or (nil, nil) if no such item exists.
func (s *Store) GetItemByRemindersUID(ctx context.Context, uid string) (*Item, error) {
	const q = `
		SELECT id, reminders_uid, ha_uid, list_name, title,
		       last_sync_hash, reminders_modified, ha_modified, last_synced_at
		FROM sync_items WHERE reminders_uid = ?`
	row := s.db.QueryRowContext(ctx, q, uid)
	return scanItem(row)
}

// GetItemByHAUID returns the item with the given HA UID,
// or (nil, nil) if no such item exists.
func (s *Store) GetItemByHAUID(ctx context.Context, uid string) (*Item, error) {
	const q = `
		SELECT id, reminders_uid, ha_uid, list_name, title,
		       last_sync_hash, reminders_modified, ha_modified, last_synced_at
		FROM sync_items WHERE ha_uid = ?`
	row := s.db.QueryRowContext(ctx, q, uid)
	return scanItem(row)
}

// GetAllItemsForList returns all tracked items for the given Reminders list name.
func (s *Store) GetAllItemsForList(ctx context.Context, listName string) ([]*Item, error) {
	const q = `
		SELECT id, reminders_uid, ha_uid, list_name, title,
		       last_sync_hash, reminders_modified, ha_modified, last_synced_at
		FROM sync_items WHERE list_name = ?`
	rows, err := s.db.QueryContext(ctx, q, listName)
	if err != nil {
		return nil, fmt.Errorf("querying items for list %q: %w", listName, err)
	}
	defer func() { _ = rows.Close() }()

	var items []*Item
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// UpsertItem inserts or replaces an item in the database using the RemindersUID
// as the primary lookup key. If RemindersUID is empty, HAUID is used instead.
// The item's ID field is updated with the row ID after insert.
func (s *Store) UpsertItem(ctx context.Context, item *Item) error {
	const q = `
		INSERT INTO sync_items
		    (reminders_uid, ha_uid, list_name, title, last_sync_hash,
		     reminders_modified, ha_modified, last_synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(reminders_uid) WHERE reminders_uid != '' DO UPDATE SET
		    ha_uid             = excluded.ha_uid,
		    list_name          = excluded.list_name,
		    title              = excluded.title,
		    last_sync_hash     = excluded.last_sync_hash,
		    reminders_modified = excluded.reminders_modified,
		    ha_modified        = excluded.ha_modified,
		    last_synced_at     = excluded.last_synced_at`

	res, err := s.db.ExecContext(ctx, q,
		item.RemindersUID,
		item.HAUID,
		item.ListName,
		item.Title,
		item.LastSyncHash,
		formatTime(item.RemindersModified),
		formatTime(item.HAModified),
		formatTime(item.LastSyncedAt),
	)
	if err != nil {
		return fmt.Errorf("upserting item %q: %w", item.Title, err)
	}
	id, err := res.LastInsertId()
	if err == nil && id > 0 {
		item.ID = id
	}
	return nil
}

// DeleteItem removes the item with the given database ID.
func (s *Store) DeleteItem(ctx context.Context, id int64) error {
	const q = `DELETE FROM sync_items WHERE id = ?`
	if _, err := s.db.ExecContext(ctx, q, id); err != nil {
		return fmt.Errorf("deleting item id=%d: %w", id, err)
	}
	return nil
}

// IsEmpty reports whether the sync_items table has no rows.
// Used by the first-run bootstrap to detect a fresh install.
func (s *Store) IsEmpty(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_items`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking if store is empty: %w", err)
	}
	return count == 0, nil
}

// --- helpers -----------------------------------------------------------------

// scanner matches both *sql.Row and *sql.Rows so scanItem can be reused.
type scanner interface {
	Scan(dest ...any) error
}

func scanItem(s scanner) (*Item, error) {
	var item Item
	var remMod, haMod, syncedAt string

	err := s.Scan(
		&item.ID,
		&item.RemindersUID,
		&item.HAUID,
		&item.ListName,
		&item.Title,
		&item.LastSyncHash,
		&remMod,
		&haMod,
		&syncedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil //nolint:nilnil // intentional: "not found" sentinel
	}
	if err != nil {
		return nil, fmt.Errorf("scanning item row: %w", err)
	}

	item.RemindersModified, _ = parseTime(remMod)
	item.HAModified, _ = parseTime(haMod)
	item.LastSyncedAt, _ = parseTime(syncedAt)

	return &item, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}
