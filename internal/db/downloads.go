package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fhsinchy/bolt/internal/model"
)

const sqliteTimeFormat = "2006-01-02 15:04:05"

// InsertDownload inserts a new download record into the database.
// Headers are marshaled to a JSON text column.
func (s *Store) InsertDownload(ctx context.Context, d *model.Download) error {
	headersJSON, err := marshalHeaders(d.Headers)
	if err != nil {
		return fmt.Errorf("marshal headers: %w", err)
	}

	checksumStr := formatChecksum(d.Checksum)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO downloads (
			id, url, filename, dir, total_size, downloaded, status,
			segments, speed_limit, headers, referer_url, checksum,
			etag, last_modified, error, queue_order, trace_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.URL, d.Filename, d.Dir, d.TotalSize, d.Downloaded,
		string(d.Status), d.SegmentCount, d.SpeedLimit, headersJSON,
		d.RefererURL, checksumStr, d.ETag, d.LastModified, d.Error,
		d.QueueOrder, d.TraceID,
	)
	if err != nil {
		return fmt.Errorf("insert download: %w", err)
	}
	return nil
}

// InsertDownloadWithSegments inserts a download and its segments atomically
// within a single transaction. If either insert fails, both are rolled back.
func (s *Store) InsertDownloadWithSegments(ctx context.Context, d *model.Download, segments []model.Segment) error {
	headersJSON, err := marshalHeaders(d.Headers)
	if err != nil {
		return fmt.Errorf("marshal headers: %w", err)
	}
	checksumStr := formatChecksum(d.Checksum)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO downloads (
			id, url, filename, dir, total_size, downloaded, status,
			segments, speed_limit, headers, referer_url, checksum,
			etag, last_modified, error, queue_order, trace_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.URL, d.Filename, d.Dir, d.TotalSize, d.Downloaded,
		string(d.Status), d.SegmentCount, d.SpeedLimit, headersJSON,
		d.RefererURL, checksumStr, d.ETag, d.LastModified, d.Error,
		d.QueueOrder, d.TraceID,
	)
	if err != nil {
		return fmt.Errorf("insert download: %w", err)
	}

	if len(segments) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO segments (download_id, idx, start_byte, end_byte, downloaded, done)
			VALUES (?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare insert segment: %w", err)
		}
		defer stmt.Close()

		for _, seg := range segments {
			done := 0
			if seg.Done {
				done = 1
			}
			if _, err := stmt.ExecContext(ctx,
				seg.DownloadID, seg.Index, seg.StartByte, seg.EndByte,
				seg.Downloaded, done,
			); err != nil {
				return fmt.Errorf("insert segment %d: %w", seg.Index, err)
			}
		}
	}

	return tx.Commit()
}

// GetDownload retrieves a single download by ID.
// Returns model.ErrNotFound if the download does not exist.
func (s *Store) GetDownload(ctx context.Context, id string) (*model.Download, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, url, filename, dir, total_size, downloaded, status,
		       segments, speed_limit, headers, referer_url, checksum,
		       etag, last_modified, error, created_at, completed_at,
		       queue_order, trace_id
		FROM downloads WHERE id = ?`, id)

	d, err := scanDownload(row)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ListDownloads lists downloads with optional status filter and pagination.
// If status is empty, all downloads are returned.
func (s *Store) ListDownloads(ctx context.Context, status string, limit, offset int) ([]model.Download, error) {
	var query strings.Builder
	var args []any

	query.WriteString(`
		SELECT id, url, filename, dir, total_size, downloaded, status,
		       segments, speed_limit, headers, referer_url, checksum,
		       etag, last_modified, error, created_at, completed_at,
		       queue_order, trace_id
		FROM downloads`)

	if status != "" {
		query.WriteString(" WHERE status = ?")
		args = append(args, status)
	}

	// Order non-terminal downloads before terminal (completed / error).
	// Within each group, sort by queue_order then creation time.
	query.WriteString(" ORDER BY CASE WHEN status IN ('completed', 'error') THEN 1 ELSE 0 END, queue_order ASC, created_at DESC")

	if limit > 0 {
		query.WriteString(" LIMIT ?")
		args = append(args, limit)
	}
	if offset > 0 {
		query.WriteString(" OFFSET ?")
		args = append(args, offset)
	}

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list downloads: %w", err)
	}
	defer rows.Close()

	var downloads []model.Download
	for rows.Next() {
		d, err := scanDownloadRows(rows)
		if err != nil {
			return nil, err
		}
		downloads = append(downloads, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate downloads: %w", err)
	}
	return downloads, nil
}

// UpdateDownloadStatus updates the status and error message of a download.
// When transitioning to 'error', also sets queue_order = -1 to release
// the queue position.
func (s *Store) UpdateDownloadStatus(ctx context.Context, id string, status model.Status, errMsg string) error {
	var result sql.Result
	var err error
	if status == model.StatusError {
		result, err = s.db.ExecContext(ctx,
			`UPDATE downloads SET status = ?, error = ?, queue_order = -1 WHERE id = ?`,
			string(status), errMsg, id)
	} else {
		result, err = s.db.ExecContext(ctx,
			`UPDATE downloads SET status = ?, error = ? WHERE id = ?`,
			string(status), errMsg, id)
	}
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return checkRowsAffected(result, id)
}

// UpdateDownloadURL updates the URL and headers for a download.
func (s *Store) UpdateDownloadURL(ctx context.Context, id string, newURL string, newHeaders map[string]string) error {
	headersJSON, err := marshalHeaders(newHeaders)
	if err != nil {
		return fmt.Errorf("marshal headers: %w", err)
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE downloads SET url = ?, headers = ? WHERE id = ?`,
		newURL, headersJSON, id)
	if err != nil {
		return fmt.Errorf("update url: %w", err)
	}
	return checkRowsAffected(result, id)
}

