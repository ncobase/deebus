package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// mockTransport simulates an MCP server by replying to calls directly,
// without any subprocess or network.
type mockTransport struct {
	handlers map[string]func(params json.RawMessage) (any, error)
	notifyCh chan rpcMessage
	onNotify func(rpcMessage)
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		handlers: make(map[string]func(json.RawMessage) (any, error)),
		notifyCh: make(chan rpcMessage, 8),
	}
}

func (m *mockTransport) handle(method string, fn func(json.RawMessage) (any, error)) {
	m.handlers[method] = fn
}

func (m *mockTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	h, ok := m.handlers[method]
	if !ok {
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}

	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = b
	}

	// Run handler in goroutine so we can respect ctx cancellation.
	type res struct {
		data json.RawMessage
		err  error
	}
	done := make(chan res, 1)
	go func() {
		result, err := h(raw)
		if err != nil {
			done <- res{err: err}
			return
		}
		data, err := json.Marshal(result)
		done <- res{data: data, err: err}
	}()

	select {
	case r := <-done:
		return r.data, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *mockTransport) notify(_ string, _ any) error { return nil }

func (m *mockTransport) setNotificationHandler(h func(rpcMessage)) {
	m.onNotify = h
}

func (m *mockTransport) close() error { return nil }

// sendNotification triggers an in-process notification to the client.
func (m *mockTransport) sendNotification(method string) {
	if m.onNotify != nil {
		m.onNotify(rpcMessage{Method: method})
	}
}

func newTestClient(t *testing.T, tools []Tool) (*Client, *mockTransport) {
	t.Helper()

	mt := newMockTransport()

	// initialize handler.
	mt.handle("initialize", func(_ json.RawMessage) (any, error) {
		return initializeResult{
			ProtocolVersion: ProtocolVersion,
			ServerInfo:      Implementation{Name: "test-server", Version: "1.0.0"},
			Capabilities: ServerCapabilities{
				Tools: &struct {
					ListChanged bool `json:"listChanged,omitempty"`
				}{ListChanged: true},
			},
		}, nil
	})

	// tools/list handler (single page, no pagination for tests).
	mt.handle("tools/list", func(_ json.RawMessage) (any, error) {
		return listToolsResult{Tools: tools}, nil
	})

	c, err := newClient(context.Background(), mt, nil)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	return c, mt
}

func sampleTools() []Tool {
	schema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)
	return []Tool{
		{Name: "search", Description: "Search the web", InputSchema: schema},
		{Name: "calculate", Description: "Do math", InputSchema: json.RawMessage(`{"type":"object","properties":{"expr":{"type":"string"}}}`)},
	}
}

func TestClientInitialize(t *testing.T) {
	c, _ := newTestClient(t, sampleTools())
	if c.ServerInfo().Name != "test-server" {
		t.Errorf("server name = %q, want test-server", c.ServerInfo().Name)
	}
	if c.ServerCapabilities().Tools == nil {
		t.Error("expected tools capability")
	}
}

func TestClientTools(t *testing.T) {
	c, _ := newTestClient(t, sampleTools())

	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].Function.Name != "search" {
		t.Errorf("tool[0].Name = %q, want search", tools[0].Function.Name)
	}
	if tools[0].Function.Description != "Search the web" {
		t.Errorf("tool[0].Description = %q, want 'Search the web'", tools[0].Function.Description)
	}
	// Parameters should be converted from inputSchema.
	if tools[0].Function.Parameters["type"] != "object" {
		t.Errorf("tool[0].Parameters = %v, missing type:object", tools[0].Function.Parameters)
	}
}

