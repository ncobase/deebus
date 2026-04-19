package deebus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ncobase/deebus/providers"
)

// AgentToolFunc executes a tool call within the agent loop.
// name is the function name and args is the JSON-encoded argument string.
// Returning a non-nil error stops the loop and returns that error to the caller.
type AgentToolFunc func(ctx context.Context, name, args string) (string, error)

// AgentEventType describes what happened in an agent loop event.
type AgentEventType string

const (
	// EventLLMRequest fires just before a model call is made.
	EventLLMRequest AgentEventType = "llm_request"
	// EventLLMResponse fires after the model responds.
	EventLLMResponse AgentEventType = "llm_response"
	// EventToolCall fires just before a tool is executed.
	EventToolCall AgentEventType = "tool_call"
	// EventToolResult fires after a tool returns.
	EventToolResult AgentEventType = "tool_result"
	// EventDone fires when the agent loop finishes successfully.
	EventDone AgentEventType = "done"
	// EventError fires when the agent loop terminates with an error.
	EventError AgentEventType = "error"
)

// AgentEvent describes one observable action in the agent loop.
type AgentEvent struct {
	// Type identifies what happened.
	Type AgentEventType
	// Iteration is the current loop count (1-based).
	Iteration int
	// ToolName is populated for EventToolCall and EventToolResult events.
	ToolName string
	// Input holds JSON arguments for EventToolCall, or empty for other types.
	Input string
	// Output holds the tool result for EventToolResult, or the model's text
	// content for EventLLMResponse.
	Output string
	// TokensUsed is populated on EventLLMResponse.
	TokensUsed int
	// Duration is set for EventToolResult and EventLLMResponse.
	Duration time.Duration
	// Err is set for EventError.
	Err error
}

// AgentConfig controls agent loop behaviour.
type AgentConfig struct {
	// MaxIterations caps the number of model->tool round-trips. Default: 10.
	MaxIterations int

	// DisableParallel forces sequential tool execution even when the model
	// returns multiple tool calls in one turn. Default: false (parallel on).
	DisableParallel bool

	// Hook is called after each significant agent action for observability.
	// It is called synchronously and must not block.
	Hook func(AgentEvent)

	// MaxHistoryMessages trims the conversation when it exceeds this many
	// messages, preserving system messages and the most recent turns.
	// 0 disables trimming.
	MaxHistoryMessages int
}

// RunAgent runs a synchronous agentic loop: call the model, execute any tool
// calls (in parallel unless DisableParallel is true), feed results back, and repeat
// until the model produces a final text response or MaxIterations is reached.
//
// Returns the model's final text content and the full conversation history.
func (c *Client) RunAgent(
	ctx context.Context,
	req *Request,
	toolFn AgentToolFunc,
	opts ...AgentConfig,
) (string, []providers.Message, error) {
	cfg := applyAgentDefaults(opts)
	msgs := make([]providers.Message, len(req.Messages))
	copy(msgs, req.Messages)

	emit := func(ev AgentEvent) {
		if cfg.Hook != nil {
			cfg.Hook(ev)
		}
	}

	for i := 0; i < cfg.MaxIterations; i++ {
		msgs = trimHistory(msgs, cfg.MaxHistoryMessages)

		r := *req
		r.Messages = msgs

		emit(AgentEvent{Type: EventLLMRequest, Iteration: i + 1})
		t0 := time.Now()

		resp, err := c.Complete(ctx, &r)
		if err != nil {
			emit(AgentEvent{Type: EventError, Iteration: i + 1, Err: err})
			return "", msgs, err
		}

		emit(AgentEvent{
			Type:       EventLLMResponse,
			Iteration:  i + 1,
			Output:     resp.Content,
			TokensUsed: resp.TokensUsed,
			Duration:   time.Since(t0),
		})

		// No tool calls -> model is done.
		if len(resp.ToolCalls) == 0 {
			emit(AgentEvent{Type: EventDone, Iteration: i + 1, Output: resp.Content})
			return resp.Content, msgs, nil
		}

		msgs = append(msgs, providers.AssistantMessage(resp.Content, resp.ToolCalls))

		results, err := dispatchTools(ctx, resp.ToolCalls, toolFn, cfg, i+1, emit)
		if err != nil {
			emit(AgentEvent{Type: EventError, Iteration: i + 1, Err: err})
			return "", msgs, err
		}

		// Append tool results in original order.
		for _, r := range results {
			msgs = append(msgs, providers.ToolResultMessage(r.callID, r.name, r.output))
		}
	}

	err := fmt.Errorf("agent: reached max iterations (%d)", cfg.MaxIterations)
	emit(AgentEvent{Type: EventError, Err: err})
	return "", msgs, err
}

