package deebus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ncobase/deebus/providers"
)

// agentMockProvider is a Provider that returns scripted responses for agent tests.
type agentMockProvider struct {
	responses []providers.Response // returned in order; last is repeated
	calls     int
}

func (p *agentMockProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	idx := p.calls
	if idx >= len(p.responses) {
		idx = len(p.responses) - 1
	}
	p.calls++
	r := p.responses[idx]
	return &r, nil
}

func (p *agentMockProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	idx := p.calls
	if idx >= len(p.responses) {
		idx = len(p.responses) - 1
	}
	p.calls++
	resp := p.responses[idx]

	ch := make(chan *providers.StreamChunk, 4)
	go func() {
		defer close(ch)
		if resp.Content != "" {
			ch <- &providers.StreamChunk{Content: resp.Content}
		}
		ch <- &providers.StreamChunk{
			Done:         true,
			ToolCalls:    resp.ToolCalls,
			TokensUsed:   resp.TokensUsed,
			FinishReason: resp.FinishReason,
		}
	}()
	return ch, nil
}

func (p *agentMockProvider) Embed(_ context.Context, _ *providers.EmbedRequest) (*providers.EmbedResponse, error) {
	return nil, errors.New("not implemented")
}

func (p *agentMockProvider) Name() string                   { return "mock" }
func (p *agentMockProvider) Health(_ context.Context) error { return nil }
func (p *agentMockProvider) ListModels(_ context.Context) ([]string, error) {
	return []string{"mock-model"}, nil
}

// toolCall constructs a ToolCall.
func toolCall(id, name, args string) providers.ToolCall {
	tc := providers.ToolCall{ID: id, Type: "function"}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

// buildTestClient creates a deebus Client backed by the given mock provider.
func buildTestClient(t *testing.T, mock *agentMockProvider) *Client {
	t.Helper()
	// Use "ollama" type so config validation does not require an API key.
	cfg := Config{
		Primary: "mock/model",
		Providers: map[string]ProviderConfig{
			"mock": {Type: "ollama", BaseURL: "http://localhost:11434"},
		},
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Swap the real ollama provider for our mock.
	c.providers["mock"] = mock
	return c
}

func TestTrimHistoryNoOp(t *testing.T) {
	msgs := []providers.Message{
		{Role: "user"},
		{Role: "assistant"},
	}
	got := trimHistory(msgs, 0) // disabled
	if len(got) != 2 {
		t.Errorf("got %d messages, want 2", len(got))
	}
	got = trimHistory(msgs, 10) // limit higher than length
	if len(got) != 2 {
		t.Errorf("got %d messages, want 2", len(got))
	}
}

func TestTrimHistoryPreservesSystem(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system"},
		{Role: "user"},
		{Role: "assistant"},
		{Role: "user"},
		{Role: "assistant"},
	}
	got := trimHistory(msgs, 3) // keep system + 2 most recent
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}
	if got[0].Role != "system" {
		t.Errorf("first message should be system, got %q", got[0].Role)
	}
}

func TestTrimHistoryNoSystem(t *testing.T) {
	var msgs []providers.Message
	for i := 0; i < 6; i++ {
		msgs = append(msgs, providers.Message{Role: "user"})
	}
	got := trimHistory(msgs, 4)
	if len(got) != 4 {
		t.Errorf("got %d messages, want 4", len(got))
	}
}

func TestDispatchToolsSequential(t *testing.T) {
	calls := []providers.ToolCall{
		toolCall("1", "add", `{"a":1,"b":2}`),
		toolCall("2", "mul", `{"a":3,"b":4}`),
	}
	cfg := AgentConfig{DisableParallel: true}

	results, err := dispatchTools(context.Background(), calls, func(_ context.Context, name, _ string) (string, error) {
		return "result-" + name, nil
	}, cfg, 1, func(AgentEvent) {})

	if err != nil {
		t.Fatalf("dispatchTools: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].output != "result-add" {
		t.Errorf("results[0].output = %q", results[0].output)
	}
	if results[1].output != "result-mul" {
		t.Errorf("results[1].output = %q", results[1].output)
	}
}

func TestDispatchToolsParallel(t *testing.T) {
	calls := []providers.ToolCall{
		toolCall("1", "a", `{}`),
		toolCall("2", "b", `{}`),
		toolCall("3", "c", `{}`),
	}
	cfg := AgentConfig{DisableParallel: false}

	results, err := dispatchTools(context.Background(), calls, func(_ context.Context, name, _ string) (string, error) {
		return name + "-done", nil
	}, cfg, 1, func(AgentEvent) {})

	if err != nil {
		t.Fatal(err)
	}
	// Results must be in original order.
	for i, tc := range calls {
		want := tc.Function.Name + "-done"
		if results[i].output != want {
			t.Errorf("results[%d].output = %q, want %q", i, results[i].output, want)
		}
	}
}

func TestDispatchToolsError(t *testing.T) {
	calls := []providers.ToolCall{toolCall("1", "fail", `{}`)}
	cfg := AgentConfig{DisableParallel: true}

	_, err := dispatchTools(context.Background(), calls, func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("boom")
	}, cfg, 1, func(AgentEvent) {})

	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected boom error, got %v", err)
	}
}

