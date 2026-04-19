package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// httpTransport implements the MCP Streamable HTTP transport (spec 2025-03-26).
//
// Each JSON-RPC call is a separate HTTP POST to the server endpoint. The server
// may respond with:
// - application/json         - direct JSON response (common for simple calls)
// - text/event-stream        - SSE stream containing one or more responses
//
// Session management: after initialization the server may return a
// Mcp-Session-Id header. The client includes it on all subsequent requests.
type httpTransport struct {
	endpoint  string
	client    *http.Client
	sessionID atomic.Value // stores string

	nextID   atomic.Int64
	notifyMu sync.Mutex
	onNotify func(rpcMessage)
}

func newHTTPTransport(endpoint string, timeout time.Duration) *httpTransport {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &httpTransport{
		endpoint: endpoint,
		client:   &http.Client{Timeout: timeout},
	}
}

func (t *httpTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := t.nextID.Add(1)

	body := map[string]any{
		"jsonrpc": jsonrpcVersion,
		"id":      id,
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("mcp http: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mcp http: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sid, _ := t.sessionID.Load().(string); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp http: do request: %w", err)
	}
	defer resp.Body.Close()

	// Store session ID returned by server.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionID.Store(sid)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("mcp http: session expired (404), re-initialize")
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mcp http: server error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return t.readSSEResponse(resp.Body, id)
	}

	// JSON response.
	var msg rpcMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("mcp http: decode response: %w", err)
	}
	if msg.Error != nil {
		return nil, msg.Error
	}
	return msg.Result, nil
}

// readSSEResponse reads an SSE stream until it finds the response matching id,
// dispatching any interleaved notifications to the notification handler.
func (t *httpTransport) readSSEResponse(body io.Reader, id int64) (json.RawMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1<<20), 4<<20)

	var eventData strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Blank line = end of event.
			raw := strings.TrimSpace(eventData.String())
			eventData.Reset()

			if raw == "" {
				continue
			}

			var msg rpcMessage
			if err := json.Unmarshal([]byte(raw), &msg); err != nil {
				continue
			}

			if msg.ID == id && msg.Method == "" {
				// Our response.
				if msg.Error != nil {
					return nil, msg.Error
				}
				return msg.Result, nil
			}

			// Notification or unrelated server message.
			t.notifyMu.Lock()
			h := t.onNotify
			t.notifyMu.Unlock()
			if h != nil && msg.Method != "" {
				h(msg)
			}
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			if eventData.Len() > 0 {
				eventData.WriteByte('\n')
			}
			eventData.WriteString(strings.TrimPrefix(line, "data: "))
		}
		// Ignore other SSE fields (event:, id:, retry:).
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mcp http: read sse: %w", err)
	}
	return nil, fmt.Errorf("mcp http: sse stream ended without response for id %d", id)
}

func (t *httpTransport) notify(method string, params any) error {
	n := map[string]any{
		"jsonrpc": jsonrpcVersion,
		"method":  method,
	}
	if params != nil {
		n["params"] = params
	}
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sid, _ := t.sessionID.Load().(string); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (t *httpTransport) setNotificationHandler(h func(rpcMessage)) {
	t.notifyMu.Lock()
	t.onNotify = h
	t.notifyMu.Unlock()
}

// close sends a DELETE to signal session termination (spec section 6.4).
func (t *httpTransport) close() error {
	sid, _ := t.sessionID.Load().(string)
	if sid == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodDelete, t.endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Mcp-Session-Id", sid)
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