// UpdateDownloadProgress updates the downloaded byte count for a download.
func (s *Store) UpdateDownloadProgress(ctx context.Context, id string, downloaded int64) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE downloads SET downloaded = ? WHERE id = ?`,
		downloaded, id)
	if err != nil {
		return fmt.Errorf("update progress: %w", err)
	}
	return checkRowsAffected(result, id)
}

// SetCompleted marks a download as completed with the current timestamp.
// Also sets queue_order = -1 to release the queue position.
func (s *Store) SetCompleted(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE downloads SET status = ?, completed_at = datetime('now'), queue_order = -1 WHERE id = ?`,
		string(model.StatusCompleted), id)
	if err != nil {
		return fmt.Errorf("set completed: %w", err)
	}
	return checkRowsAffected(result, id)
}

// UpdateDownloadChecksum updates the checksum for a download.
func (s *Store) UpdateDownloadChecksum(ctx context.Context, id string, checksum *model.Checksum) error {
	checksumStr := formatChecksum(checksum)
	result, err := s.db.ExecContext(ctx,
		`UPDATE downloads SET checksum = ? WHERE id = ?`,
		checksumStr, id)
	if err != nil {
		return fmt.Errorf("update checksum: %w", err)
	}
	return checkRowsAffected(result, id)
}

// FindByFilename finds a non-completed download with the given filename and directory.
// Returns nil, nil if no match.
func (s *Store) FindByFilename(ctx context.Context, filename, dir string) (*model.Download, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, url, filename, dir, total_size, downloaded, status,
		       segments, speed_limit, headers, referer_url, checksum,
		       etag, last_modified, error, created_at, completed_at,
		       queue_order, trace_id
		FROM downloads
		WHERE filename = ? AND dir = ? AND status NOT IN ('completed')
		ORDER BY created_at DESC
		LIMIT 1`, filename, dir)

	d, err := scanDownload(row)
	if err == model.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// DeleteDownload deletes a download by ID. Segments are cascade-deleted.
func (s *Store) DeleteDownload(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM downloads WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete download: %w", err)
	}
	return checkRowsAffected(result, id)
}

// GetNextQueued returns the queued download with the lowest queue_order.
// Returns nil, nil if there are no queued downloads.
func (s *Store) GetNextQueued(ctx context.Context) (*model.Download, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, url, filename, dir, total_size, downloaded, status,
		       segments, speed_limit, headers, referer_url, checksum,
		       etag, last_modified, error, created_at, completed_at,
		       queue_order, trace_id
		FROM downloads
		WHERE status = ?
		ORDER BY queue_order ASC, created_at ASC
		LIMIT 1`, string(model.StatusQueued))

	d, err := scanDownload(row)
	if err == model.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// CountByStatus counts downloads with the given status.
func (s *Store) CountByStatus(ctx context.Context, status model.Status) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM downloads WHERE status = ?`,
		string(status)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count by status: %w", err)
	}
	return count, nil
}

// CountAll counts all downloads regardless of status.
func (s *Store) CountAll(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM downloads`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count all: %w", err)
	}
	return count, nil
}

