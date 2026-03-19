// Example 01: Completion, Streaming, Multimodal, Logging, Health
//
// Run:
//
//	ANTHROPIC_API_KEY=sk-... go run .
//
// Or with a config file:
//
//	go run . -config ../../examples/deebus.yaml
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/ncobase/deebus"
)

func main() {
	config := flag.String("config", "", "path to deebus.yaml (optional)")
	flag.Parse()

	var client *deebus.Client
	var err error

	if *config != "" {
		client, err = deebus.LoadConfig(*config)
	} else {
		client, err = deebus.NewClient(deebus.Config{
			Providers: map[string]deebus.ProviderConfig{
				"anthropic": {
					Type:    "anthropic",
					APIKey:  requireEnv("ANTHROPIC_API_KEY"),
					BaseURL: "https://api.anthropic.com",
				},
			},
			Primary:   "anthropic/claude-opus-4-6",
			Timeout:   30,
			Retry:     2,
			RateLimit: 5,
		})
	}
	if err != nil {
		log.Fatal(err)
	}

	// Structured logging via slog — any Logger implementation works.
	client.SetLogger(slogLogger{})

	ctx := context.Background()

	singleTurn(ctx, client)
	streaming(ctx, client)
	healthCheck(ctx, client)
	printStats(client)
}

// singleTurn demonstrates a basic completion request.
func singleTurn(ctx context.Context, client *deebus.Client) {
	fmt.Println("── Single-turn completion ───────────────────────────────────────────")

	resp, err := client.Complete(ctx, &deebus.Request{
		Messages: []deebus.Message{
			deebus.TextMessage("system", "You are a concise technical assistant."),
			deebus.TextMessage("user", "What is exponential backoff? One paragraph."),
		},
		MaxTokens:   256,
		Temperature: 0.3,
	})
	if err != nil {
		log.Fatalf("complete: %v", err)
	}

	fmt.Println(resp.Content)
	fmt.Printf("\nprovider=%s  model=%s  tokens=%d  finish=%s\n\n",
		resp.Provider, resp.Model, resp.TokensUsed, resp.FinishReason)
}

// streaming demonstrates token-by-token SSE streaming.
func streaming(ctx context.Context, client *deebus.Client) {
	fmt.Println("── Streaming ────────────────────────────────────────────────────────")

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stream, err := client.Stream(ctx, &deebus.Request{
		Messages: []deebus.Message{
			deebus.TextMessage("user", "List the Go proverbs you know, one per line."),
		},
		MaxTokens: 512,
	})
	if err != nil {
		log.Fatalf("stream: %v", err)
	}

	var totalTokens int
	for chunk := range stream {
		if chunk.Error != nil {
			log.Fatalf("stream error: %v", chunk.Error)
		}
		fmt.Print(chunk.Content)
		if chunk.Done {
			totalTokens = chunk.TokensUsed
			break
		}
	}
	fmt.Printf("\n\ntokens=%d\n\n", totalTokens)
}

// healthCheck calls Health on every configured provider.
func healthCheck(ctx context.Context, client *deebus.Client) {
	fmt.Println("── Health check ─────────────────────────────────────────────────────")

	results := client.Health(ctx)
	for provider, err := range results {
		status := "OK"
		if err != nil {
			status = fmt.Sprintf("ERROR: %v", err)
		}
		fmt.Printf("  %-12s %s\n", provider, status)
	}
	fmt.Println()
}

// printStats shows aggregate request and token counters.
func printStats(client *deebus.Client) {
	total, tokens, success, failed := client.Stats.Get()
	fmt.Printf("── Stats ────────────────────────────────────────────────────────────\n")
	fmt.Printf("  requests=%d  tokens=%d  success=%d  failed=%d\n",
		total, tokens, success, failed)
}

// ── Logger bridge ─────────────────────────────────────────────────────────────

type slogLogger struct{}

func (slogLogger) Debug(msg string, f ...any) { slog.Debug(msg, f...) }
func (slogLogger) Info(msg string, f ...any)  { slog.Info(msg, f...) }
func (slogLogger) Warn(msg string, f ...any)  { slog.Warn(msg, f...) }
func (slogLogger) Error(msg string, f ...any) { slog.Error(msg, f...) }

// ── Helpers ───────────────────────────────────────────────────────────────────

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("environment variable %s is required", key)
	}
	return v
}
