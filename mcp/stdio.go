package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// stdioTransport launches a subprocess and communicates with it via its
// stdin (client->server) and stdout (server->client) using newline-delimited
// JSON-RPC 2.0 messages, as specified by the MCP stdio transport.
//
// The subprocess's stderr is forwarded to os.Stderr so log output is visible.
type stdioTransport struct {
	cmd       *exec.Cmd
	conn      *conn
	closeOnce sync.Once
	closeErr  error
}

// newStdioTransport starts command with args and env overlaid on the current
// environment, then returns a connected transport ready to use.
func newStdioTransport(ctx context.Context, command string, args, env []string) (*stdioTransport, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp stdio: start %q: %w", command, err)
	}

	t := &stdioTransport{cmd: cmd}
	t.conn = newConn(func(data []byte) error {
		_, err := fmt.Fprintf(stdin, "%s\n", data)
		return err
	})

	go t.conn.readLoop(stdout)

	return t, nil
}

func (t *stdioTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return t.conn.call(ctx, method, params)
}

func (t *stdioTransport) notify(method string, params any) error {
	return t.conn.notify(method, params)
}

func (t *stdioTransport) setNotificationHandler(h func(rpcMessage)) {
	t.conn.onNotification = h
}

func (t *stdioTransport) close() error {
	t.closeOnce.Do(func() {
		// Close stdin to signal EOF to the subprocess, then wait for it to exit.
		if pipe, ok := t.cmd.Stdin.(interface{ Close() error }); ok {
			pipe.Close()
		}
		t.closeErr = t.cmd.Wait()
	})
	return t.closeErr
}
