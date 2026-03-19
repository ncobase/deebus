package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"

	"github.com/ncobase/deebus"
	"github.com/ncobase/deebus/mcp"
)

// slogAdapter bridges Go's standard slog to the deebus Logger interface.
type slogAdapter struct{}

func (slogAdapter) Debug(msg string, fields ...any) { slog.Debug(msg, fields...) }
func (slogAdapter) Info(msg string, fields ...any)  { slog.Info(msg, fields...) }
func (slogAdapter) Warn(msg string, fields ...any)  { slog.Warn(msg, fields...) }
func (slogAdapter) Error(msg string, fields ...any) { slog.Error(msg, fields...) }

func main() {
	// LoadConfig reads deebus.yaml and expands ${ENV_VAR} references.
	client, err := deebus.LoadConfig("deebus.yaml")
	if err != nil {
		log.Fatal(err)
	}
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
	fmt.Printf("Tokens   : %d\n\n", resp.TokensUsed)

	// ── Streaming ─────────────────────────────────────────────────────────────
	fmt.Print("Streaming: ")
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
			log.Fatalf("stream error: %v", chunk.Error)
		}
		fmt.Print(chunk.Content)
		if chunk.Done {
			break
		}
	}
	fmt.Println()

	// ── Tool calling ──────────────────────────────────────────────────────────
	tools := []deebus.Tool{{
		Type: "function",
		Function: deebus.FunctionSchema{
			Name:        "get_weather",
			Description: "Return current weather for a city",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string", "description": "City name"},
				},
				"required": []string{"city"},
			},
		},
	}}

	toolResp, err := client.Complete(ctx, &deebus.Request{
		Messages:   []deebus.Message{deebus.SimpleMessage("user", "What is the weather in Tokyo?")},
		Tools:      tools,
		ToolChoice: "auto",
	})
	if err != nil {
		log.Fatalf("tool complete: %v", err)
	}
	for _, tc := range toolResp.ToolCalls {
		fmt.Printf("Tool call: %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
	}

	// ── Agent loop ────────────────────────────────────────────────────────────
	// RunAgent automates the tool-call loop: model → tools (parallel) → model → …
	answer, history, err := client.RunAgent(ctx,
		&deebus.Request{
			Messages: []deebus.Message{
				deebus.SimpleMessage("user", "What is the weather in Tokyo and London?"),
			},
			Tools: tools,
		},
		func(_ context.Context, name, argsJSON string) (string, error) {
			// Replace with real tool execution.
			return fmt.Sprintf(`{"city":%q,"temp":"22°C","condition":"sunny"}`, name), nil
		},
		deebus.AgentConfig{
			MaxIterations: 5,
			Hook: func(ev deebus.AgentEvent) {
				if ev.ToolName != "" {
					fmt.Printf("  [agent] %s → %s\n", ev.Type, ev.ToolName)
				}
			},
		},
	)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	fmt.Printf("\nAgent answer : %s\n", answer)
	fmt.Printf("Agent history: %d messages\n\n", len(history))

	// ── MCP client ────────────────────────────────────────────────────────────
	// Connect to any MCP-compatible server (stdio or HTTP).
	// This example requires `npx` and network access; skip if unavailable.
	mcpDemo(ctx, client)

	// ── Statistics ────────────────────────────────────────────────────────────
	total, tokens, success, failed := client.Stats.Get()
	fmt.Printf("Stats: requests=%d tokens=%d success=%d failed=%d\n",
		total, tokens, success, failed)
}

func mcpDemo(ctx context.Context, client *deebus.Client) {
	// Launch the official MCP filesystem server as a subprocess.
	// Requires: npx (Node.js) and internet access for the first run.
	mcpClient, err := mcp.NewStdioClient(ctx,
		"npx", []string{"-y", "--quiet", "@modelcontextprotocol/server-filesystem", "/tmp"}, nil)
	if err != nil {
		fmt.Printf("MCP server unavailable (%v); skipping MCP demo.\n\n", err)
		return
	}
	defer mcpClient.Close()

	fmt.Printf("MCP server : %s %s\n",
		mcpClient.ServerInfo().Name, mcpClient.ServerInfo().Version)

	// Fetch tool definitions and convert them to deebus format automatically.
	tools, err := mcpClient.Tools(ctx)
	if err != nil {
		fmt.Printf("MCP tools error: %v\n\n", err)
		return
	}
	fmt.Printf("MCP tools  : %d available\n", len(tools))

	// Run an agent that uses MCP tools without any manual wiring.
	answer, _, err := client.RunAgent(ctx,
		&deebus.Request{
			Messages: []deebus.Message{
				deebus.SimpleMessage("user", "List all files in /tmp."),
			},
			Tools: tools,
		},
		mcpClient.Execute,
		deebus.AgentConfig{MaxIterations: 5},
	)
	if err != nil {
		fmt.Printf("MCP agent error: %v\n\n", err)
		return
	}
	fmt.Printf("MCP agent  : %s\n\n", answer)
}