func TestRunAgentNoTools(t *testing.T) {
	mock := &agentMockProvider{
		responses: []providers.Response{
			{Content: "Hello, world!", TokensUsed: 10},
		},
	}
	c := buildTestClient(t, mock)
	req := &Request{Messages: []Message{TextMessage("user", "hi")}}

	answer, history, err := c.RunAgent(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if answer != "Hello, world!" {
		t.Errorf("answer = %q", answer)
	}
	if len(history) != 1 {
		t.Errorf("history len = %d, want 1", len(history))
	}
}

func TestRunAgentOneTurn(t *testing.T) {
	// Turn 1: model calls "get_time"; Turn 2: model gives final answer.
	mock := &agentMockProvider{
		responses: []providers.Response{
			{
				Content:   "",
				ToolCalls: []providers.ToolCall{toolCall("tc1", "get_time", `{}`)},
			},
			{Content: "The time is noon."},
		},
	}
	c := buildTestClient(t, mock)
	req := &Request{Messages: []Message{TextMessage("user", "what time?")}}

	toolFn := func(_ context.Context, name, _ string) (string, error) {
		if name != "get_time" {
			return "", fmt.Errorf("unknown tool %q", name)
		}
		return "12:00", nil
	}

	answer, history, err := c.RunAgent(context.Background(), req, toolFn)
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if answer != "The time is noon." {
		t.Errorf("answer = %q", answer)
	}
	// history: user | assistant(tool_call) | tool_result
	if len(history) != 3 {
		t.Errorf("history len = %d, want 3", len(history))
	}
	if mock.calls != 2 {
		t.Errorf("LLM called %d times, want 2", mock.calls)
	}
}

func TestRunAgentMaxIterations(t *testing.T) {
	// Model always returns a tool call -> should exhaust MaxIterations.
	mock := &agentMockProvider{
		responses: []providers.Response{
			{ToolCalls: []providers.ToolCall{toolCall("tc1", "loop", `{}`)}},
		},
	}
	c := buildTestClient(t, mock)
	req := &Request{Messages: []Message{TextMessage("user", "loop")}}

	_, _, err := c.RunAgent(context.Background(), req, func(_ context.Context, _, _ string) (string, error) {
		return "ok", nil
	}, AgentConfig{MaxIterations: 3})

	if err == nil || !strings.Contains(err.Error(), "max iterations") {
		t.Errorf("expected max iterations error, got %v", err)
	}
	if mock.calls != 3 {
		t.Errorf("LLM called %d times, want 3", mock.calls)
	}
}

func TestRunAgentToolError(t *testing.T) {
	mock := &agentMockProvider{
		responses: []providers.Response{
			{ToolCalls: []providers.ToolCall{toolCall("tc1", "bad", `{}`)}},
		},
	}
	c := buildTestClient(t, mock)
	req := &Request{Messages: []Message{TextMessage("user", "hi")}}

	_, _, err := c.RunAgent(context.Background(), req, func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("tool exploded")
	})

	if err == nil || !strings.Contains(err.Error(), "tool exploded") {
		t.Errorf("expected tool error, got %v", err)
	}
}

func TestRunAgentHook(t *testing.T) {
	mock := &agentMockProvider{
		responses: []providers.Response{
			{ToolCalls: []providers.ToolCall{toolCall("1", "ping", `{}`)}},
			{Content: "pong"},
		},
	}
	c := buildTestClient(t, mock)
	req := &Request{Messages: []Message{TextMessage("user", "ping")}}

	var events []AgentEventType
	hook := func(ev AgentEvent) { events = append(events, ev.Type) }

	c.RunAgent(context.Background(), req, func(_ context.Context, _, _ string) (string, error) {
		return "pong-result", nil
	}, AgentConfig{Hook: hook})

	want := []AgentEventType{
		EventLLMRequest, EventLLMResponse, EventToolCall, EventToolResult,
		EventLLMRequest, EventLLMResponse, EventDone,
	}
	if len(events) != len(want) {
		t.Fatalf("got %d events: %v, want %d: %v", len(events), events, len(want), want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("event[%d] = %q, want %q", i, events[i], want[i])
		}
	}
}

func TestRunAgentStreamNoTools(t *testing.T) {
	mock := &agentMockProvider{
		responses: []providers.Response{
			{Content: "streaming answer", TokensUsed: 5},
		},
	}
	c := buildTestClient(t, mock)
	req := &Request{Messages: []Message{TextMessage("user", "hi")}}

	ch, err := c.RunAgentStream(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var content strings.Builder
	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("stream error: %v", chunk.Error)
		}
		content.WriteString(chunk.Content)
	}
	if content.String() != "streaming answer" {
		t.Errorf("content = %q", content.String())
	}
}

func TestRunAgentStreamHistory(t *testing.T) {
	mock := &agentMockProvider{
		responses: []providers.Response{
			{ToolCalls: []providers.ToolCall{toolCall("1", "calc", `{}`)}},
			{Content: "42"},
		},
	}
	c := buildTestClient(t, mock)
	req := &Request{Messages: []Message{TextMessage("user", "calc")}}

	histCh := make(chan []providers.Message, 1)
	ch, err := c.RunAgentStream(context.Background(), req, func(_ context.Context, _, _ string) (string, error) {
		return "result", nil
	}, histCh)
	if err != nil {
		t.Fatal(err)
	}

	for range ch {
	} // drain

	history := <-histCh
	// user | assistant(tool) | tool_result
	if len(history) != 3 {
		t.Errorf("history len = %d, want 3", len(history))
	}
}

func TestRunAgentStreamContextCancel(t *testing.T) {
	mock := &agentMockProvider{
		responses: []providers.Response{
			{ToolCalls: []providers.ToolCall{toolCall("1", "slow", `{}`)}},
		},
	}
	c := buildTestClient(t, mock)
	req := &Request{Messages: []Message{TextMessage("user", "hi")}}

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := c.RunAgentStream(ctx, req, func(ctx context.Context, _, _ string) (string, error) {
		cancel() // cancel mid-tool
		return "too late", nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Drain; we just need it to terminate without deadlock.
	for range ch {
	}
}
