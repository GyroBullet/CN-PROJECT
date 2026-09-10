package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const (
	// NIP-98 event kind for HTTP Authentication
	KindHTTPAuth = 27235

	// Maximum age of an auth token (5 minutes)
	MaxAuthAge = 5 * time.Minute
)

// ValidateNIP98 extracts and validates a NIP-98 authorization header.
// Returns the authenticated public key on success.
func ValidateNIP98(r *http.Request, baseURL string) (string, error) {
	// 1. Extract the Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", errors.New("missing Authorization header")
	}

	// 2. Parse the "Nostr <base64>" format
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Nostr") {
		return "", errors.New("invalid Authorization header format: expected 'Nostr <base64>'")
	}

	// 3. Decode the base64 payload
	eventJSON, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 auth payload: %w", err)
	}

	// 4. Unmarshal the Nostr event
	var event nostr.Event
	if err := json.Unmarshal(eventJSON, &event); err != nil {
		return "", fmt.Errorf("failed to parse auth event JSON: %w", err)
	}

	// 5. Verify the cryptographic signature
	ok, err := event.CheckSignature()
	if err != nil {
		return "", fmt.Errorf("signature verification error: %w", err)
	}
	if !ok {
		return "", errors.New("invalid event signature")
	}

	// 6. Verify the event kind is NIP-98 (Kind 27235)
	if event.Kind != KindHTTPAuth {
		return "", fmt.Errorf("invalid event kind: expected %d, got %d", KindHTTPAuth, event.Kind)
	}

	// 7. Verify the timestamp is within the allowed window (anti-replay)
	now := time.Now().Unix()
	createdAt := int64(event.CreatedAt)
	maxAge := int64(MaxAuthAge.Seconds())
	if createdAt < now-maxAge || createdAt > now+maxAge {
		return "", fmt.Errorf("auth token timestamp out of range: created_at=%d, now=%d", createdAt, now)
	}

	// 8. Verify the URL tag matches the request
	targetURL := buildRequestURL(r, baseURL)
	var urlValid, methodValid bool

	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "u":
			if tag[1] == targetURL {
				urlValid = true
			}
		case "method":
			if strings.EqualFold(tag[1], r.Method) {
				methodValid = true
			}
		}
	}

	if !urlValid {
		return "", fmt.Errorf("URL tag mismatch: expected %q", targetURL)
	}
	if !methodValid {
		return "", fmt.Errorf("method tag mismatch: expected %q", r.Method)
	}

	// All checks passed — return the authenticated pubkey
	return event.PubKey, nil
}

// buildRequestURL constructs the full URL for NIP-98 tag validation
func buildRequestURL(r *http.Request, baseURL string) string {
	// Strip trailing slash from base URL
	base := strings.TrimRight(baseURL, "/")
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	return base + path
}
