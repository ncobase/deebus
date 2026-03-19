// Example 02: Tool Calling — single-turn and multi-turn (manual history).
//
// Shows how to:
//   - Define tool schemas in the unified format.
//   - Detect and dispatch tool calls from a single-turn response.
//   - Build a multi-turn conversation manually using AssistantMessage and
//     ToolResultMessage so the model can see results and continue.
//   - Handle parallel tool calls (multiple tools in one response).
//
// Run:
//
//	OPENAI_API_KEY=sk-... go run .
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/ncobase/deebus"
)

func main() {
	client, err := deebus.NewClient(deebus.Config{
		Providers: map[string]deebus.ProviderConfig{
			"openai": {
				Type:    "openai",
				APIKey:  requireEnv("OPENAI_API_KEY"),
				BaseURL: "https://api.openai.com",
			},
		},
		Primary: "openai/gpt-4o",
		Timeout: 30,
		Retry:   2,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	singleTurn(ctx, client)
	multiTurn(ctx, client)
	parallelTools(ctx, client)
}

// ── Tool definitions ──────────────────────────────────────────────────────────

var weatherTool = deebus.Tool{
	Type: "function",
	Function: deebus.FunctionSchema{
		Name:        "get_weather",
		Description: "Return current weather for a city.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{
					"type":        "string",
					"description": "City name, e.g. Tokyo",
				},
				"unit": map[string]any{
					"type": "string",
					"enum": []string{"celsius", "fahrenheit"},
				},
			},
			"required": []string{"city"},
		},
	},
}

var calendarTool = deebus.Tool{
	Type: "function",
	Function: deebus.FunctionSchema{
		Name:        "get_calendar",
		Description: "Return upcoming events for a date.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"date": map[string]any{
					"type":        "string",
					"description": "ISO 8601 date, e.g. 2026-03-19",
				},
			},
			"required": []string{"date"},
		},
	},
}

// ── Single-turn: detect tool calls ───────────────────────────────────────────

func singleTurn(ctx context.Context, client *deebus.Client) {
	fmt.Println("── Single-turn tool calling ─────────────────────────────────────────")

	resp, err := client.Complete(ctx, &deebus.Request{
		Messages:   []deebus.Message{deebus.TextMessage("user", "What is the weather in Tokyo?")},
		Tools:      []deebus.Tool{weatherTool},
		ToolChoice: "auto",
	})
	if err != nil {
		log.Fatalf("complete: %v", err)
	}

	if len(resp.ToolCalls) == 0 {
		fmt.Println("Model answered directly:", resp.Content)
		return
	}

	for _, tc := range resp.ToolCalls {
		result := executeWeather(tc.Function.Arguments)
		fmt.Printf("  tool=%-12s args=%s\n  result=%s\n\n",
			tc.Function.Name, tc.Function.Arguments, result)
	}
}

// ── Multi-turn: build conversation history manually ───────────────────────────

func multiTurn(ctx context.Context, client *deebus.Client) {
	fmt.Println("── Multi-turn tool calling ──────────────────────────────────────────")

	tools := []deebus.Tool{weatherTool}
	messages := []deebus.Message{
		deebus.TextMessage("system", "You are a helpful assistant. Use tools when needed."),
		deebus.TextMessage("user", "Compare the weather in Tokyo and Paris."),
	}

	for turn := 0; turn < 5; turn++ {
		resp, err := client.Complete(ctx, &deebus.Request{
			Messages:   messages,
			Tools:      tools,
			ToolChoice: "auto",
		})
		if err != nil {
			log.Fatalf("turn %d: %v", turn, err)
		}

		if len(resp.ToolCalls) == 0 {
			// Model produced a final answer — conversation is complete.
			fmt.Println("Final answer:", resp.Content)
			fmt.Println()
			return
		}

		// Append the assistant's tool-call turn to history.
		messages = append(messages, deebus.AssistantMessage(resp.Content, resp.ToolCalls))

		// Execute each tool and append its result.
		for _, tc := range resp.ToolCalls {
			result := executeWeather(tc.Function.Arguments)
			fmt.Printf("  → %s(%s) = %s\n", tc.Function.Name, tc.Function.Arguments, result)

			// ToolResultMessage links the result back to the tool call by ID.
			messages = append(messages, deebus.ToolResultMessage(tc.ID, tc.Function.Name, result))
		}
	}

	fmt.Println("(max turns reached)")
}

// ── Parallel tool calls (multiple tools in one response) ──────────────────────

func parallelTools(ctx context.Context, client *deebus.Client) {
	fmt.Println("── Parallel tool calls ──────────────────────────────────────────────")

	messages := []deebus.Message{
		deebus.TextMessage("user",
			"What is the weather in Tokyo today, and what events do I have on 2026-03-19?"),
	}
	tools := []deebus.Tool{weatherTool, calendarTool}

	resp, err := client.Complete(ctx, &deebus.Request{
		Messages:   messages,
		Tools:      tools,
		ToolChoice: "auto",
	})
	if err != nil {
		log.Fatalf("complete: %v", err)
	}

	if len(resp.ToolCalls) == 0 {
		fmt.Println("No tool calls:", resp.Content)
		return
	}

	fmt.Printf("Model requested %d tool call(s) in one turn:\n", len(resp.ToolCalls))
	messages = append(messages, deebus.AssistantMessage(resp.Content, resp.ToolCalls))

	for _, tc := range resp.ToolCalls {
		var result string
		switch tc.Function.Name {
		case "get_weather":
			result = executeWeather(tc.Function.Arguments)
		case "get_calendar":
			result = `{"date":"2026-03-19","events":["10:00 Team standup","14:00 Architecture review"]}`
		}
		fmt.Printf("  %s → %s\n", tc.Function.Name, result)
		messages = append(messages, deebus.ToolResultMessage(tc.ID, tc.Function.Name, result))
	}

	final, err := client.Complete(ctx, &deebus.Request{Messages: messages, Tools: tools})
	if err != nil {
		log.Fatalf("final: %v", err)
	}
	fmt.Println("\nFinal answer:", final.Content)
	fmt.Println()
}

// ── Stub tool implementations ─────────────────────────────────────────────────

func executeWeather(argsJSON string) string {
	var args struct {
		City string `json:"city"`
		Unit string `json:"unit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return `{"error":"invalid arguments"}`
	}
	unit := args.Unit
	if unit == "" {
		unit = "celsius"
	}
	// Stub response — replace with a real weather API call.
	return fmt.Sprintf(`{"city":%q,"temperature":22,"unit":%q,"condition":"partly cloudy"}`,
		args.City, unit)
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("environment variable %s is required", key)
	}
	return v
}