// NextQueueOrder returns the next available queue_order value.
// Only non-terminal downloads (queued, active, paused, verifying) are
// considered so that completed and error downloads don't inflate the
// queue position for new items.
func (s *Store) NextQueueOrder(ctx context.Context) (int, error) {
	var order int
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(queue_order), 0) + 1 FROM downloads
		 WHERE status IN (?, ?, ?, ?)`,
		string(model.StatusQueued),
		string(model.StatusActive),
		string(model.StatusPaused),
		string(model.StatusVerifying)).Scan(&order)
	if err != nil {
		return 0, fmt.Errorf("next queue order: %w", err)
	}
	return order, nil
}

// ReorderDownloads updates the queue_order for the given download IDs.
// The order of the IDs determines the new queue_order values (0-indexed).
//
// To avoid queue_order collisions with non-reordered non-terminal
// downloads (active, paused, verifying), the function first computes a
// base offset from the highest queue_order among downloads NOT in the
// ordered set but still in a non-terminal status.  Queued downloads that
// are not in orderedIDs are shifted above the reordered block so they
// appear after the manually ordered items.
func (s *Store) ReorderDownloads(ctx context.Context, orderedIDs []string) error {
	if len(orderedIDs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Build set of ordered IDs for quick lookup.
	orderedSet := make(map[string]bool, len(orderedIDs))
	for _, id := range orderedIDs {
		orderedSet[id] = true
	}

	// Find the highest queue_order among non-terminal downloads that
	// are NOT in the ordered set.  The reordered block starts after
	// these so it doesn't collide.
	placeholders := make([]string, 0, len(orderedIDs))
	queryArgs := []any{
		string(model.StatusActive),
		string(model.StatusPaused),
		string(model.StatusVerifying),
	}
	for _, id := range orderedIDs {
		placeholders = append(placeholders, "?")
		queryArgs = append(queryArgs, id)
	}

	var base int
	query := fmt.Sprintf(
		`SELECT COALESCE(MAX(queue_order), -1) FROM downloads
		 WHERE status IN (?, ?, ?) AND id NOT IN (%s)`,
		strings.Join(placeholders, ","),
	)
	if err := tx.QueryRowContext(ctx, query, queryArgs...).Scan(&base); err != nil {
		return fmt.Errorf("compute reorder base: %w", err)
	}
	base++ // start after the highest non-reordered non-terminal item

	// Also find queued downloads not in the ordered set so we can bump
	// them above the reordered block.
	queuedPlaceholders := make([]string, len(orderedIDs))
	queuedArgs := []any{string(model.StatusQueued)}
	for i, id := range orderedIDs {
		queuedPlaceholders[i] = "?"
		queuedArgs = append(queuedArgs, id)
	}
	queuedQuery := fmt.Sprintf(
		`SELECT id FROM downloads
		 WHERE status = ? AND id NOT IN (%s)
		 ORDER BY queue_order ASC`,
		strings.Join(queuedPlaceholders, ","),
	)
	rows, err := tx.QueryContext(ctx, queuedQuery, queuedArgs...)
	if err != nil {
		return fmt.Errorf("query non-reordered queued: %w", err)
	}
	var otherQueued []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan queued id: %w", err)
		}
		otherQueued = append(otherQueued, id)
	}
	rows.Close()

	stmt, err := tx.PrepareContext(ctx, `UPDATE downloads SET queue_order = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("prepare reorder: %w", err)
	}
	defer stmt.Close()

	// Assign the user-ordered items starting from base.
	for i, id := range orderedIDs {
		if _, err := stmt.ExecContext(ctx, base+i, id); err != nil {
			return fmt.Errorf("reorder download %s: %w", id, err)
		}
	}

	// Bump remaining queued downloads above the reordered block so they
	// appear after it in the queue.
	for i, id := range otherQueued {
		if _, err := stmt.ExecContext(ctx, base+len(orderedIDs)+i, id); err != nil {
			return fmt.Errorf("reorder remaining queued %s: %w", id, err)
		}
	}

	return tx.Commit()
}

