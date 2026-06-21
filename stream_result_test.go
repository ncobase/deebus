package deebus

import (
	"context"
	"errors"
	"testing"
)

func TestStreamAccumulatorCollectsContentUsageAndTools(t *testing.T) {
	acc := NewStreamAccumulator()
	_ = acc.Add(&StreamChunk{Content: "hel"})
	_ = acc.Add(&StreamChunk{Reasoning: "think"})
	_ = acc.Add(&StreamChunk{Content: "lo"})
	_ = acc.Add(&StreamChunk{
		Done:         true,
		FinishReason: "stop",
		InputTokens:  7,
		OutputTokens: 3,
		CacheUsage:   CacheUsage{ReadTokens: 4},
		ToolCalls: []ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "lookup", Arguments: "{}"},
		}},
	})

	result := acc.Result()
	if result.Content != "hello" || result.Reasoning != "think" {
		t.Fatalf("result content=%q reasoning=%q", result.Content, result.Reasoning)
	}
	if result.FinishReason != "stop" || result.TokensUsed != 10 || result.CacheUsage.ReadTokens != 4 {
		t.Fatalf("unexpected final result: %#v", result)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("tool calls not collected: %#v", result.ToolCalls)
	}
	if result.ChunkCount != 4 || result.EndedAt.IsZero() {
		t.Fatalf("chunk/duration not recorded: %#v", result)
	}
}

func TestCollectStreamReturnsPartialResultOnChunkError(t *testing.T) {
	stream := make(chan *StreamChunk, 2)
	chunkErr := errors.New("provider stream failed")
	stream <- &StreamChunk{Content: "partial"}
	stream <- &StreamChunk{Error: chunkErr}
	close(stream)

	result, err := CollectStream(context.Background(), stream)
	if !errors.Is(err, chunkErr) {
		t.Fatalf("CollectStream error = %v, want %v", err, chunkErr)
	}
	if result == nil || result.Content != "partial" || !errors.Is(result.Err, chunkErr) {
		t.Fatalf("partial result not preserved: %#v", result)
	}
}
