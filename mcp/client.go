package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ncobase/deebus/providers"
)

// transport is the internal interface implemented by stdioTransport and
// httpTransport. It is not exported; callers use Client directly.
type transport interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(method string, params any) error
	setNotificationHandler(func(rpcMessage))
	close() error
}

// Client is an MCP client. Create one via NewStdioClient or NewHTTPClient,
// then use Tools to fetch tool definitions and Execute to run tool calls.
// Client is safe for concurrent use after initialization.
type Client struct {
	t          transport
	serverInfo Implementation
	serverCaps ServerCapabilities

	toolsMu    sync.RWMutex
	toolsCache []Tool // refreshed on tools/list_changed notifications
	toolsDirty bool
}

// clientOptions holds resolved options before the Client is constructed.
type clientOptions struct {
	extraNotify func(string, json.RawMessage)
}

// ClientOption configures a Client.
type ClientOption func(*clientOptions)

// WithNotificationHandler installs a handler for server-initiated notifications
// that are not handled internally (e.g. progress, logging, resource updates).
// The handler receives the notification method name and raw JSON params.
func WithNotificationHandler(h func(method string, params json.RawMessage)) ClientOption {
	return func(o *clientOptions) {
		o.extraNotify = h
	}
}

// newClient initializes the MCP handshake and returns a ready Client.
func newClient(ctx context.Context, t transport, extraNotify func(string, json.RawMessage)) (*Client, error) {
	c := &Client{t: t}

	// Wire notification handler: handle tools/list_changed internally,
	// forward everything else to extraNotify.
	t.setNotificationHandler(func(msg rpcMessage) {
		if msg.Method == "notifications/tools/list_changed" {
			c.toolsMu.Lock()
			c.toolsDirty = true
			c.toolsMu.Unlock()
		}
		if extraNotify != nil {
			extraNotify(msg.Method, msg.Params)
		}
	})

	// Initialize handshake.
	raw, err := t.call(ctx, "initialize", initializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    ClientCapabilities{},
		ClientInfo:      Implementation{Name: "deebus-mcp", Version: "1.0.0"},
	})
	if err != nil {
		return nil, fmt.Errorf("mcp: initialize: %w", err)
	}

	var result initializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: decode initialize result: %w", err)
	}

	c.serverInfo = result.ServerInfo
	c.serverCaps = result.Capabilities

	// Confirm initialization.
	if err := t.notify("notifications/initialized", nil); err != nil {
		return nil, fmt.Errorf("mcp: initialized notification: %w", err)
	}

	return c, nil
}

// NewStdioClient launches command with args and env appended to the current
// environment, performs the MCP handshake, and returns a ready Client.
func NewStdioClient(ctx context.Context, command string, args, env []string, opts ...ClientOption) (*Client, error) {
	t, err := newStdioTransport(ctx, command, args, env)
	if err != nil {
		return nil, err
	}
	return newClient(ctx, t, resolveOptions(opts))
}

// NewHTTPClient connects to an MCP server at endpoint using the Streamable
// HTTP transport (spec 2025-03-26) and performs the initialization handshake.
//
//	c, err := mcp.NewHTTPClient(ctx, "https://mcp.example.com/mcp", 30*time.Second)
func NewHTTPClient(ctx context.Context, endpoint string, timeout time.Duration, opts ...ClientOption) (*Client, error) {
	t := newHTTPTransport(endpoint, timeout)
	return newClient(ctx, t, resolveOptions(opts))
}

func resolveOptions(opts []ClientOption) func(string, json.RawMessage) {
	o := &clientOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o.extraNotify
}

// Tools fetches all tool definitions from the server (paginated internally)
// and returns them converted to the deebus providers.Tool format.
//
// Results are cached and refreshed automatically when the server sends a
// notifications/tools/list_changed notification.
func (c *Client) Tools(ctx context.Context) ([]providers.Tool, error) {
	c.toolsMu.RLock()
	cached := c.toolsCache
	dirty := c.toolsDirty
	c.toolsMu.RUnlock()

	if cached != nil && !dirty {
		return mcpToolsToProviders(cached), nil
	}

	tools, err := c.fetchAllTools(ctx)
	if err != nil {
		return nil, err
	}

	c.toolsMu.Lock()
	c.toolsCache = tools
	c.toolsDirty = false
	c.toolsMu.Unlock()

	return mcpToolsToProviders(tools), nil
}

// fetchAllTools retrieves all pages of tools/list.
func (c *Client) fetchAllTools(ctx context.Context) ([]Tool, error) {
	var all []Tool
	var cursor string

	for {
		params := listToolsParams{Cursor: cursor}
		raw, err := c.t.call(ctx, "tools/list", params)
		if err != nil {
			return nil, fmt.Errorf("mcp: tools/list: %w", err)
		}

		var result listToolsResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("mcp: decode tools/list: %w", err)
		}

		all = append(all, result.Tools...)

		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}

	return all, nil
}

// CallTool invokes a tool on the MCP server by name with JSON-encoded args and
// returns the full CallToolResult (including the IsError flag).
//
// Most callers should use Execute, which returns just the text and propagates
// IsError as a Go error.
func (c *Client) CallTool(ctx context.Context, name, argsJSON string) (*CallToolResult, error) {
	var args map[string]any
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return nil, fmt.Errorf("mcp: parse tool args: %w", err)
		}
	}

	raw, err := c.t.call(ctx, "tools/call", callToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/call %q: %w", name, err)
	}

	var result CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: decode tools/call result: %w", err)
	}

	return &result, nil
}

// Execute calls a tool and returns its text output, compatible with
// deebus.AgentToolFunc. When the tool signals an error (IsError=true), Execute
// returns the error text so the model can see and recover from it.
//
//	deebusClient.RunAgent(ctx, req, mcpClient.Execute)
func (c *Client) Execute(ctx context.Context, name, argsJSON string) (string, error) {
	result, err := c.CallTool(ctx, name, argsJSON)
	if err != nil {
		return "", err
	}

	text := result.Text()

	// IsError=true means the tool itself reported a failure. We return the
	// error text rather than a Go error so the model can observe and react.
	// This follows MCP's design: tool errors are part of the conversation,
	// not fatal protocol failures.
	if result.IsError {
		return fmt.Sprintf("[tool error] %s", text), nil
	}

	return text, nil
}

// ServerInfo returns the server's self-reported name and version.
func (c *Client) ServerInfo() Implementation { return c.serverInfo }

// ServerCapabilities returns the capabilities the server declared during init.
func (c *Client) ServerCapabilities() ServerCapabilities { return c.serverCaps }

// Close shuts down the transport and, for stdio clients, waits for the
// subprocess to exit.
func (c *Client) Close() error {
	return c.t.close()
}

// mcpToolsToProviders converts MCP tool definitions to the unified
// providers.Tool format expected by deebus requests.
func mcpToolsToProviders(tools []Tool) []providers.Tool {
	out := make([]providers.Tool, len(tools))
	for i, t := range tools {
		var params map[string]any
		if err := json.Unmarshal(t.InputSchema, &params); err != nil {
			params = map[string]any{"type": "object"}
		}
		out[i] = providers.Tool{
			Type: "function",
			Function: providers.FunctionSchema{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		}
	}
	return out
}
