// Package mcp provides a client for the Model Context Protocol (MCP),
// allowing deebus agents to discover and invoke tools exposed by any
// MCP-compatible server.
//
// Supported transports:
//   - stdio   — launches a local subprocess (most common for CLI tools)
//   - HTTP    — connects to a remote server via Streamable HTTP (spec 2025-03-26)
//
// Quick start:
//
//	// stdio (e.g. filesystem MCP server)
//	c, _ := mcp.NewStdioClient(ctx, "npx", []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}, nil)
//	defer c.Close()
//
//	tools, _ := c.Tools(ctx)
//	deebusClient.RunAgent(ctx, req, c.Execute)
package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ─── JSON-RPC 2.0 ─────────────────────────────────────────────────────────────

const jsonrpcVersion = "2.0"

// rpcMessage is a unified JSON-RPC 2.0 message. The fields present determine
// the message type:
//   - Request:      id != 0, method != ""
//   - Response:     id != 0, method == ""
//   - Notification: id == 0, method != ""
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message)
}

// ─── MCP protocol types ───────────────────────────────────────────────────────

// ProtocolVersion is the MCP spec version this client targets.
const ProtocolVersion = "2025-03-26"

// Implementation identifies a client or server.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities describes what this client supports.
type ClientCapabilities struct {
	Experimental map[string]any `json:"experimental,omitempty"`
}

// ServerCapabilities describes what a server supports.
type ServerCapabilities struct {
	Logging     *struct{} `json:"logging,omitempty"`
	Completions *struct{} `json:"completions,omitempty"`
	Prompts     *struct {
		ListChanged bool `json:"listChanged,omitempty"`
	} `json:"prompts,omitempty"`
	Resources *struct {
		Subscribe   bool `json:"subscribe,omitempty"`
		ListChanged bool `json:"listChanged,omitempty"`
	} `json:"resources,omitempty"`
	Tools *struct {
		ListChanged bool `json:"listChanged,omitempty"`
	} `json:"tools,omitempty"`
}

// initializeParams is the body of the `initialize` request.
type initializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// initializeResult is the body of the `initialize` response.
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// ─── Tool types ───────────────────────────────────────────────────────────────

// Tool is an MCP tool definition as returned by tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations provides hints about a tool's behaviour.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint,omitempty"`
	DestructiveHint bool   `json:"destructiveHint,omitempty"`
	IdempotentHint  bool   `json:"idempotentHint,omitempty"`
	OpenWorldHint   bool   `json:"openWorldHint,omitempty"`
}

// listToolsParams is the body of the tools/list request.
type listToolsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

// listToolsResult is the body of the tools/list response.
type listToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// callToolParams is the body of the tools/call request.
type callToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ContentItem is one element of a tool result's content array.
type ContentItem struct {
	Type     string `json:"type"`               // "text", "image", "audio", "resource"
	Text     string `json:"text,omitempty"`     // for type=text
	Data     string `json:"data,omitempty"`     // base64, for type=image/audio
	MimeType string `json:"mimeType,omitempty"` // for type=image/audio
}

// CallToolResult is the response to a tools/call request.
type CallToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// Text returns the concatenated text from all text content items.
func (r CallToolResult) Text() string {
	var sb strings.Builder
	for _, item := range r.Content {
		if item.Type == "text" && item.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(item.Text)
		}
	}
	return sb.String()
}
