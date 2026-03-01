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
			etag, last_modified, error, queue_order
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.URL, d.Filename, d.Dir, d.TotalSize, d.Downloaded,
		string(d.Status), d.SegmentCount, d.SpeedLimit, headersJSON,
		d.RefererURL, checksumStr, d.ETag, d.LastModified, d.Error,
		d.QueueOrder,
	)
	if err != nil {
		return fmt.Errorf("insert download: %w", err)
	}
	return nil
}

// GetDownload retrieves a single download by ID.
// Returns model.ErrNotFound if the download does not exist.
func (s *Store) GetDownload(ctx context.Context, id string) (*model.Download, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, url, filename, dir, total_size, downloaded, status,
		       segments, speed_limit, headers, referer_url, checksum,
		       etag, last_modified, error, created_at, completed_at,
		       queue_order
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
		       queue_order
		FROM downloads`)

	if status != "" {
		query.WriteString(" WHERE status = ?")
		args = append(args, status)
	}

	query.WriteString(" ORDER BY queue_order ASC, created_at DESC")

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
func (s *Store) UpdateDownloadStatus(ctx context.Context, id string, status model.Status, errMsg string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE downloads SET status = ?, error = ? WHERE id = ?`,
		string(status), errMsg, id)
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
func (s *Store) SetCompleted(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE downloads SET status = ?, completed_at = datetime('now') WHERE id = ?`,
		string(model.StatusCompleted), id)
	if err != nil {
		return fmt.Errorf("set completed: %w", err)
	}
	return checkRowsAffected(result, id)
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
		       queue_order
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

// NextQueueOrder returns the next available queue_order value.
func (s *Store) NextQueueOrder(ctx context.Context) (int, error) {
	var order int
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(queue_order), 0) + 1 FROM downloads`).Scan(&order)
	if err != nil {
		return 0, fmt.Errorf("next queue order: %w", err)
	}
	return order, nil
}

// ReorderDownloads updates the queue_order for the given download IDs.
// The order of the IDs determines the new queue_order values (0-indexed).
func (s *Store) ReorderDownloads(ctx context.Context, orderedIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `UPDATE downloads SET queue_order = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("prepare reorder: %w", err)
	}
	defer stmt.Close()

	for i, id := range orderedIDs {
		if _, err := stmt.ExecContext(ctx, i, id); err != nil {
			return fmt.Errorf("reorder download %s: %w", id, err)
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
		&createdAtStr, &completedStr, &d.QueueOrder,
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
