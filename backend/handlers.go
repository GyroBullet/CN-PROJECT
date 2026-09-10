package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// ServerInfo represents the Blossom/NIP-96 server information response
type ServerInfo struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Pubkey        string   `json:"pubkey,omitempty"`
	Contact       string   `json:"contact,omitempty"`
	SupportedNIPs []int    `json:"supported_nips"`
	Software      string   `json:"software"`
	Version       string   `json:"version"`
	Protocols     []string `json:"protocols"`
}

// UploadResponse is returned after a successful upload
type UploadResponse struct {
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Type      string `json:"type"`
	Uploaded  int64  `json:"uploaded"`
	Existed   bool   `json:"existed"`
}

// ErrorResponse is returned on errors
type ErrorResponse struct {
	Error   string `json:"error"`
	Status  int    `json:"status"`
}

// Handlers holds references to the storage and database layers
type Handlers struct {
	store   *BlobStore
	db      *Database
	baseURL string
	maxSize int64
}

// NewHandlers creates a new handler group
func NewHandlers(store *BlobStore, db *Database, baseURL string, maxSize int64) *Handlers {
	return &Handlers{
		store:   store,
		db:      db,
		baseURL: strings.TrimRight(baseURL, "/"),
		maxSize: maxSize,
	}
}

// HandleServerInfo returns server metadata (GET /)
func (h *Handlers) HandleServerInfo(w http.ResponseWriter, r *http.Request) {
	// Only match exact root path
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	info := ServerInfo{
		Name:          "Sovereign Media Hub",
		Description:   "Self-hosted Blossom-compatible media server with Nostr authentication",
		SupportedNIPs: []int{94, 96, 98},
		Software:      "sovereign-media-hub",
		Version:       "1.0.0",
		Protocols:     []string{"blossom", "nip96"},
	}

	writeJSON(w, http.StatusOK, info)
}

// HandleUpload handles blob uploads (PUT /upload)
func (h *Handlers) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Authenticate via NIP-98
	pubkey, err := ValidateNIP98(r, h.baseURL)
	if err != nil {
		log.Printf("  ✗ Auth failed: %v\n", err)
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("authentication failed: %v", err))
		return
	}
	log.Printf("  ✓ Authenticated upload from %s\n", pubkey[:16])

	// Enforce max file size
	r.Body = http.MaxBytesReader(w, r.Body, h.maxSize)

	// Detect content type from header or filename
	contentType := r.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		// Try to detect from the X-Filename header
		if filename := r.Header.Get("X-Filename"); filename != "" {
			contentType = mime.TypeByExtension(filepath.Ext(filename))
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}

	// Get optional original filename
	originalName := r.Header.Get("X-Filename")
	if originalName == "" {
		originalName = "unnamed"
	}

	// Stream body to content-addressed storage
	hash, size, existed, err := h.store.SaveBlob(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file exceeds maximum size of %d bytes", h.maxSize))
			return
		}
		log.Printf("  ✗ Storage error: %v\n", err)
		writeError(w, http.StatusInternalServerError, "failed to store blob")
		return
	}

	// Record ownership in the database
	if err := h.db.RecordUpload(pubkey, hash, contentType, size, originalName); err != nil {
		log.Printf("  ✗ Database error: %v\n", err)
		writeError(w, http.StatusInternalServerError, "failed to record upload")
		return
	}

	resp := UploadResponse{
		URL:      fmt.Sprintf("%s/%s", h.baseURL, hash),
		SHA256:   hash,
		Size:     size,
		Type:     contentType,
		Uploaded: time.Now().Unix(),
		Existed:  existed,
	}

	log.Printf("  ✓ Upload complete: %s (%d bytes, dedup=%v)\n", hash[:16], size, existed)
	writeJSON(w, http.StatusOK, resp)
}

// HandleServeBlob serves a blob by its SHA-256 hash (GET /{sha256})
func (h *Handlers) HandleServeBlob(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/")
	hash = strings.TrimSpace(hash)

	if len(hash) != 64 {
		http.NotFound(w, r)
		return
	}

	// For HEAD requests, just check existence
	if r.Method == http.MethodHead {
		if h.store.BlobExists(hash) {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}

	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	file, err := h.store.GetBlob(hash)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	// Try to detect content type from the file
	buffer := make([]byte, 512)
	n, _ := file.Read(buffer)
	detectedType := http.DetectContentType(buffer[:n])

	// Seek back to beginning
	file.Seek(0, io.SeekStart)

	// Set headers for efficient caching (content-addressed = immutable)
	w.Header().Set("Content-Type", detectedType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-SHA256", hash)
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Stream the file
	io.Copy(w, file)
}

// HandleDeleteBlob deletes a blob (DELETE /{sha256})
func (h *Handlers) HandleDeleteBlob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	hash := strings.TrimPrefix(r.URL.Path, "/")
	hash = strings.TrimSpace(hash)

	if len(hash) != 64 {
		writeError(w, http.StatusBadRequest, "invalid hash")
		return
	}

	// Authenticate via NIP-98
	pubkey, err := ValidateNIP98(r, h.baseURL)
	if err != nil {
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("authentication failed: %v", err))
		return
	}
	log.Printf("  ✓ Authenticated delete from %s for %s\n", pubkey[:16], hash[:16])

	// Remove the ownership record
	if err := h.db.DeleteRecord(pubkey, hash); err != nil {
		writeError(w, http.StatusNotFound, "blob not found or not owned by you")
		return
	}

	// Check if any other users still reference this blob
	count, err := h.db.CountOwners(hash)
	if err != nil {
		log.Printf("  ✗ Count error: %v\n", err)
	}

	// If no one else owns it, delete from disk
	if count == 0 {
		if err := h.store.DeleteBlob(hash); err != nil {
			log.Printf("  ✗ Disk delete error: %v\n", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "blob deleted",
		"sha256":  hash,
	})
}

// HandleListBlobs lists all blobs for the authenticated user (GET /list/{pubkey})
func (h *Handlers) HandleListBlobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Authenticate via NIP-98
	pubkey, err := ValidateNIP98(r, h.baseURL)
	if err != nil {
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("authentication failed: %v", err))
		return
	}

	// Extract requested pubkey from URL
	requestedPubkey := strings.TrimPrefix(r.URL.Path, "/list/")
	requestedPubkey = strings.TrimSpace(requestedPubkey)

	// Users can only list their own blobs
	if requestedPubkey != pubkey {
		writeError(w, http.StatusForbidden, "you can only list your own blobs")
		return
	}

	blobs, err := h.db.ListBlobs(pubkey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list blobs")
		return
	}

	// Add URLs to each blob
	if blobs == nil {
		blobs = []BlobMeta{}
	}
	for i := range blobs {
		blobs[i].URL = fmt.Sprintf("%s/%s", h.baseURL, blobs[i].SHA256)
	}

	writeJSON(w, http.StatusOK, blobs)
}

// writeJSON writes a JSON response with the given status code
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError writes a JSON error response
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ErrorResponse{
		Error:  message,
		Status: status,
	})
}
