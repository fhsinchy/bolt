# internal/db

SQLite data access layer using `modernc.org/sqlite` (pure Go, driver name `"sqlite"`).

## SQLite Configuration

- **WAL mode** — concurrent reads during writes
- **synchronous=NORMAL** — good safety/speed balance
- **busy_timeout=5000** — wait 5s on lock contention
- **foreign_keys=ON** — enforced FK constraints
- **MaxOpenConns=1** — serializes writes to avoid SQLITE_BUSY

## Schema (v1)

**downloads** table: id (PK), url, filename, dir, total_size, downloaded, status, segments, speed_limit, headers (JSON), referer_url, checksum ("algo:value"), etag, last_modified, error, created_at, completed_at, scheduled_at, queue_order

**segments** table: download_id + idx (composite PK), start_byte, end_byte, downloaded, done. FK to downloads with ON DELETE CASCADE.

**Indexes:** status, created_at DESC, queue_order ASC

## Migration System

Version-based using `PRAGMA user_version`. Each migration runs in its own transaction. Adding a new migration = appending to the `migrations` slice in `db.go`.

## Key Patterns

- Headers stored as JSON text, marshaled/unmarshaled on read/write
- Checksum stored as `"algorithm:value"` string
- Dates as `TEXT` via `datetime('now')`, parsed with `time.Parse("2006-01-02 15:04:05", ...)`
- `BatchUpdateSegments` uses prepared statement in a transaction for efficiency (called every 2s during downloads)
- `sql.ErrNoRows` → `model.ErrNotFound`; update/delete check `RowsAffected() == 0`
- `NextQueueOrder` returns `MAX(queue_order)+1` for new downloads
- `ReorderDownloads` sets `queue_order` by array index in a transaction
- `ListDownloads` sorts by `queue_order ASC, created_at DESC`
