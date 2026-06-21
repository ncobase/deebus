package deebus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestPolicyApplyDefaultsCacheAndCCH(t *testing.T) {
	store := false
	req := &Request{
		Messages: []Message{
			{
				Role: "system",
				Content: []ContentBlock{
					TextContent{Type: "text", Text: "x-anthropic-billing-header: account=demo;cch=random-123;"},
				},
			},
			TextMessage("user", "hello"),
		},
		UserID:   "user-1",
		Metadata: map[string]string{"space_id": "space-1"},
	}

	policy := RequestPolicy{
		Defaults: RequestDefaults{Store: &store},
		PromptCache: PromptCachePolicy{
			Enabled:         true,
			Scope:           "tenant A",
			Client:          "console/browser",
			IncludeProvider: true,
			IncludeModel:    true,
			IncludeUser:     true,
			MetadataKeys:    []string{"space_id"},
			Retention:       "24h",
		},
		CacheBreaker: CacheBreakerPolicy{
			Enabled:                   true,
			AnthropicBillingHeaderCCH: true,
			Replacement:               "stable-1",
		},
		Fingerprint: FingerprintOptions{Salt: "test", IncludeText: true},
	}

	report, err := policy.Apply("openai", "gpt-4o", req)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if req.Store == nil || *req.Store {
		t.Fatalf("Store default not applied: %#v", req.Store)
	}
	if req.Cache == nil || req.Cache.Key == "" || req.Cache.Retention != "24h" {
		t.Fatalf("cache policy not applied: %#v", req.Cache)
	}
	if strings.Contains(req.Cache.Key, " ") || strings.Contains(req.Cache.Key, "/") {
		t.Fatalf("cache key was not normalized: %q", req.Cache.Key)
	}
	gotSystem := testExtractText(req.Messages[0].Content)
	if !strings.Contains(gotSystem, "cch=stable-1") {
		t.Fatalf("system prompt not rewritten: %q", gotSystem)
	}
	if report.Fingerprint == "" || report.Snapshot.Fingerprint == "" {
		t.Fatal("policy report must include fingerprint and snapshot")
	}
	if len(report.Changes) != 4 {
		t.Fatalf("changes=%#v, want 4 changes", report.Changes)
	}
	if report.Snapshot.Messages[0].TextHash == "" {
		t.Fatalf("snapshot should include text hash when IncludeText is true: %#v", report.Snapshot.Messages[0])
	}
}

func TestRequestPolicyReporterReceivesSuccessAndRejectedReports(t *testing.T) {
	var reports []RequestPolicyReport
	reporter := RequestPolicyReporterFunc(func(_ context.Context, report RequestPolicyReport) error {
		reports = append(reports, report)
		return nil
	})

	policy := RequestPolicy{
		Limits:   RequestLimits{MaxTextBytes: 4},
		Reporter: reporter,
	}

	_, err := policy.ApplyContext(context.Background(), "openai", "gpt-4o", &Request{
		Messages: []Message{TextMessage("user", "ok")},
	})
	if err != nil {
		t.Fatalf("ApplyContext() success path error = %v", err)
	}

	_, err = policy.ApplyContext(context.Background(), "openai", "gpt-4o", &Request{
		Messages: []Message{TextMessage("user", "too long")},
	})
	if err == nil {
		t.Fatal("ApplyContext() should reject oversized text")
	}

	if len(reports) != 2 {
		t.Fatalf("reports len = %d, want 2", len(reports))
	}
	if reports[0].Rejected || reports[0].Fingerprint == "" {
		t.Fatalf("success report malformed: %#v", reports[0])
	}
	if !reports[1].Rejected || !strings.Contains(reports[1].Error, "text bytes") || reports[1].Fingerprint == "" {
		t.Fatalf("rejection report malformed: %#v", reports[1])
	}
}