// PromoteDownload moves a queued download to the front of the non-terminal
// queue so it will be the next queued item picked up.  The download keeps
// its queue_order just above any currently active downloads and all other
// queued items are shifted down by one.  Returns an error if the download
// is not in queued status.
func (s *Store) PromoteDownload(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Verify the download is queued.
	var status string
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM downloads WHERE id = ?`, id,
	).Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return model.ErrNotFound
		}
		return fmt.Errorf("lookup download: %w", err)
	}
	if model.Status(status) != model.StatusQueued {
		return fmt.Errorf("only queued downloads can be promoted, got %s", status)
	}

	// Find the highest queue_order among active downloads.  The promoted
	// item goes right after them (or at 0 if none are active).
	var base int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(queue_order), -1) FROM downloads
		 WHERE status = ?`,
		string(model.StatusActive),
	).Scan(&base); err != nil {
		return fmt.Errorf("find active base: %w", err)
	}
	base++

	// Get all other queued downloads (excluding the one being promoted),
	// ordered by their current queue_order.
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM downloads
		 WHERE status = ? AND id != ?
		 ORDER BY queue_order ASC`,
		string(model.StatusQueued), id,
	)
	if err != nil {
		return fmt.Errorf("query other queued: %w", err)
	}
	var otherQueued []string
	for rows.Next() {
		var otherID string
		if err := rows.Scan(&otherID); err != nil {
			rows.Close()
			return fmt.Errorf("scan queued id: %w", err)
		}
		otherQueued = append(otherQueued, otherID)
	}
	rows.Close()

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE downloads SET queue_order = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("prepare update: %w", err)
	}
	defer stmt.Close()

	// Promoted download gets the slot right after active downloads.
	if _, err := stmt.ExecContext(ctx, base, id); err != nil {
		return fmt.Errorf("promote download: %w", err)
	}

	// Shift remaining queued downloads down.
	for i, otherID := range otherQueued {
		if _, err := stmt.ExecContext(ctx, base+1+i, otherID); err != nil {
			return fmt.Errorf("shift queued %s: %w", otherID, err)
		}
	}

	return tx.Commit()
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanDownload scans a single row from *sql.Row.
func scanDownload(row *sql.Row) (*model.Download, error) {
	d, err := scanDownloadFrom(row)
	if err == sql.ErrNoRows {
		return nil, model.ErrNotFound
	}
	return d, err
}

// scanDownloadRows scans a single row from *sql.Rows.
func scanDownloadRows(rows *sql.Rows) (*model.Download, error) {
	return scanDownloadFrom(rows)
}

func scanDownloadFrom(s scanner) (*model.Download, error) {
	var d model.Download
	var (
		headersStr    sql.NullString
		checksumStr   sql.NullString
		statusStr     string
		createdAtStr  string
		completedStr  sql.NullString
	)

	err := s.Scan(
		&d.ID, &d.URL, &d.Filename, &d.Dir, &d.TotalSize, &d.Downloaded,
		&statusStr, &d.SegmentCount, &d.SpeedLimit, &headersStr,
		&d.RefererURL, &checksumStr, &d.ETag, &d.LastModified, &d.Error,
		&createdAtStr, &completedStr, &d.QueueOrder, &d.TraceID,
	)
	if err != nil {
		return nil, err
	}

	d.Status = model.Status(statusStr)

	// Parse headers JSON.
	d.Headers = make(map[string]string)
	if headersStr.Valid && headersStr.String != "" {
		if err := json.Unmarshal([]byte(headersStr.String), &d.Headers); err != nil {
			return nil, fmt.Errorf("unmarshal headers: %w", err)
		}
	}

	// Parse checksum "algorithm:value".
	if checksumStr.Valid && checksumStr.String != "" {
		d.Checksum = parseChecksum(checksumStr.String)
	}

	// Parse created_at.
	if t, err := time.Parse(sqliteTimeFormat, createdAtStr); err == nil {
		d.CreatedAt = t
	}

	// Parse completed_at.
	if completedStr.Valid && completedStr.String != "" {
		if t, err := time.Parse(sqliteTimeFormat, completedStr.String); err == nil {
			d.CompletedAt = &t
		}
	}

	return &d, nil
}

// marshalHeaders converts the headers map to a JSON string.
func marshalHeaders(headers map[string]string) (string, error) {
	if len(headers) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(headers)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// formatChecksum converts a Checksum to "algorithm:value" storage format.
func formatChecksum(c *model.Checksum) string {
	if c == nil || c.Algorithm == "" {
		return ""
	}
	return c.Algorithm + ":" + c.Value
}

// parseChecksum converts the stored "algorithm:value" string to a Checksum.
func parseChecksum(s string) *model.Checksum {
	idx := strings.Index(s, ":")
	if idx < 0 {
		return nil
	}
	return &model.Checksum{
		Algorithm: s[:idx],
		Value:     s[idx+1:],
	}
}

// checkRowsAffected returns model.ErrNotFound if no rows were affected.
func checkRowsAffected(result sql.Result, id string) error {
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return model.ErrNotFound
	}
	return nil
}
