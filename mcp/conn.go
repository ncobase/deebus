package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// conn handles JSON-RPC 2.0 request/response correlation over a streaming
// reader (e.g. subprocess stdout or an SSE body). Multiple goroutines may
// call send concurrently; responses are dispatched to waiting callers by ID.
type conn struct {
	// send writes one complete newline-terminated JSON message.
	send func([]byte) error

	mu      sync.Mutex
	pending map[int64]chan rpcMessage

	nextID atomic.Int64

	// onNotification is called (synchronously from the read loop) for every
	// server-initiated notification or request. May be nil.
	onNotification func(rpcMessage)

	done chan struct{} // closed when the read loop exits
}

func newConn(send func([]byte) error) *conn {
	return &conn{
		send:    send,
		pending: make(map[int64]chan rpcMessage),
		done:    make(chan struct{}),
	}
}

// call sends a JSON-RPC request and blocks until the matching response arrives
// or ctx is cancelled.
func (c *conn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	req := map[string]any{
		"jsonrpc": jsonrpcVersion,
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	ch := make(chan rpcMessage, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.send(data); err != nil {
		return nil, fmt.Errorf("mcp: send: %w", err)
	}

	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("mcp: connection closed")
	}
}

// notify sends a JSON-RPC notification (no ID, no response expected).
func (c *conn) notify(method string, params any) error {
	n := map[string]any{
		"jsonrpc": jsonrpcVersion,
		"method":  method,
	}
	if params != nil {
		n["params"] = params
	}
	data, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("mcp: marshal notification: %w", err)
	}
	return c.send(data)
}

// readLoop reads newline-delimited JSON messages from r until EOF or error.
// It should be run in a dedicated goroutine.
func (c *conn) readLoop(r io.Reader) {
	defer close(c.done)

	scanner := bufio.NewScanner(r)
	// MCP messages can be large (e.g. embedded file contents).
	scanner.Buffer(make([]byte, 1<<20), 4<<20)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue // skip malformed messages
		}

		if msg.ID != 0 && msg.Method == "" {
			// Response to a pending call.
			c.mu.Lock()
			ch, ok := c.pending[msg.ID]
			c.mu.Unlock()
			if ok {
				select {
				case ch <- msg:
				default:
				}
			}
		} else if msg.Method != "" {
			// Notification or server-initiated request — dispatch to handler.
			if c.onNotification != nil {
				c.onNotification(msg)
			}
		}
	}
}