func TestRequestPolicyReporterErrorCanBeNonBlockingOrStrict(t *testing.T) {
	boom := errors.New("audit sink unavailable")
	policy := RequestPolicy{
		Reporter: RequestPolicyReporterFunc(func(context.Context, RequestPolicyReport) error {
			return boom
		}),
	}

	report, err := policy.ApplyContext(context.Background(), "openai", "gpt-4o", &Request{})
	if err != nil {
		t.Fatalf("non-strict reporter error should not fail request: %v", err)
	}
	if report.ReportError != boom.Error() {
		t.Fatalf("ReportError = %q, want %q", report.ReportError, boom.Error())
	}

	policy.FailOnReporterError = true
	if _, err := policy.ApplyContext(context.Background(), "openai", "gpt-4o", &Request{}); err == nil || !strings.Contains(err.Error(), "request policy reporter") {
		t.Fatalf("strict reporter should fail with wrapped error, got %v", err)
	}
}

func TestRequestPolicyPreservesExplicitCacheKey(t *testing.T) {
	req := &Request{Cache: &CacheOptions{Key: "caller-key"}}
	policy := RequestPolicy{
		PromptCache: PromptCachePolicy{Enabled: true, Key: "policy-key"},
	}
	if _, err := policy.Apply("openai", "gpt-4o", req); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if req.Cache.Key != "caller-key" {
		t.Fatalf("cache key = %q, want caller-key", req.Cache.Key)
	}
}

