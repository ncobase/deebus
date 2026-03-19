// Example 05: MCP Client — stdio and HTTP transports, agent integration.
//
// Shows how to:
//   - Connect to a local MCP server launched as a subprocess (stdio transport).
//   - Connect to a remote MCP server via Streamable HTTP (spec 2025-03-26).
//   - Fetch tool definitions and run an agent that calls MCP tools automatically.
//   - Observe tool list change notifications.
//   - Call MCP tools directly without an agent loop.
//
// Prerequisites for the stdio demo:
//
//	npm install -g @modelcontextprotocol/server-filesystem
//	  (or let npx fetch it on first run)
//
// Run:
//
//	ANTHROPIC_API_KEY=sk-... go run .
//
// To test the HTTP transport, set MCP_HTTP_ENDPOINT to a running MCP server URL:
//
//	MCP_HTTP_ENDPOINT=https://mcp.example.com/mcp go run .
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ncobase/deebus"
	"github.com/ncobase/deebus/mcp"
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

	stdioDemo(ctx, client)
	httpDemo(ctx, client)
}

// ── stdio transport ───────────────────────────────────────────────────────────

func stdioDemo(ctx context.Context, client *deebus.Client) {
	fmt.Println("── MCP stdio (filesystem server) ────────────────────────────────────")

	// Launch the MCP filesystem server as a child process.
	// The server communicates via its stdin/stdout using newline-delimited JSON-RPC 2.0.
	mcpClient, err := mcp.NewStdioClient(ctx,
		"npx",
		[]string{"-y", "--quiet", "@modelcontextprotocol/server-filesystem", os.TempDir()},
		nil, // additional env vars
	)
	if err != nil {
		fmt.Printf("stdio server unavailable (%v); skipping.\n\n", err)
		return
	}
	defer mcpClient.Close()

	fmt.Printf("Server  : %s %s\n",
		mcpClient.ServerInfo().Name, mcpClient.ServerInfo().Version)

	// Tools() fetches all pages of tools/list, caches the result, and auto-invalidates
	// the cache when the server sends a notifications/tools/list_changed notification.
	tools, err := mcpClient.Tools(ctx)
	if err != nil {
		fmt.Printf("tools/list error: %v\n\n", err)
		return
	}
	fmt.Printf("Tools   : %d available\n", len(tools))
	for _, t := range tools {
		fmt.Printf("          • %s — %s\n", t.Function.Name, t.Function.Description)
	}
	fmt.Println()

	// Direct tool call — useful when you know exactly which tool to call.
	directCall(ctx, mcpClient)

	// Agent integration — pass mcpClient.Execute as the AgentToolFunc.
	agentWithMCP(ctx, client, mcpClient)
}

// directCall invokes a single MCP tool and prints the result.
func directCall(ctx context.Context, mcpClient *mcp.Client) {
	fmt.Println("  Direct tool call:")

	result, err := mcpClient.CallTool(ctx, "list_directory",
		fmt.Sprintf(`{"path":%q}`, os.TempDir()))
	if err != nil {
		fmt.Printf("  CallTool error: %v\n\n", err)
		return
	}

	if result.IsError {
		// IsError=true means the tool itself reported a failure (not a protocol error).
		// The text contains the error message so the model can observe and recover.
		fmt.Printf("  tool error: %s\n\n", result.Text())
		return
	}

	fmt.Printf("  %s\n\n", truncate(result.Text(), 120))
}

// agentWithMCP wires an MCP client into RunAgent via mcpClient.Execute.
func agentWithMCP(ctx context.Context, client *deebus.Client, mcpClient *mcp.Client) {
	fmt.Println("  Agent + MCP:")

	tools, err := mcpClient.Tools(ctx)
	if err != nil {
		fmt.Printf("  tools error: %v\n\n", err)
		return
	}

	answer, history, err := client.RunAgent(ctx,
		&deebus.Request{
			Messages: []deebus.Message{
				deebus.TextMessage("system", "You are a helpful assistant. Use the provided tools to answer questions about the filesystem."),
				deebus.TextMessage("user", fmt.Sprintf("How many files are in %s? List their names.", os.TempDir())),
			},
			Tools: tools,
		},
		mcpClient.Execute, // mcpClient.Execute matches the AgentToolFunc signature
		deebus.AgentConfig{
			MaxIterations: 5,
			Hook: func(ev deebus.AgentEvent) {
				if ev.ToolName != "" {
					fmt.Printf("  [%s] %s\n", ev.Type, ev.ToolName)
				}
			},
		},
	)
	if err != nil {
		fmt.Printf("  agent error: %v\n\n", err)
		return
	}

	fmt.Printf("  Answer  : %s\n", answer)
	fmt.Printf("  History : %d messages\n\n", len(history))
}

// ── HTTP transport ────────────────────────────────────────────────────────────

func httpDemo(ctx context.Context, client *deebus.Client) {
	endpoint := os.Getenv("MCP_HTTP_ENDPOINT")
	if endpoint == "" {
		fmt.Println("── MCP HTTP (skipped — set MCP_HTTP_ENDPOINT to enable) ─────────────")
		fmt.Println()
		return
	}

	fmt.Println("── MCP HTTP (Streamable HTTP, spec 2025-03-26) ───────────────────────")
	fmt.Printf("Endpoint: %s\n", endpoint)

	mcpClient, err := mcp.NewHTTPClient(ctx, endpoint, 30*time.Second,
		mcp.WithNotificationHandler(func(method string, params json.RawMessage) {
			fmt.Printf("  notification: %s\n", method)
		}),
	)
	if err != nil {
		fmt.Printf("HTTP connect error: %v\n\n", err)
		return
	}
	defer mcpClient.Close()

	fmt.Printf("Server  : %s %s\n",
		mcpClient.ServerInfo().Name, mcpClient.ServerInfo().Version)

	tools, err := mcpClient.Tools(ctx)
	if err != nil {
		fmt.Printf("tools/list error: %v\n\n", err)
		return
	}
	fmt.Printf("Tools   : %d available\n\n", len(tools))

	// Run an agent backed by the remote MCP server.
	answer, _, err := client.RunAgent(ctx,
		&deebus.Request{
			Messages: []deebus.Message{
				deebus.TextMessage("user", "What can you help me with?"),
			},
			Tools: tools,
		},
		mcpClient.Execute,
		deebus.AgentConfig{MaxIterations: 3},
	)
	if err != nil {
		fmt.Printf("agent error: %v\n\n", err)
		return
	}
	fmt.Printf("Answer: %s\n\n", answer)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("environment variable %s is required", key)
	}
	return v
}
