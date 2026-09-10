package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/rs/cors"
)

func main() {
	// Configuration from environment
	port := getEnv("PORT", "8080")
	dataDir := getEnv("DATA_DIR", "./data")
	baseURL := getEnv("BASE_URL", fmt.Sprintf("http://localhost:%s", port))
	maxSizeStr := getEnv("MAX_FILE_SIZE", "104857600") // 100MB default

	maxSize, err := strconv.ParseInt(maxSizeStr, 10, 64)
	if err != nil {
		log.Fatalf("Invalid MAX_FILE_SIZE: %v", err)
	}

	log.Println("╔══════════════════════════════════════════╗")
	log.Println("║   Sovereign Personal Media Hub v1.0.0    ║")
	log.Println("║   Blossom + Nostr NIP-98 Auth Server     ║")
	log.Println("╚══════════════════════════════════════════╝")
	log.Printf("  Port:     %s\n", port)
	log.Printf("  Data dir: %s\n", dataDir)
	log.Printf("  Base URL: %s\n", baseURL)
	log.Printf("  Max size: %d bytes\n", maxSize)

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Initialize database
	dbPath := filepath.Join(dataDir, "sovereign.db")
	db, err := NewDatabase(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize blob storage
	store, err := NewBlobStore(dataDir)
	if err != nil {
		log.Fatalf("Failed to initialize blob storage: %v", err)
	}

	// Create handlers
	handlers := NewHandlers(store, db, baseURL, maxSize)

	// Set up routing
	mux := http.NewServeMux()

	// Server info
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Root path → server info
		if path == "/" {
			handlers.HandleServerInfo(w, r)
			return
		}

		// /upload → upload handler
		if path == "/upload" {
			handlers.HandleUpload(w, r)
			return
		}

		// /list/{pubkey} → list handler
		if len(path) > 6 && path[:6] == "/list/" {
			handlers.HandleListBlobs(w, r)
			return
		}

		// /{sha256} → serve or delete blob
		hash := path[1:] // strip leading /
		if len(hash) == 64 {
			if r.Method == http.MethodDelete {
				handlers.HandleDeleteBlob(w, r)
			} else {
				handlers.HandleServeBlob(w, r)
			}
			return
		}

		http.NotFound(w, r)
	})

	// CORS middleware (allow browser clients from any origin)
	corsHandler := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "HEAD", "PUT", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Filename"},
		ExposedHeaders:   []string{"X-Content-SHA256"},
		AllowCredentials: false,
		MaxAge:           86400,
	}).Handler(mux)

	// Start server
	addr := fmt.Sprintf(":%s", port)
	log.Printf("✓ Server listening on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, corsHandler))
}

// getEnv returns the value of an environment variable or a default
func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