func TestClientToolsCached(t *testing.T) {
	callCount := 0
	c, mt := newTestClient(t, sampleTools())

	// Override the handler to count calls.
	mt.handle("tools/list", func(_ json.RawMessage) (any, error) {
		callCount++
		return listToolsResult{Tools: sampleTools()}, nil
	})

	ctx := context.Background()
	if _, err := c.Tools(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Tools(ctx); err != nil {
		t.Fatal(err)
	}

	if callCount != 1 {
		t.Errorf("tools/list called %d times, expected 1 (cached)", callCount)
	}
}

func TestClientToolsCacheInvalidatedOnNotification(t *testing.T) {
	callCount := 0
	c, mt := newTestClient(t, sampleTools())

	mt.handle("tools/list", func(_ json.RawMessage) (any, error) {
		callCount++
		return listToolsResult{Tools: sampleTools()}, nil
	})

	ctx := context.Background()
	if _, err := c.Tools(ctx); err != nil {
		t.Fatal(err)
	}

	// Simulate server sending tools/list_changed.
	mt.sendNotification("notifications/tools/list_changed")

	if _, err := c.Tools(ctx); err != nil {
		t.Fatal(err)
	}

	if callCount != 2 {
		t.Errorf("tools/list called %d times, expected 2 after invalidation", callCount)
	}
}

func TestClientCallTool(t *testing.T) {
	c, mt := newTestClient(t, sampleTools())

	mt.handle("tools/call", func(params json.RawMessage) (any, error) {
		var p callToolParams
		_ = json.Unmarshal(params, &p)

		if p.Name != "search" {
			return nil, fmt.Errorf("unexpected tool: %s", p.Name)
		}
		return CallToolResult{
			Content: []ContentItem{{Type: "text", Text: "result: " + fmt.Sprint(p.Arguments["query"])}},
		}, nil
	})

	result, err := c.CallTool(context.Background(), "search", `{"query":"golang"}`)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Error("unexpected IsError=true")
	}
	if result.Text() != "result: golang" {
		t.Errorf("Text() = %q, want 'result: golang'", result.Text())
	}
}

func TestClientExecuteToolError(t *testing.T) {
	c, mt := newTestClient(t, sampleTools())

	mt.handle("tools/call", func(_ json.RawMessage) (any, error) {
		return CallToolResult{
			Content: []ContentItem{{Type: "text", Text: "rate limit exceeded"}},
			IsError: true,
		}, nil
	})

	// Execute should not return a Go error for tool-level errors.
	out, err := c.Execute(context.Background(), "search", `{"query":"test"}`)
	if err != nil {
		t.Fatalf("Execute returned unexpected Go error: %v", err)
	}
	if out != "[tool error] rate limit exceeded" {
		t.Errorf("output = %q", out)
	}
}

func TestClientExecuteProtocolError(t *testing.T) {
	c, mt := newTestClient(t, sampleTools())

	mt.handle("tools/call", func(_ json.RawMessage) (any, error) {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	})

	_, err := c.Execute(context.Background(), "search", `{}`)
	if err == nil {
		t.Fatal("expected error for protocol failure")
	}
}

func TestClientPaginatedTools(t *testing.T) {
	page1 := []Tool{{Name: "tool1", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	page2 := []Tool{{Name: "tool2", InputSchema: json.RawMessage(`{"type":"object"}`)}}

	c, mt := newTestClient(t, nil)

	callNum := 0
	mt.handle("tools/list", func(params json.RawMessage) (any, error) {
		callNum++
		var p listToolsParams
		_ = json.Unmarshal(params, &p)

		if p.Cursor == "" {
			return listToolsResult{Tools: page1, NextCursor: "page2"}, nil
		}
		return listToolsResult{Tools: page2}, nil
	})

	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 2 {
		t.Errorf("got %d tools, want 2", len(tools))
	}
	if callNum != 2 {
		t.Errorf("tools/list called %d times, want 2 for 2 pages", callNum)
	}
}

func TestCallToolResultText(t *testing.T) {
	r := CallToolResult{
		Content: []ContentItem{
			{Type: "text", Text: "hello"},
			{Type: "image", Data: "base64..."},
			{Type: "text", Text: "world"},
		},
	}
	got := r.Text()
	want := "hello\nworld"
	if got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
}

func TestMCPToolConversion(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"x": {"type": "number"}},
		"required": ["x"]
	}`)
	tools := []Tool{{
		Name:        "add",
		Description: "Add numbers",
		InputSchema: schema,
	}}

	converted := mcpToolsToProviders(tools)
	if len(converted) != 1 {
		t.Fatalf("got %d tools", len(converted))
	}
	if converted[0].Type != "function" {
		t.Errorf("Type = %q", converted[0].Type)
	}
	if converted[0].Function.Name != "add" {
		t.Errorf("Name = %q", converted[0].Function.Name)
	}
	if converted[0].Function.Parameters["type"] != "object" {
		t.Errorf("Parameters = %v", converted[0].Function.Parameters)
	}
}

func TestContextCancellation(t *testing.T) {
	mt := newMockTransport()
	// Slow initialize.
	mt.handle("initialize", func(_ json.RawMessage) (any, error) {
		time.Sleep(100 * time.Millisecond)
		return initializeResult{
			ProtocolVersion: ProtocolVersion,
			ServerInfo:      Implementation{Name: "slow", Version: "1"},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := newClient(ctx, mt, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}
