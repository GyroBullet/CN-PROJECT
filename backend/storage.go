package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// BlobStore handles content-addressed file storage
type BlobStore struct {
	blobDir string
}

// NewBlobStore initializes the blob storage directory
func NewBlobStore(dataDir string) (*BlobStore, error) {
	blobDir := filepath.Join(dataDir, "blobs")
	if err := os.MkdirAll(blobDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create blob directory: %w", err)
	}
	log.Printf("✓ Blob storage initialized at %s\n", blobDir)
	return &BlobStore{blobDir: blobDir}, nil
}

// SaveBlob streams data from a reader, computes the SHA-256 hash, and stores the
// file using the hash as its filename. Returns the hash, size, and whether the
// blob already existed (deduplication).
func (bs *BlobStore) SaveBlob(reader io.Reader) (hash string, size int64, existed bool, err error) {
	// Create a temporary file in the same directory (for atomic rename)
	tmpFile, err := os.CreateTemp(bs.blobDir, "upload-*.tmp")
	if err != nil {
		return "", 0, false, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup on any error path
	defer func() {
		if err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
		}
	}()

	// Stream data through SHA-256 hasher while writing to temp file
	hasher := sha256.New()
	tee := io.TeeReader(reader, hasher)

	size, err = io.Copy(tmpFile, tee)
	if err != nil {
		return "", 0, false, fmt.Errorf("failed to write blob data: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return "", 0, false, fmt.Errorf("failed to close temp file: %w", err)
	}

	// Compute the final hash
	hash = hex.EncodeToString(hasher.Sum(nil))
	finalPath := bs.blobPath(hash)

	// Check for deduplication
	if bs.BlobExists(hash) {
		// Blob already exists on disk — remove the temp file
		os.Remove(tmpPath)
		log.Printf("  → Blob %s already exists (dedup), skipping disk write\n", hash[:16])
		return hash, size, true, nil
	}

	// Atomic rename temp file to its hash-based name
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", 0, false, fmt.Errorf("failed to rename blob file: %w", err)
	}

	log.Printf("  → Blob %s saved (%d bytes)\n", hash[:16], size)
	return hash, size, false, nil
}

// GetBlob opens a blob file for reading
func (bs *BlobStore) GetBlob(hash string) (*os.File, error) {
	path := bs.blobPath(hash)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("blob not found: %s", hash)
		}
		return nil, fmt.Errorf("failed to open blob: %w", err)
	}
	return f, nil
}

// BlobExists checks if a blob file exists on disk
func (bs *BlobStore) BlobExists(hash string) bool {
	_, err := os.Stat(bs.blobPath(hash))
	return err == nil
}

// DeleteBlob removes a blob file from disk
func (bs *BlobStore) DeleteBlob(hash string) error {
	path := bs.blobPath(hash)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // Already gone
		}
		return fmt.Errorf("failed to delete blob: %w", err)
	}
	log.Printf("  → Blob %s deleted from disk\n", hash[:16])
	return nil
}

// blobPath returns the full filesystem path for a given hash
func (bs *BlobStore) blobPath(hash string) string {
	return filepath.Join(bs.blobDir, hash)
}
