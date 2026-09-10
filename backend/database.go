package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

// BlobMeta represents metadata about an uploaded blob
type BlobMeta struct {
	PubKey       string `json:"pubkey"`
	SHA256       string `json:"sha256"`
	MimeType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
	OriginalName string `json:"original_name"`
	UploadedAt   int64  `json:"uploaded_at"`
	URL          string `json:"url,omitempty"`
}

// Database wraps the SQLite connection
type Database struct {
	db *sql.DB
}

// NewDatabase opens (or creates) the SQLite database and initializes the schema
func NewDatabase(path string) (*Database, error) {
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// SQLite performs best with a single writer
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	d := &Database{db: db}
	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("✓ Database initialized")
	return d, nil
}

// migrate creates the schema if it doesn't exist
func (d *Database) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS user_blobs (
		pubkey       TEXT NOT NULL,
		sha256       TEXT NOT NULL,
		mime_type    TEXT NOT NULL DEFAULT '',
		size_bytes   INTEGER NOT NULL DEFAULT 0,
		original_name TEXT NOT NULL DEFAULT '',
		uploaded_at  INTEGER NOT NULL,
		PRIMARY KEY (pubkey, sha256)
	);
	CREATE INDEX IF NOT EXISTS idx_user_blobs_pubkey ON user_blobs(pubkey);
	CREATE INDEX IF NOT EXISTS idx_user_blobs_sha256 ON user_blobs(sha256);
	`
	_, err := d.db.Exec(schema)
	return err
}

// RecordUpload inserts or replaces a blob ownership record
func (d *Database) RecordUpload(pubkey, sha256, mimeType string, sizeBytes int64, originalName string) error {
	_, err := d.db.Exec(
		`INSERT OR REPLACE INTO user_blobs (pubkey, sha256, mime_type, size_bytes, original_name, uploaded_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		pubkey, sha256, mimeType, sizeBytes, originalName, time.Now().Unix(),
	)
	return err
}

// ListBlobs returns all blob metadata for a given pubkey
func (d *Database) ListBlobs(pubkey string) ([]BlobMeta, error) {
	rows, err := d.db.Query(
		`SELECT pubkey, sha256, mime_type, size_bytes, original_name, uploaded_at
		 FROM user_blobs WHERE pubkey = ? ORDER BY uploaded_at DESC`,
		pubkey,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var blobs []BlobMeta
	for rows.Next() {
		var b BlobMeta
		if err := rows.Scan(&b.PubKey, &b.SHA256, &b.MimeType, &b.SizeBytes, &b.OriginalName, &b.UploadedAt); err != nil {
			return nil, err
		}
		blobs = append(blobs, b)
	}
	return blobs, rows.Err()
}

// DeleteRecord removes a blob ownership record for a specific pubkey
func (d *Database) DeleteRecord(pubkey, sha256 string) error {
	result, err := d.db.Exec(
		`DELETE FROM user_blobs WHERE pubkey = ? AND sha256 = ?`,
		pubkey, sha256,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("no record found for pubkey=%s sha256=%s", pubkey, sha256)
	}
	return nil
}

// CountOwners returns the number of pubkeys that reference a given blob hash
func (d *Database) CountOwners(sha256 string) (int, error) {
	var count int
	err := d.db.QueryRow(
		`SELECT COUNT(*) FROM user_blobs WHERE sha256 = ?`,
		sha256,
	).Scan(&count)
	return count, err
}

// GetBlob returns metadata for a specific pubkey+hash combination
func (d *Database) GetBlob(pubkey, sha256 string) (*BlobMeta, error) {
	var b BlobMeta
	err := d.db.QueryRow(
		`SELECT pubkey, sha256, mime_type, size_bytes, original_name, uploaded_at
		 FROM user_blobs WHERE pubkey = ? AND sha256 = ?`,
		pubkey, sha256,
	).Scan(&b.PubKey, &b.SHA256, &b.MimeType, &b.SizeBytes, &b.OriginalName, &b.UploadedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// Close shuts down the database connection
func (d *Database) Close() error {
	return d.db.Close()
}