func TestRequestLimitsRejectOversizedText(t *testing.T) {
	req := &Request{Messages: []Message{TextMessage("user", "abcdef")}}
	err := RequestLimits{MaxTextBytes: 4}.Validate(req)
	if err == nil {
		t.Fatal("Validate() should reject oversized text")
	}
	if !strings.Contains(err.Error(), "text bytes") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloneRequestDeepCopy(t *testing.T) {
	req := &Request{
		Messages: []Message{{
			Role:    "user",
			Content: []ContentBlock{TextContent{Type: "text", Text: "original"}},
		}},
		Tools: []Tool{{
			Type: "function",
			Function: FunctionSchema{
				Name:       "lookup",
				Parameters: map[string]any{"properties": map[string]any{"id": map[string]any{"type": "string"}}},
			},
		}},
		Metadata: map[string]string{"space": "a"},
		Options:  map[string]any{"nested": map[string]any{"a": "b"}},
	}

	clone := CloneRequest(req)
	clone.Messages[0].Content[0] = TextContent{Type: "text", Text: "changed"}
	clone.Tools[0].Function.Parameters["properties"].(map[string]any)["id"] = "changed"
	clone.Metadata["space"] = "b"
	clone.Options["nested"].(map[string]any)["a"] = "c"

	if testExtractText(req.Messages[0].Content) != "original" {
		t.Fatal("CloneRequest mutated original message content")
	}
	if req.Tools[0].Function.Parameters["properties"].(map[string]any)["id"] == "changed" {
		t.Fatal("CloneRequest mutated original tool schema")
	}
	if req.Metadata["space"] != "a" || req.Options["nested"].(map[string]any)["a"] != "b" {
		t.Fatal("CloneRequest mutated original maps")
	}
}

func TestClientRequestPolicyAppliesBeforeProviderWithoutMutatingCaller(t *testing.T) {
	var captured map[string]any
	var reports []RequestPolicyReport
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 2,
				"total_tokens":      12,
			},
		})
	}))
	defer srv.Close()

	store := false
	client, err := NewClient(Config{
		Primary: "openai/gpt-4o",
		Providers: map[string]ProviderConfig{
			"openai": {Type: "openai", APIKey: "sk-test", BaseURL: srv.URL},
		},
		Retry: 0,
		RequestPolicy: RequestPolicy{
			Defaults: RequestDefaults{Store: &store},
			PromptCache: PromptCachePolicy{
				Enabled:         true,
				Scope:           "space-1",
				Client:          "console",
				IncludeProvider: true,
				IncludeModel:    true,
				Retention:       "in-memory",
			},
			CacheBreaker: CacheBreakerPolicy{
				Enabled:                   true,
				AnthropicBillingHeaderCCH: true,
				Replacement:               "fixed",
			},
			Reporter: RequestPolicyReporterFunc(func(_ context.Context, report RequestPolicyReport) error {
				reports = append(reports, report)
				return nil
			}),
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	req := &Request{Messages: []Message{{
		Role:    "system",
		Content: []ContentBlock{TextContent{Type: "text", Text: "x-anthropic-billing-header: cch=random;"}},
	}}}
	if _, err := client.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if captured["prompt_cache_key"] != "space-1:console:openai:gpt-4o" {
		t.Fatalf("prompt_cache_key=%#v", captured["prompt_cache_key"])
	}
	if captured["prompt_cache_retention"] != "in_memory" {
		t.Fatalf("prompt_cache_retention=%#v", captured["prompt_cache_retention"])
	}
	if captured["store"] != false {
		t.Fatalf("store=%#v, want false", captured["store"])
	}
	messages := captured["messages"].([]any)
	first := messages[0].(map[string]any)
	if !strings.Contains(wireMessageText(first["content"]), "cch=fixed") {
		t.Fatalf("wire system prompt not rewritten: %#v", first["content"])
	}
	if strings.Contains(testExtractText(req.Messages[0].Content), "cch=fixed") {
		t.Fatal("client policy mutated caller request")
	}
	if len(reports) != 1 || reports[0].Fingerprint == "" || reports[0].Rejected {
		t.Fatalf("client reporter did not receive success report: %#v", reports)
	}
}

func TestBuildPromptCacheKeyShortensLongValues(t *testing.T) {
	key := BuildPromptCacheKey(32, strings.Repeat("abc/", 40), "client")
	if len(key) > 32 {
		t.Fatalf("key len=%d, want <=32", len(key))
	}
	if strings.ContainsAny(key, " /") {
		t.Fatalf("key was not sanitized: %q", key)
	}
}

func TestRequestSnapshotSummarizesToolCallsWithoutArguments(t *testing.T) {
	req := &Request{
		Messages: []Message{{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "lookup", Arguments: `{"secret":"alpha"}`},
			}},
		}},
	}

	snapshot := SnapshotRequest("openai", "gpt-4o", req, FingerprintOptions{Salt: "salt"})
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if strings.Contains(string(raw), "alpha") || strings.Contains(string(raw), "secret") {
		t.Fatalf("snapshot leaked tool arguments: %s", raw)
	}
	if len(snapshot.Messages) != 1 || len(snapshot.Messages[0].ToolCalls) != 1 {
		t.Fatalf("tool call summary missing: %#v", snapshot.Messages)
	}
	if snapshot.Messages[0].ToolCalls[0].ArgumentsBytes == 0 || snapshot.Messages[0].ToolCalls[0].ArgumentsHash != "" {
		t.Fatalf("unexpected tool call summary without IncludeText: %#v", snapshot.Messages[0].ToolCalls[0])
	}

	fpWithoutText := FingerprintRequest("openai", "gpt-4o", req, FingerprintOptions{Salt: "salt"})
	req.Messages[0].ToolCalls[0].Function.Arguments = `{"secret":"bravo"}`
	if got := FingerprintRequest("openai", "gpt-4o", req, FingerprintOptions{Salt: "salt"}); got != fpWithoutText {
		t.Fatalf("fingerprint changed despite IncludeText=false: %s != %s", got, fpWithoutText)
	}

	withText := SnapshotRequest("openai", "gpt-4o", req, FingerprintOptions{Salt: "salt", IncludeText: true})
	if withText.Messages[0].ToolCalls[0].ArgumentsHash == "" {
		t.Fatalf("tool argument hash should be present when IncludeText=true: %#v", withText.Messages[0].ToolCalls[0])
	}
}

func testExtractText(blocks []ContentBlock) string {
	for _, block := range blocks {
		switch value := block.(type) {
		case TextContent:
			return value.Text
		case *TextContent:
			if value != nil {
				return value.Text
			}
		}
	}
	return ""
}

func wireMessageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var out strings.Builder
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := block["text"].(string); ok {
				out.WriteString(text)
			}
		}
		return out.String()
	default:
		return ""
	}
}