// RunAgentStream is the streaming variant of RunAgent. Each model response is
// streamed to the caller via the returned channel. Tool calls are executed
// between turns (in parallel unless DisableParallel is true). The channel is closed when
// the agent finishes or an error occurs (delivered as a Done chunk with Error).
//
// When the loop ends, the full conversation history is sent to histCh if
// non-nil; pass nil to discard it.
func (c *Client) RunAgentStream(
	ctx context.Context,
	req *Request,
	toolFn AgentToolFunc,
	histCh chan<- []providers.Message,
	opts ...AgentConfig,
) (<-chan *providers.StreamChunk, error) {
	cfg := applyAgentDefaults(opts)
	msgs := make([]providers.Message, len(req.Messages))
	copy(msgs, req.Messages)

	out := make(chan *providers.StreamChunk, 16)

	go func() {
		defer close(out)
		if histCh != nil {
			defer func() {
				select {
				case histCh <- msgs:
				default:
				}
			}()
		}

		emit := func(ev AgentEvent) {
			if cfg.Hook != nil {
				cfg.Hook(ev)
			}
		}

		send := func(chunk *providers.StreamChunk) bool {
			select {
			case out <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}

		for i := 0; i < cfg.MaxIterations; i++ {
			msgs = trimHistory(msgs, cfg.MaxHistoryMessages)

			r := *req
			r.Messages = msgs

			emit(AgentEvent{Type: EventLLMRequest, Iteration: i + 1})
			t0 := time.Now()

			ch, err := c.Stream(ctx, &r)
			if err != nil {
				emit(AgentEvent{Type: EventError, Iteration: i + 1, Err: err})
				send(&providers.StreamChunk{Error: err, Done: true})
				return
			}

			var contentBuf strings.Builder
			var toolCalls []providers.ToolCall
			var tokensUsed int

			for chunk := range ch {
				if chunk.Error != nil {
					emit(AgentEvent{Type: EventError, Iteration: i + 1, Err: chunk.Error})
					send(chunk)
					return
				}
				if chunk.Content != "" {
					if !send(chunk) {
						return
					}
					contentBuf.WriteString(chunk.Content)
				}
				if chunk.Done {
					toolCalls = chunk.ToolCalls
					tokensUsed = chunk.TokensUsed
					if !send(chunk) {
						return
					}
				}
			}

			emit(AgentEvent{
				Type:       EventLLMResponse,
				Iteration:  i + 1,
				Output:     contentBuf.String(),
				TokensUsed: tokensUsed,
				Duration:   time.Since(t0),
			})

			if len(toolCalls) == 0 {
				emit(AgentEvent{Type: EventDone, Iteration: i + 1, Output: contentBuf.String()})
				return
			}

			msgs = append(msgs, providers.AssistantMessage(contentBuf.String(), toolCalls))

			results, err := dispatchTools(ctx, toolCalls, toolFn, cfg, i+1, emit)
			if err != nil {
				emit(AgentEvent{Type: EventError, Iteration: i + 1, Err: err})
				send(&providers.StreamChunk{Error: err, Done: true})
				return
			}

			for _, r := range results {
				msgs = append(msgs, providers.ToolResultMessage(r.callID, r.name, r.output))
			}
		}

		err := fmt.Errorf("agent: reached max iterations (%d)", cfg.MaxIterations)
		emit(AgentEvent{Type: EventError, Err: err})
		send(&providers.StreamChunk{Error: err, Done: true})
	}()

	return out, nil
}

type toolExecResult struct {
	idx    int
	callID string
	name   string
	output string
}

// dispatchTools executes tool calls sequentially or in parallel according to
// cfg.DisableParallel. Results are returned in the same order as calls.
func dispatchTools(
	ctx context.Context,
	calls []providers.ToolCall,
	fn AgentToolFunc,
	cfg AgentConfig,
	iteration int,
	emit func(AgentEvent),
) ([]toolExecResult, error) {
	results := make([]toolExecResult, len(calls))

	exec := func(i int, tc providers.ToolCall) error {
		emit(AgentEvent{
			Type:      EventToolCall,
			Iteration: iteration,
			ToolName:  tc.Function.Name,
			Input:     tc.Function.Arguments,
		})
		t0 := time.Now()

		out, err := fn(ctx, tc.Function.Name, tc.Function.Arguments)
		if err != nil {
			return fmt.Errorf("tool %q: %w", tc.Function.Name, err)
		}

		emit(AgentEvent{
			Type:      EventToolResult,
			Iteration: iteration,
			ToolName:  tc.Function.Name,
			Output:    out,
			Duration:  time.Since(t0),
		})

		results[i] = toolExecResult{idx: i, callID: tc.ID, name: tc.Function.Name, output: out}
		return nil
	}

	if cfg.DisableParallel || len(calls) == 1 {
		for i, tc := range calls {
			if err := exec(i, tc); err != nil {
				return nil, err
			}
		}
		return results, nil
	}

	// Parallel execution - collect first error.
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for i, tc := range calls {
		i, tc := i, tc
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := exec(i, tc); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// trimHistory keeps at most max messages, always preserving system messages
// at the front and retaining the most recent non-system messages.
// Returns msgs unchanged when max is 0 or len(msgs) <= max.
func trimHistory(msgs []providers.Message, max int) []providers.Message {
	if max <= 0 || len(msgs) <= max {
		return msgs
	}

	var system, rest []providers.Message
	for _, m := range msgs {
		if m.Role == "system" {
			system = append(system, m)
		} else {
			rest = append(rest, m)
		}
	}

	keep := max - len(system)
	if keep <= 0 {
		return system
	}
	if len(rest) > keep {
		rest = rest[len(rest)-keep:]
	}
	return append(system, rest...)
}

// applyAgentDefaults fills zero-value AgentConfig fields with sensible defaults.
func applyAgentDefaults(opts []AgentConfig) AgentConfig {
	cfg := AgentConfig{MaxIterations: 10}
	if len(opts) == 0 {
		return cfg
	}
	o := opts[0]
	if o.MaxIterations > 0 {
		cfg.MaxIterations = o.MaxIterations
	}
	cfg.DisableParallel = o.DisableParallel
	cfg.Hook = o.Hook
	cfg.MaxHistoryMessages = o.MaxHistoryMessages
	return cfg
}
