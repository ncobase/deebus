package mcp

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestStdioCloseStopsProcess(t *testing.T) {
	transport, err := newStdioTransport("cat", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- transport.close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close blocked; stdin was not closed")
	}
}

func TestStdioSurvivesCallerCancel(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	script := `
import json, sys
for line in sys.stdin:
    msg = json.loads(line)
    if msg.get("method") == "initialize":
        sys.stdout.write(json.dumps({
            "jsonrpc": "2.0",
            "id": msg["id"],
            "result": {
                "protocolVersion": "2025-11-25",
                "capabilities": {},
                "serverInfo": {"name": "t", "version": "0"},
            },
        }) + "\n")
        sys.stdout.flush()
`
	ctx, cancel := context.WithCancel(context.Background())
	client, err := NewStdioClient(ctx, "python3", []string{"-c", script}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
	if err := client.Close(); err != nil {
		t.Fatalf("process exited because the caller context was cancelled: %v", err)
	}
}
