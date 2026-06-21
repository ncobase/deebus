package deebus

import (
	"context"
	"fmt"
	"time"
)

// StreamResult is the accumulated result of a streaming request.
type StreamResult struct {
	Content   string
	Reasoning string
	ToolCalls []ToolCall

	FinishReason    string
	InputTokens     int
	OutputTokens    int
	TokensUsed      int
	ReasoningTokens int
	CacheUsage      CacheUsage

	ChunkCount int
	StartedAt  time.Time
	EndedAt    time.Time
	Duration   time.Duration
	Err        error
}

// StreamAccumulator incrementally folds StreamChunk values into StreamResult.
type StreamAccumulator struct {
	result StreamResult
}

// NewStreamAccumulator creates an empty stream accumulator.
func NewStreamAccumulator() *StreamAccumulator {
	now := time.Now().UTC()
	return &StreamAccumulator{
		result: StreamResult{StartedAt: now},
	}
}

// Add records one stream chunk. It returns chunk.Error when present so callers
// can stop early while still preserving the partial result.
func (a *StreamAccumulator) Add(chunk *StreamChunk) error {
	if a == nil || chunk == nil {
		return nil
	}
	if a.result.StartedAt.IsZero() {
		a.result.StartedAt = time.Now().UTC()
	}
	a.result.ChunkCount++
	a.result.Content += chunk.Content
	a.result.Reasoning += chunk.Reasoning

	if len(chunk.ToolCalls) > 0 {
		a.result.ToolCalls = cloneToolCalls(chunk.ToolCalls)
	}
	if chunk.FinishReason != "" {
		a.result.FinishReason = chunk.FinishReason
	}
	if chunk.InputTokens > 0 || chunk.OutputTokens > 0 || chunk.TokensUsed > 0 || chunk.Done {
		a.result.InputTokens = chunk.InputTokens
		a.result.OutputTokens = chunk.OutputTokens
		a.result.TokensUsed = chunk.TokensUsed
		if a.result.TokensUsed == 0 && (a.result.InputTokens > 0 || a.result.OutputTokens > 0) {
			a.result.TokensUsed = a.result.InputTokens + a.result.OutputTokens
		}
		a.result.ReasoningTokens = chunk.ReasoningTokens
		a.result.CacheUsage = chunk.CacheUsage
	}
	if chunk.Error != nil {
		a.result.Err = chunk.Error
		return chunk.Error
	}
	if chunk.Done {
		a.finish()
	}
	return nil
}

// Result returns the current accumulated stream result.
func (a *StreamAccumulator) Result() StreamResult {
	if a == nil {
		return StreamResult{}
	}
	result := a.result
	if result.EndedAt.IsZero() && !result.StartedAt.IsZero() {
		result.EndedAt = time.Now().UTC()
		result.Duration = result.EndedAt.Sub(result.StartedAt)
	}
	result.ToolCalls = cloneToolCalls(result.ToolCalls)
	return result
}

func (a *StreamAccumulator) finish() {
	if a.result.EndedAt.IsZero() {
		a.result.EndedAt = time.Now().UTC()
		a.result.Duration = a.result.EndedAt.Sub(a.result.StartedAt)
	}
}

// CollectStream consumes a stream to completion and returns the accumulated
// result. It respects ctx cancellation and returns any chunk error with partial
// content and final usage collected up to that point.
func CollectStream(ctx context.Context, stream <-chan *StreamChunk) (*StreamResult, error) {
	acc := NewStreamAccumulator()
	if stream == nil {
		result := acc.Result()
		result.Err = fmt.Errorf("collect stream: stream is nil")
		return &result, result.Err
	}
	for {
		select {
		case chunk, ok := <-stream:
			if !ok {
				result := acc.Result()
				if result.Err != nil {
					return &result, result.Err
				}
				return &result, nil
			}
			if err := acc.Add(chunk); err != nil {
				result := acc.Result()
				return &result, err
			}
		case <-ctx.Done():
			result := acc.Result()
			result.Err = ctx.Err()
			return &result, fmt.Errorf("collect stream: %w", ctx.Err())
		}
	}
}
