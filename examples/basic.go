package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"

	"github.com/ncobase/deebus"
)

// slogAdapter bridges Go's standard slog to the deebus Logger interface.
type slogAdapter struct{}

func (slogAdapter) Debug(msg string, fields ...any) { slog.Debug(msg, fields...) }
func (slogAdapter) Info(msg string, fields ...any)  { slog.Info(msg, fields...) }
func (slogAdapter) Warn(msg string, fields ...any)  { slog.Warn(msg, fields...) }
func (slogAdapter) Error(msg string, fields ...any) { slog.Error(msg, fields...) }

func main() {
	// LoadConfig reads deebus.yaml and expands ${ENV_VAR} references.
	// Example deebus.yaml:
	//
	//   providers:
	//     anthropic:
	//       type: anthropic
	//       apiKey: ${ANTHROPIC_API_KEY}
	//       baseURL: https://api.anthropic.com
	//     openai:
	//       type: openai
	//       apiKey: ${OPENAI_API_KEY}
	//       baseURL: https://api.openai.com
	//   primary: anthropic/claude-opus-4-6
	//   fallbacks:
	//     - openai/gpt-4o
	//   timeout: 30
	//   retry: 2
	//   rateLimit: 10
	//   circuitBreaker:
	//     maxFailures: 5
	//     resetTimeout: 60
	client, err := deebus.LoadConfig("deebus.yaml")
	if err != nil {
		log.Fatal(err)
	}

	// Attach a structured logger (optional — defaults to NoopLogger).
	client.SetLogger(slogAdapter{})

	ctx := context.Background()

	// ── Completion ────────────────────────────────────────────────────────────
	resp, err := client.Complete(ctx, &deebus.Request{
		Messages: []deebus.Message{
			deebus.SimpleMessage("user", "Explain the circuit breaker pattern in one sentence."),
		},
		MaxTokens:   256,
		Temperature: 0.7,
	})
	if err != nil {
		log.Fatalf("complete: %v", err)
	}
	fmt.Printf("Response : %s\n", resp.Content)
	fmt.Printf("Model    : %s (%s)\n", resp.Model, resp.Provider)
	fmt.Printf("Tokens   : %d\n", resp.TokensUsed)

	// ── Streaming ─────────────────────────────────────────────────────────────
	fmt.Print("\nStreaming: ")
	stream, err := client.Stream(ctx, &deebus.Request{
		Messages: []deebus.Message{
			deebus.SimpleMessage("user", "Count to five, one word per line."),
		},
	})
	if err != nil {
		log.Fatalf("stream: %v", err)
	}
	for chunk := range stream {
		if chunk.Error != nil {
			log.Printf("stream error: %v", chunk.Error)
			break
		}
		fmt.Print(chunk.Content)
		if chunk.Done {
			break
		}
	}
	fmt.Println()

	// ── Multimodal ────────────────────────────────────────────────────────────
	// Uncomment to use image input (requires a provider that supports it).
	//
	// imgResp, err := client.Complete(ctx, &deebus.Request{
	// 	Messages: []deebus.Message{
	// 		deebus.ImageMessage("user",
	// 			deebus.ImageSource{Type: "url", URL: "https://example.com/photo.jpg"},
	// 			"What is in this image?",
	// 		),
	// 	},
	// })

	// ── Usage statistics ──────────────────────────────────────────────────────
	total, tokens, success, failed := client.Stats.Get()
	fmt.Printf("\nStats: requests=%d tokens=%d success=%d failed=%d\n",
		total, tokens, success, failed)
}
