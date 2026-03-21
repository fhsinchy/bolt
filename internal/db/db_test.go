package db

import (
	"path/filepath"
	"testing"
)

func TestOpen(t *testing.T) {
	store := openTestStore(t)

	// Verify the database connection is alive.
	if err := store.db.Ping(); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
}

func TestMigration_TablesExist(t *testing.T) {
	store := openTestStore(t)

	tables := []string{"downloads", "segments"}
	for _, table := range tables {
		var name string
		err := store.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestMigration_IndexesExist(t *testing.T) {
	store := openTestStore(t)

	indexes := []string{
		"idx_downloads_status",
		"idx_downloads_created",
		"idx_downloads_queue",
	}
	for _, idx := range indexes {
		var name string
		err := store.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&name)
		if err != nil {
			t.Errorf("index %q not found: %v", idx, err)
		}
	}
}

func TestMigration_PragmasSet(t *testing.T) {
	store := openTestStore(t)

	tests := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"synchronous", "1"}, // NORMAL = 1
		{"foreign_keys", "1"},
		{"temp_store", "2"}, // MEMORY = 2
	}

	for _, tt := range tests {
		var got string
		err := store.db.QueryRow("PRAGMA " + tt.pragma).Scan(&got)
		if err != nil {
			t.Errorf("PRAGMA %s: %v", tt.pragma, err)
			continue
		}
		if got != tt.want {
			t.Errorf("PRAGMA %s = %q, want %q", tt.pragma, got, tt.want)
		}
	}
}

func TestMigration_UserVersion(t *testing.T) {
	store := openTestStore(t)

	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 2 {
		t.Errorf("user_version = %d, want 2", version)
	}
}

func TestMigration_Idempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Open and close twice; second open should succeed without re-migrating.
	store1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	store1.Close()

	store2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer store2.Close()

	var version int
	if err := store2.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 2 {
		t.Errorf("user_version = %d after re-open, want 2", version)
	}
}

func TestMigration_ForeignKeysEnforced(t *testing.T) {
	store := openTestStore(t)

	// Inserting a segment for a non-existent download should fail.
	_, err := store.db.Exec(`
		INSERT INTO segments (download_id, idx, start_byte, end_byte)
		VALUES ('nonexistent', 0, 0, 1000)`)
	if err == nil {
		t.Error("expected foreign key violation, got nil")
	}
}

// openTestStore creates a temporary Store for testing.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}
