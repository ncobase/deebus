// Example 03: Agent Loop - RunAgent, RunAgentStream, hooks, history trimming.
//
// Shows how to:
// - Run a non-streaming agent loop with parallel tool execution.
// - Observe every action via AgentConfig.Hook for structured logging.
// - Run a streaming agent and receive tokens in real time between tool turns.
// - Trim conversation history to prevent context overflow (MaxHistoryMessages).
// - Collect the full conversation history after the loop ends.
//
// Run:
//
//	ANTHROPIC_API_KEY=sk-... go run .
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ncobase/deebus"
)

func main() {
	client, err := deebus.NewClient(deebus.Config{
		Providers: map[string]deebus.ProviderConfig{
			"anthropic": {
				Type:    "anthropic",
				APIKey:  requireEnv("ANTHROPIC_API_KEY"),
				BaseURL: "https://api.anthropic.com",
			},
		},
		Primary: "anthropic/claude-opus-4-6",
		Timeout: 60,
		Retry:   2,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	blockingAgent(ctx, client)
	streamingAgent(ctx, client)
	longRunningAgent(ctx, client)
}

var tools = []deebus.Tool{
	{
		Type: "function",
		Function: deebus.FunctionSchema{
			Name:        "search",
			Description: "Search for information on a topic.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
				"required":   []string{"query"},
			},
		},
	},
	{
		Type: "function",
		Function: deebus.FunctionSchema{
			Name:        "calculate",
			Description: "Evaluate a mathematical expression.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"expression": map[string]any{"type": "string"}},
				"required":   []string{"expression"},
			},
		},
	},
}

// toolFn is the AgentToolFunc that the agent loop calls for every tool.
func toolFn(_ context.Context, name, argsJSON string) (string, error) {
	var args map[string]any
	_ = json.Unmarshal([]byte(argsJSON), &args)

	switch name {
	case "search":
		query, _ := args["query"].(string)
		// Stub - replace with a real search API call.
		return fmt.Sprintf(`{"results":["Result A for %q","Result B for %q"]}`, query, query), nil

	case "calculate":
		expr, _ := args["expression"].(string)
		// Stub - replace with a real expression evaluator.
		return fmt.Sprintf(`{"expression":%q,"result":42}`, expr), nil
	}

	return "", fmt.Errorf("unknown tool: %s", name)
}

func blockingAgent(ctx context.Context, client *deebus.Client) {
	fmt.Println("RunAgent")

	answer, history, err := client.RunAgent(ctx,
		&deebus.Request{
			Messages: []deebus.Message{
				deebus.TextMessage("system", "You are a research assistant. Use tools to gather information before answering."),
				deebus.TextMessage("user", "Search for 'Go concurrency patterns' and calculate 2^10, then summarise."),
			},
			Tools: tools,
		},
		toolFn,
		deebus.AgentConfig{
			MaxIterations:   8,
			DisableParallel: false, // execute independent tool calls concurrently (default)
			Hook:            logHook,
		},
	)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}

	fmt.Printf("\nAnswer: %s\n", answer)
	fmt.Printf("History: %d messages\n\n", len(history))
}

func streamingAgent(ctx context.Context, client *deebus.Client) {
	fmt.Println("RunAgentStream")

	histCh := make(chan []deebus.Message, 1)

	stream, err := client.RunAgentStream(ctx,
		&deebus.Request{
			Messages: []deebus.Message{
				deebus.TextMessage("user", "Search for 'circuit breaker pattern' and write a one-sentence summary."),
			},
			Tools: tools,
		},
		toolFn,
		histCh,
		deebus.AgentConfig{
			MaxIterations: 5,
			Hook:          logHook,
		},
	)
	if err != nil {
		log.Fatalf("stream agent: %v", err)
	}

	fmt.Print("Stream: ")
	for chunk := range stream {
		if chunk.Error != nil {
			fmt.Printf("\n[error] %v\n", chunk.Error)
			break
		}
		if chunk.Content != "" {
			fmt.Print(chunk.Content)
		}
		if chunk.Done && len(chunk.ToolCalls) > 0 {
			// Tool-call turn ending - a blank line separates turns visually.
			fmt.Print(" [tool turn] ")
		}
	}

	history := <-histCh
	fmt.Printf("\nHistory: %d messages\n\n", len(history))
}

func longRunningAgent(ctx context.Context, client *deebus.Client) {
	fmt.Println("Long-running agent")

	// Simulate a conversation that would exceed the context window.
	// MaxHistoryMessages trims oldest turns while preserving the system message.
	answer, history, err := client.RunAgent(ctx,
		&deebus.Request{
			Messages: []deebus.Message{
				deebus.TextMessage("system", "You are a helpful assistant."),
				deebus.TextMessage("user", "Search for topics: Go, Rust, Zig, then summarise all three."),
			},
			Tools: tools,
		},
		toolFn,
		deebus.AgentConfig{
			MaxIterations:      10,
			MaxHistoryMessages: 10, // keeps system + 9 most recent messages
			Hook:               logHook,
		},
	)
	if err != nil {
		log.Fatalf("long agent: %v", err)
	}

	fmt.Printf("\nAnswer: %s\n", answer)
	fmt.Printf("History: %d messages (trimmed to max 10)\n\n", len(history))
}

func logHook(ev deebus.AgentEvent) {
	switch ev.Type {
	case deebus.EventLLMRequest:
		fmt.Printf("  [iter %d] llm_request\n", ev.Iteration)
	case deebus.EventLLMResponse:
		fmt.Printf("  [iter %d] llm_response tokens=%d elapsed=%s\n",
			ev.Iteration, ev.TokensUsed, round(ev.Duration))
	case deebus.EventToolCall:
		fmt.Printf("  [iter %d] tool_call %s(%s)\n",
			ev.Iteration, ev.ToolName, truncate(ev.Input, 60))
	case deebus.EventToolResult:
		fmt.Printf("  [iter %d] tool_result %s result=%s elapsed=%s\n",
			ev.Iteration, ev.ToolName, truncate(ev.Output, 40), round(ev.Duration))
	case deebus.EventDone:
		fmt.Printf("  [iter %d] done\n", ev.Iteration)
	case deebus.EventError:
		fmt.Printf("  [iter %d] error: %v\n", ev.Iteration, ev.Err)
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func round(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("environment variable %s is required", key)
	}
	return v
}
