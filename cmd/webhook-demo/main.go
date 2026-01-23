package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xpadev-net/youtube-stream-tracker/internal/webhook"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9090"
	}

	signingKey := os.Getenv("WEBHOOK_SIGNING_KEY")
	if signingKey == "" {
		signingKey = "demo-signing-key"
	}

	http.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Read body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// Get headers
		signature := r.Header.Get("X-Signature-256")
		timestampStr := r.Header.Get("X-Timestamp")

		// Parse timestamp
		timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			fmt.Printf("[%s] ERROR: Invalid timestamp: %s\n", time.Now().Format(time.RFC3339), timestampStr)
			http.Error(w, "Invalid timestamp", http.StatusBadRequest)
			return
		}

		// Verify signature
		signature = strings.TrimPrefix(signature, "sha256=")
		if !webhook.VerifySignature(signingKey, signature, timestamp, body) {
			fmt.Printf("[%s] ERROR: Invalid signature\n", time.Now().Format(time.RFC3339))
			http.Error(w, "Invalid signature", http.StatusUnauthorized)
			return
		}

		// Parse payload
		var payload webhook.Payload
		if err := json.Unmarshal(body, &payload); err != nil {
			fmt.Printf("[%s] ERROR: Failed to parse payload: %v\n", time.Now().Format(time.RFC3339), err)
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}

		// Log the webhook
		fmt.Printf("[%s] WEBHOOK RECEIVED\n", time.Now().Format(time.RFC3339))
		fmt.Printf("  Event Type: %s\n", payload.EventType)
		fmt.Printf("  Monitor ID: %s\n", payload.MonitorID)
		fmt.Printf("  Stream URL: %s\n", payload.StreamURL)
		fmt.Printf("  Timestamp:  %s\n", payload.Timestamp.Format(time.RFC3339))
		if payload.Data != nil {
			dataJSON, _ := json.MarshalIndent(payload.Data, "  ", "  ")
			fmt.Printf("  Data:\n  %s\n", string(dataJSON))
		}
		fmt.Println("  Signature verified: OK")
		fmt.Println()

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	})

	fmt.Printf("Webhook demo server starting on port %s\n", port)
	fmt.Printf("Signing key: %s\n", signingKey)
	fmt.Printf("Endpoint: POST /webhook\n\n")

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
