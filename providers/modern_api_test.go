package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIToolChoiceSerializesSpecificFunctionByAPIMode(t *testing.T) {
	chatChoice, ok := openAIToolChoice("lookup_account").(map[string]any)
	if !ok || chatChoice["type"] != "function" {
		t.Fatalf("chat tool_choice = %#v", chatChoice)
	}
	chatFunction, ok := chatChoice["function"].(map[string]any)
	if !ok || chatFunction["name"] != "lookup_account" {
		t.Fatalf("chat function tool_choice = %#v", chatChoice)
	}

	responsesChoice, ok := openAIResponsesToolChoice("lookup_account").(map[string]any)
	if !ok || responsesChoice["type"] != "function" || responsesChoice["name"] != "lookup_account" {
		t.Fatalf("Responses tool_choice = %#v", responsesChoice)
	}

	if openAIToolChoice("auto") != "auto" || openAIResponsesToolChoice("required") != "required" {
		t.Fatalf("built-in tool_choice values must remain strings")
	}
}

func TestOpenAIResponsesCompleteMapsModernFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %s, want /v1/responses", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["max_output_tokens"]; got != float64(128) {
			t.Fatalf("max_output_tokens = %#v", got)
		}
		if _, ok := body["reasoning"].(map[string]any); !ok {
			t.Fatalf("reasoning missing: %#v", body["reasoning"])
		}
		text, ok := body["text"].(map[string]any)
		if !ok {
			t.Fatalf("text config missing: %#v", body["text"])
		}
		format, ok := text["format"].(map[string]any)
		if !ok || format["type"] != "json_schema" {
			t.Fatalf("format = %#v", text["format"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"model":  "gpt-5.4",
			"output": []map[string]any{
				{"type": "reasoning", "summary": []map[string]any{{"text": "checked"}}},
				{"type": "message", "content": []map[string]any{{"type": "output_text", "text": "{\"ok\":true}"}}},
			},
			"usage": map[string]any{
				"input_tokens":          10,
				"output_tokens":         5,
				"total_tokens":          15,
				"output_tokens_details": map[string]any{"reasoning_tokens": 2},
			},
		})
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "sk-test", BaseURL: srv.URL, APIMode: "responses", Timeout: time.Second})
	resp, err := p.Complete(context.Background(), &Request{
		Model:           "gpt-5.4",
		Messages:        []Message{TextMessage("user", "return json")},
		MaxOutputTokens: 128,
		Reasoning:       &ReasoningConfig{Effort: "medium"},
		ResponseFormat: &ResponseFormat{
			Type:   "json_schema",
			Name:   "result",
			Schema: map[string]any{"type": "object"},
			Strict: true,
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != `{"ok":true}` || resp.Reasoning != "checked" || resp.ReasoningTokens != 2 {
		t.Fatalf("response = %+v", resp)
	}
}

func TestGeminiCompleteMapsStructuredThinkingConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gc, ok := body["generationConfig"].(map[string]any)
		if !ok {
			t.Fatalf("generationConfig missing: %#v", body)
		}
		if gc["responseMimeType"] != "application/json" {
			t.Fatalf("responseMimeType = %#v", gc["responseMimeType"])
		}
		if _, ok := gc["responseSchema"].(map[string]any); !ok {
			t.Fatalf("responseSchema missing: %#v", gc)
		}
		thinking, ok := gc["thinkingConfig"].(map[string]any)
		if !ok || thinking["includeThoughts"] != true {
			t.Fatalf("thinkingConfig = %#v", gc["thinkingConfig"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates":    []map[string]any{{"content": map[string]any{"parts": []map[string]any{{"text": "ok"}}}, "finishReason": "STOP"}},
			"usageMetadata": map[string]any{"promptTokenCount": 1, "candidatesTokenCount": 1},
		})
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "key", BaseURL: srv.URL, Timeout: time.Second})
	_, err := p.Complete(context.Background(), &Request{
		Model:          "gemini-3-flash-preview",
		Messages:       []Message{TextMessage("user", "hello")},
		ResponseFormat: &ResponseFormat{Type: "json_schema", Schema: map[string]any{"type": "object"}},
		Reasoning:      &ReasoningConfig{IncludeThoughts: true, BudgetTokens: 64},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestOllamaCompleteMapsStructuredFormatAndOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if _, ok := body["format"].(map[string]any); !ok {
			t.Fatalf("format = %#v", body["format"])
		}
		options, ok := body["options"].(map[string]any)
		if !ok || options["num_predict"] != float64(32) || options["temperature"] != 0.2 {
			t.Fatalf("options = %#v", body["options"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "{}"}, "model": "llama", "done_reason": "stop"})
	}))
	defer srv.Close()

	p := NewOllama(Config{BaseURL: srv.URL, Timeout: time.Second})
	_, err := p.Complete(context.Background(), &Request{
		Model:           "llama",
		Messages:        []Message{TextMessage("user", "json")},
		MaxOutputTokens: 32,
		Temperature:     0.2,
		ResponseFormat:  &ResponseFormat{Type: "json_schema", Schema: map[string]any{"type": "object"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}


func TestOpenAIChatCompletionsReasoningEffortAndStreamSeparation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["reasoning_effort"] != "high" {
			t.Fatalf("expected top-level reasoning_effort=high, got %#v", body["reasoning_effort"])
		}
		if _, hasNested := body["reasoning"]; hasNested {
			t.Fatalf("chat completions should not send nested responses reasoning object: %#v", body["reasoning"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think step\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"final answer\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":6,\"total_tokens\":10}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := NewOpenAICompatible("grok", Config{APIKey: "xai-test", BaseURL: srv.URL, Timeout: time.Second})
	ch, err := p.Stream(context.Background(), &Request{
		Model:     "grok-3",
		Messages:  []Message{TextMessage("user", "solve")},
		Reasoning: &ReasoningConfig{Effort: "high"},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var gotContent, gotReasoning string
	var done *StreamChunk
	for chunk := range ch {
		gotContent += chunk.Content
		gotReasoning += chunk.Reasoning
		if chunk.Done {
			done = chunk
		}
	}
	if gotReasoning != "think step" || gotContent != "final answer" || done == nil || done.TokensUsed != 10 {
		t.Fatalf("content=%q reasoning=%q done=%+v", gotContent, gotReasoning, done)
	}
}

func TestOpenAIResponsesEncryptedReasoningAndSummaryRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		reasoningCfg, ok := body["reasoning"].(map[string]any)
		if !ok || reasoningCfg["effort"] != "xhigh" || reasoningCfg["summary"] != "detailed" {
			t.Fatalf("unexpected reasoning config: %#v", body["reasoning"])
		}
		include, ok := body["include"].([]any)
		if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			t.Fatalf("unexpected include: %#v", body["include"])
		}
		input, _ := body["input"].([]any)
		foundReasoningItem := false
		for _, item := range input {
			if m, ok := item.(map[string]any); ok && m["type"] == "reasoning" && m["encrypted_content"] == "enc_prev_123" {
				foundReasoningItem = true
			}
		}
		if !foundReasoningItem {
			t.Fatalf("expected encrypted reasoning item in input: %#v", body["input"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"model":  "gpt-5.4",
			"output": []map[string]any{
				{"type": "reasoning", "encrypted_content": "enc_next_456", "summary": []map[string]any{{"text": "summarized"}}},
				{"type": "message", "content": []map[string]any{{"type": "output_text", "text": "done"}}},
			},
			"usage": map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8},
		})
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "sk-test", BaseURL: srv.URL, APIMode: "responses", Timeout: time.Second})
	resp, err := p.Complete(context.Background(), &Request{
		Model: "gpt-5.4",
		Messages: []Message{
			{
				Role: "assistant",
				Content: []ContentBlock{
					ThinkingContent{Type: "redacted_thinking", Data: "enc_prev_123"},
					TextContent{Type: "output_text", Text: "prior"},
				},
			},
			TextMessage("user", "continue"),
		},
		Reasoning: &ReasoningConfig{Effort: "xhigh", Summary: "detailed"},
		Include:   []string{"reasoning.encrypted_content"},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.EncryptedReasoning != "enc_next_456" || resp.Reasoning != "summarized" || resp.Content != "done" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestAnthropicAdaptiveThinkingSignatureAndOrderedToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		thinkingCfg, ok := body["thinking"].(map[string]any)
		if !ok || thinkingCfg["type"] != "adaptive" {
			t.Fatalf("expected adaptive thinking config, got %#v", body["thinking"])
		}
		oc, ok := body["output_config"].(map[string]any)
		if !ok || oc["effort"] != "high" {
			t.Fatalf("expected output_config.effort=high, got %#v", body["output_config"])
		}
		msgs, _ := body["messages"].([]any)
		firstAsst := msgs[0].(map[string]any)["content"].([]any)
		if firstAsst[0].(map[string]any)["type"] != "thinking" || firstAsst[0].(map[string]any)["signature"] != "sig_0" {
			t.Fatalf("expected signed thinking block preserved first, got %#v", firstAsst)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"ponder\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig_live\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tu_1\",\"name\":\"first\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"a\\\":1}\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_stop\",\"index\":1}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tu_2\",\"name\":\"second\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"b\\\":2}\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_stop\",\"index\":2}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":9}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	p := NewAnthropic(Config{APIKey: "sk-ant-test", BaseURL: srv.URL, Timeout: time.Second})
	ch, err := p.Stream(context.Background(), &Request{
		Model: "claude-opus-4-6",
		Messages: []Message{
			{
				Role: "assistant",
				Content: []ContentBlock{
					ThinkingContent{Type: "thinking", Thinking: "prior thought", Signature: "sig_0"},
				},
			},
			TextMessage("user", "run both tools"),
		},
		Reasoning: &ReasoningConfig{Mode: "adaptive", Effort: "high"},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var gotThought, gotSig string
	var final *StreamChunk
	for chunk := range ch {
		gotThought += chunk.Reasoning
		if chunk.ReasoningSignature != "" && !chunk.Done {
			gotSig += chunk.ReasoningSignature
		}
		if chunk.Done {
			final = chunk
		}
	}
	if gotThought != "ponder" || gotSig != "sig_live" || final == nil || final.ReasoningSignature != "sig_live" {
		t.Fatalf("thought=%q sig=%q final=%+v", gotThought, gotSig, final)
	}
	if len(final.ToolCalls) != 2 || final.ToolCalls[0].Function.Name != "first" || final.ToolCalls[1].Function.Name != "second" {
		t.Fatalf("expected deterministic ordered toolCalls, got %+v", final.ToolCalls)
	}
}

func TestGeminiThoughtSeparationAndThoughtSignatureRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		gc := body["generationConfig"].(map[string]any)
		tc := gc["thinkingConfig"].(map[string]any)
		if tc["thinkingLevel"] != "HIGH" || tc["includeThoughts"] != true {
			t.Fatalf("expected thinkingLevel=HIGH, got %#v", tc)
		}
		contents := body["contents"].([]any)
		modelTurn := contents[0].(map[string]any)
		parts := modelTurn["parts"].([]any)
		if parts[0].(map[string]any)["thoughtSignature"] != "gem_sig_prev" {
			t.Fatalf("expected prior functionCall thoughtSignature preserved, got %#v", parts[0])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{
						{"text": "hidden deliberation", "thought": true, "thoughtSignature": "gem_sig_thought"},
						{"text": "visible answer"},
						{"functionCall": map[string]any{"name": "next_step", "args": map[string]any{"x": 1}}, "thoughtSignature": "gem_sig_call"},
					},
				},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 5, "candidatesTokenCount": 4, "thoughtsTokenCount": 3},
		})
	}))
	defer srv.Close()

	prevCall := ToolCall{ID: "call_1", Type: "function", ThoughtSignature: "gem_sig_prev"}
	prevCall.Function.Name = "lookup"
	prevCall.Function.Arguments = `{"q":"test"}`

	p := NewGemini(Config{APIKey: "gem-test", BaseURL: srv.URL, Timeout: time.Second})
	resp, err := p.Complete(context.Background(), &Request{
		Model: "gemini-3-flash-preview",
		Messages: []Message{
			AssistantMessage("", []ToolCall{prevCall}),
			ToolResultMessage("call_1", "lookup", `{"ok":true}`),
		},
		Reasoning: &ReasoningConfig{Effort: "high", IncludeThoughts: true},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "visible answer" || resp.Reasoning != "hidden deliberation" || resp.ReasoningSignature != "gem_sig_call" {
		t.Fatalf("unexpected gemini response: %+v", resp)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ThoughtSignature != "gem_sig_call" {
		t.Fatalf("expected tool thoughtSignature=gem_sig_call, got %+v", resp.ToolCalls)
	}
}


func TestGeminiStreamSeparatesThoughtsFromTextAndKeepsToolCallSignature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"step 1\",\"thought\":true}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"lookup\",\"args\":{\"id\":7}},\"thoughtSignature\":\"sig_stream_tc\"}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"done\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":6,\"candidatesTokenCount\":3,\"thoughtsTokenCount\":2}}\n\n"))
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "gem-test", BaseURL: srv.URL, Timeout: time.Second})
	ch, err := p.Stream(context.Background(), &Request{
		Model:    "gemini-3-flash-preview",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var gotText, gotThought string
	var final *StreamChunk
	for chunk := range ch {
		gotText += chunk.Content
		gotThought += chunk.Reasoning
		if chunk.Done {
			final = chunk
		}
	}
	if gotThought != "step 1" || gotText != "done" || final == nil {
		t.Fatalf("text=%q thought=%q final=%+v", gotText, gotThought, final)
	}
	if final.ReasoningSignature != "sig_stream_tc" || len(final.ToolCalls) != 1 || final.ToolCalls[0].ThoughtSignature != "sig_stream_tc" {
		t.Fatalf("expected preserved streamed toolCall signature, got final=%+v", final)
	}
}
