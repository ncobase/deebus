package deebus

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// testCreds holds per-provider credentials loaded from the credentials file.
type testCreds struct {
	AnthropicAPIKey  string
	AnthropicBaseURL string
	OpenAIAPIKey     string
	OpenAIBaseURL    string
	GeminiAPIKey     string
	GeminiBaseURL    string
}

// integrationEnv enables live integration tests against real provider APIs.
const integrationEnv = "DEEBUS_RUN_INTEGRATION"

// credentialsFile is the name of the local credentials file used for
// integration tests. The file uses KEY=VALUE lines (comments with #).
const credentialsFile = "token.test"

// requireIntegrationEnabled skips live integration tests unless explicitly
// enabled by the caller.
func requireIntegrationEnabled(t *testing.T) {
	t.Helper()

	switch strings.ToLower(strings.TrimSpace(os.Getenv(integrationEnv))) {
	case "1", "true", "yes":
		return
	default:
		t.Skipf("set %s=1 to run live integration tests", integrationEnv)
	}
}

// loadTestCreds reads the credentials file from the module root.
// If the file does not exist the calling test is skipped (not failed).
func loadTestCreds(t *testing.T) testCreds {
	t.Helper()
	f, err := os.Open(credentialsFile)
	if err != nil {
		t.Skipf("credentials file %q not found - skipping integration tests", credentialsFile)
	}
	defer f.Close()

	var c testCreds
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "ANTHROPIC_API_KEY":
			c.AnthropicAPIKey = strings.TrimSpace(v)
		case "ANTHROPIC_BASE_URL":
			// Strip version suffix - the provider appends the path segment itself.
			c.AnthropicBaseURL = strings.TrimSuffix(strings.TrimSpace(v), "/v1")
		case "OPENAI_API_KEY":
			c.OpenAIAPIKey = strings.TrimSpace(v)
		case "OPENAI_BASE_URL":
			// Strip version suffix - the provider appends the path segment itself.
			c.OpenAIBaseURL = strings.TrimSuffix(strings.TrimSpace(v), "/v1")
		case "GEMINI_API_KEY":
			c.GeminiAPIKey = strings.TrimSpace(v)
		case "GEMINI_BASE_URL":
			// Strip version suffix - the provider appends the path segment itself.
			c.GeminiBaseURL = strings.TrimSuffix(strings.TrimSpace(v), "/v1beta")
		}
	}
	if err := sc.Err(); err != nil {
		t.Skipf("credentials file scan error: %v - skipping integration tests", err)
	}
	return c
}

// newIntegrationClient creates a single-provider client with no retry and a
// 60-second timeout.
func newIntegrationClient(t *testing.T, name, typ, baseURL, apiKey, primary string) *Client {
	t.Helper()
	c, err := NewClient(Config{
		Providers: map[string]ProviderConfig{
			name: {Type: typ, APIKey: apiKey, BaseURL: baseURL},
		},
		Primary: primary,
		Retry:   0,
		Timeout: 60,
	})
	if err != nil {
		t.Fatalf("newIntegrationClient(%s): %v", name, err)
	}
	return c
}

// checkTokens asserts that all three token fields are consistent.
func checkTokens(t *testing.T, label string, input, output, total int) {
	t.Helper()
	if input <= 0 {
		t.Errorf("%s: input tokens want >0, got %d", label, input)
	}
	if output <= 0 {
		t.Errorf("%s: output tokens want >0, got %d", label, output)
	}
	if total != input+output {
		t.Errorf("%s: total=%d != input(%d)+output(%d)", label, total, input, output)
	}
}

// buildLongSystemPrompt returns a substantial system-prompt string (~6000
// characters, well over 1500 tokens) suitable for testing Anthropic prompt
// caching (minimum 1024 tokens for Sonnet models).
func buildLongSystemPrompt() string {
	return `You are a senior technical documentation assistant specialising in distributed systems, cloud-native architectures, and high-performance backend engineering. Your purpose is to help software engineers write, review, and improve technical documentation, API references, architecture decision records (ADRs), runbooks, and developer guides.

## Core Capabilities

### Technical Writing
You produce clear, precise, and unambiguous technical prose. You understand the difference between user-facing documentation (which must prioritise clarity and example-driven learning) and internal reference documentation (which must prioritise completeness and accuracy). You can write in multiple formats: Markdown, reStructuredText, AsciiDoc, and plain prose. You are equally comfortable writing a one-paragraph overview, a detailed multi-page architecture guide, or a structured API reference.

### Go and Backend Systems
You have deep familiarity with the Go programming language, including its standard library, idioms, concurrency primitives (goroutines, channels, sync package, atomic operations), and ecosystem tools (go generate, go test, go build constraints, module system). You can explain, document, and review Go code at any level: from simple helper functions to complex concurrent data structures, HTTP servers, gRPC services, and database access layers.

You understand common backend patterns such as:
- Repository and service layer separation
- Dependency injection and provider patterns
- Middleware chains and handler pipelines
- Circuit breaker, retry with exponential backoff, and rate limiting
- Event-driven architectures and message brokers
- Cursor-based and keyset pagination
- Distributed tracing with OpenTelemetry
- Structured logging with context propagation
- Graceful shutdown and signal handling

### API Design and Documentation
You are proficient in REST API design principles: resource naming, HTTP method semantics, status code selection, pagination strategies, versioning, and error response formats. You can write OpenAPI 3.x specifications, annotate Go handler functions with Swagger comments, and produce human-readable API reference documentation from raw specifications.

You also understand gRPC, Protocol Buffers, GraphQL, and WebSocket-based protocols. You can document streaming APIs, bidirectional communication patterns, and connection lifecycle management.

### Distributed Systems Concepts
Your knowledge covers the following areas in depth:
- CAP theorem, consistency models (eventual, strong, linearisable, causal)
- Consensus algorithms (Raft, Paxos) at a conceptual and operational level
- Distributed transactions: two-phase commit, saga pattern, outbox pattern
- Service mesh concepts: load balancing, service discovery, health checking, mTLS
- Observability: metrics (RED/USE method), distributed tracing, structured logs, alerting
- Caching strategies: cache-aside, read-through, write-through, write-behind; TTL selection; cache stampede prevention; prompt caching for LLM workloads
- Database internals: B-tree and LSM-tree storage engines, MVCC, WAL, index design, query planning
- Message queues: at-most-once, at-least-once, exactly-once delivery semantics; dead-letter queues; consumer group management

### Large Language Model (LLM) Integration
You understand the practical aspects of integrating LLMs into production systems:
- Provider APIs: OpenAI, Anthropic, Google Gemini, Ollama, Cohere
- Prompt engineering best practices: system prompts, few-shot examples, chain-of-thought reasoning
- Prompt caching: Anthropic explicit cache_control markers, OpenAI automatic prefix caching, Gemini context caching
- Token counting, cost estimation, and budget management
- Streaming responses: SSE and NDJSON parsing, back-pressure handling
- Tool calling / function calling: schema design, multi-turn conversation management, parallel tool dispatch
- Agent loops: iterative model-tool interaction, history trimming, max-iteration guards
- Model Context Protocol (MCP): stdio and HTTP transports, tool discovery, server lifecycle

## Formatting and Style Guidelines

### General Rules
- Use active voice and present tense.
- Keep sentences short (under 25 words where possible).
- Use numbered lists for sequential steps; use bullet lists for unordered sets of items.
- Include code examples for every non-trivial concept. All code examples must be runnable or clearly marked as pseudocode.
- Always specify the language identifier in fenced code blocks.
- Use tables for comparison information (e.g., provider feature matrices, configuration reference tables).
- Link to relevant external documentation when introducing external dependencies.

### Code Examples
- Go examples must be idiomatic: handle errors explicitly, use context correctly, close resources with defer.
- Variable names must be descriptive; avoid single-letter names except for loop indices and common conventions (ctx, err, t for testing.T).
- Include import blocks only when they add clarity; omit them for very short snippets.
- Annotate non-obvious lines with inline comments.

### Tone
- Be direct and factual. Avoid hedging language ("it might", "perhaps", "you could consider").
- Do not use marketing language or superlatives.
- When there are multiple valid approaches, explain the trade-offs neutrally and recommend based on stated constraints.
- Acknowledge limitations and failure modes honestly.

## Constraints

- Do not fabricate API endpoints, configuration options, or library function signatures. If uncertain, state that verification against the source is required.
- Do not reproduce large blocks of code verbatim from external sources without attribution.
- When asked to document an API, ask for the actual function signatures and types before writing the reference rather than guessing.
- Flag any security-sensitive operations (credential handling, secret storage, input validation, SQL injection surfaces) with a prominent note.
- All monetary amounts and SLA percentages must be clearly labelled with units and measurement methodology.

## Knowledge Areas Summary

Primary: Go, distributed systems, REST/gRPC APIs, LLM integration, observability, caching, databases (PostgreSQL, MySQL, Redis), message queues (Kafka, NATS, RabbitMQ), container orchestration (Kubernetes), cloud platforms (AWS, GCP, Azure), CI/CD pipelines.

Secondary: TypeScript/React for frontend-adjacent documentation, Python for data pipeline documentation, Terraform for infrastructure documentation.

Your responses are always grounded in the specific context provided. You ask clarifying questions before writing long documents to ensure the output meets the reader's needs.`
}

func TestIntegrationAnthropic(t *testing.T) {
	requireIntegrationEnabled(t)

	creds := loadTestCreds(t)
	if creds.AnthropicAPIKey == "" || creds.AnthropicBaseURL == "" {
		t.Skip("Anthropic credentials not configured")
	}

	client := newIntegrationClient(t,
		"anthropic", "anthropic",
		creds.AnthropicBaseURL, creds.AnthropicAPIKey,
		"anthropic/claude-sonnet-4-6",
	)

	ctx := context.Background()

	// Pre-flight: verify the endpoint responds successfully before running subtests.
	{
		preCtx, preCancel := context.WithTimeout(ctx, 30*time.Second)
		_, preErr := client.Complete(preCtx, &Request{
			Messages:  []Message{TextMessage("user", "Hi.")},
			MaxTokens: 8,
		})
		preCancel()
		if preErr != nil {
			t.Skipf("Anthropic endpoint unavailable: %v", preErr)
		}
	}

	t.Run("complete", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		resp, err := client.Complete(reqCtx, &Request{
			Messages:  []Message{TextMessage("user", "Say hello in one sentence.")},
			MaxTokens: 64,
		})
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if resp.Content == "" {
			t.Error("response Content is empty")
		}
		checkTokens(t, "complete", resp.InputTokens, resp.OutputTokens, resp.TokensUsed)
		t.Logf("content=%q model=%s provider=%s input=%d output=%d total=%d finish=%s reasoning=%d cache_created=%d cache_read=%d",
			resp.Content, resp.Model, resp.Provider,
			resp.InputTokens, resp.OutputTokens, resp.TokensUsed,
			resp.FinishReason, resp.ReasoningTokens,
			resp.CacheUsage.CreatedTokens, resp.CacheUsage.ReadTokens)
	})

	t.Run("stream", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		ch, err := client.Stream(reqCtx, &Request{
			Messages:  []Message{TextMessage("user", "Count to three, one number per line.")},
			MaxTokens: 64,
		})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}

		var full strings.Builder
		var doneChunk *StreamChunk
		for chunk := range ch {
			if chunk.Error != nil {
				t.Fatalf("stream chunk error: %v", chunk.Error)
			}
			full.WriteString(chunk.Content)
			if chunk.Done {
				doneChunk = chunk
			}
		}

		if doneChunk == nil {
			t.Fatal("stream closed without Done chunk")
		}
		checkTokens(t, "stream", doneChunk.InputTokens, doneChunk.OutputTokens, doneChunk.TokensUsed)
		t.Logf("streamed=%q finish=%s input=%d output=%d total=%d reasoning=%d cache_created=%d cache_read=%d",
			full.String(), doneChunk.FinishReason,
			doneChunk.InputTokens, doneChunk.OutputTokens, doneChunk.TokensUsed,
			doneChunk.ReasoningTokens,
			doneChunk.CacheUsage.CreatedTokens, doneChunk.CacheUsage.ReadTokens)
	})

	t.Run("cache_write_then_read", func(t *testing.T) {
		systemPrompt := buildLongSystemPrompt()

		makeReq := func() *Request {
			return &Request{
				Messages: []Message{
					{
						Role: "system",
						Content: []ContentBlock{
							TextContent{
								Type:         "text",
								Text:         systemPrompt,
								CacheControl: &CacheControl{Type: "ephemeral"},
							},
						},
					},
					TextMessage("user", "Summarise your role in one sentence."),
				},
				MaxTokens: 64,
			}
		}

		reqCtx1, cancel1 := context.WithTimeout(ctx, 90*time.Second)
		defer cancel1()

		r1, err := client.Complete(reqCtx1, makeReq())
		if err != nil {
			t.Fatalf("cache request 1: %v", err)
		}
		t.Logf("r1: input=%d output=%d cache_created=%d cache_read=%d",
			r1.InputTokens, r1.OutputTokens,
			r1.CacheUsage.CreatedTokens, r1.CacheUsage.ReadTokens)

		reqCtx2, cancel2 := context.WithTimeout(ctx, 90*time.Second)
		defer cancel2()

		r2, err := client.Complete(reqCtx2, makeReq())
		if err != nil {
			t.Fatalf("cache request 2: %v", err)
		}
		t.Logf("r2: input=%d output=%d cache_created=%d cache_read=%d",
			r2.InputTokens, r2.OutputTokens,
			r2.CacheUsage.CreatedTokens, r2.CacheUsage.ReadTokens)

		if r2.CacheUsage.ReadTokens == 0 {
			t.Log("second request did not report cached tokens")
		}

		total, _, _, _, _ := client.Stats.Get()
		cacheCreated := client.Stats.CacheCreatedTokens.Load()
		cacheRead := client.Stats.CacheReadTokens.Load()
		t.Logf("stats after cache test: requests=%d cache_writes=%d cache_reads=%d",
			total, cacheCreated, cacheRead)

		if cacheCreated+cacheRead == 0 {
			t.Error("expected Stats.CacheCreatedTokens+Stats.CacheReadTokens > 0 after two caching requests")
		}
	})

	t.Run("automatic_cache_write_then_read", func(t *testing.T) {
		systemPrompt := buildLongSystemPrompt()

		makeReq := func() *Request {
			return &Request{
				Messages: []Message{
					TextMessage("system", systemPrompt),
					TextMessage("user", "Summarise your role in one sentence."),
				},
				MaxTokens: 64,
				Cache: &CacheOptions{
					Control: &CacheControl{Type: "ephemeral"},
				},
			}
		}

		reqCtx1, cancel1 := context.WithTimeout(ctx, 90*time.Second)
		defer cancel1()

		r1, err := client.Complete(reqCtx1, makeReq())
		if err != nil {
			t.Fatalf("automatic cache request 1: %v", err)
		}
		t.Logf("r1: input=%d output=%d cache_created=%d cache_read=%d",
			r1.InputTokens, r1.OutputTokens,
			r1.CacheUsage.CreatedTokens, r1.CacheUsage.ReadTokens)

		reqCtx2, cancel2 := context.WithTimeout(ctx, 90*time.Second)
		defer cancel2()

		r2, err := client.Complete(reqCtx2, makeReq())
		if err != nil {
			t.Fatalf("automatic cache request 2: %v", err)
		}
		t.Logf("r2: input=%d output=%d cache_created=%d cache_read=%d",
			r2.InputTokens, r2.OutputTokens,
			r2.CacheUsage.CreatedTokens, r2.CacheUsage.ReadTokens)

		if r2.CacheUsage.ReadTokens == 0 {
			t.Log("second automatic-cache request did not report cached tokens")
		}
	})

	t.Run("userid_attribution", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		_, err := client.Complete(reqCtx, &Request{
			Messages:  []Message{TextMessage("user", "Ping.")},
			MaxTokens: 16,
			UserID:    "integration-test-user",
		})
		if err != nil {
			t.Fatalf("Complete with UserID: %v", err)
		}
		t.Log("userid_attribution: request succeeded with UserID set")
	})

	t.Run("stats", func(t *testing.T) {
		total, input, output, success, failed := client.Stats.Get()
		cacheCreated := client.Stats.CacheCreatedTokens.Load()
		cacheRead := client.Stats.CacheReadTokens.Load()
		t.Logf("stats: total=%d input=%d output=%d success=%d failed=%d cache_writes=%d cache_reads=%d",
			total, input, output, success, failed, cacheCreated, cacheRead)

		if total <= 0 {
			t.Error("Stats.TotalRequests want >0")
		}
		if success <= 0 {
			t.Error("Stats.SuccessRequests want >0")
		}
		if failed != 0 {
			t.Errorf("Stats.FailedRequests want 0, got %d", failed)
		}
	})
}

func TestIntegrationOpenAI(t *testing.T) {
	requireIntegrationEnabled(t)

	creds := loadTestCreds(t)
	if creds.OpenAIAPIKey == "" || creds.OpenAIBaseURL == "" {
		t.Skip("OpenAI credentials not configured")
	}

	client := newIntegrationClient(t,
		"openai", "openai",
		creds.OpenAIBaseURL, creds.OpenAIAPIKey,
		"openai/gpt-5.4",
	)

	ctx := context.Background()

	t.Run("complete", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		resp, err := client.Complete(reqCtx, &Request{
			Messages:  []Message{TextMessage("user", "Say hello in one sentence.")},
			MaxTokens: 64,
		})
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if resp.Content == "" {
			t.Error("response Content is empty")
		}
		checkTokens(t, "complete", resp.InputTokens, resp.OutputTokens, resp.TokensUsed)
		t.Logf("content=%q model=%s provider=%s input=%d output=%d total=%d finish=%s reasoning=%d cache_read=%d",
			resp.Content, resp.Model, resp.Provider,
			resp.InputTokens, resp.OutputTokens, resp.TokensUsed,
			resp.FinishReason, resp.ReasoningTokens, resp.CacheUsage.ReadTokens)
		t.Logf("reasoning_tokens=%d (expected 0 for non-o-series model)", resp.ReasoningTokens)
	})

	t.Run("stream", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		ch, err := client.Stream(reqCtx, &Request{
			Messages:  []Message{TextMessage("user", "Count to three, one number per line.")},
			MaxTokens: 64,
		})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}

		var full strings.Builder
		var doneChunk *StreamChunk
		for chunk := range ch {
			if chunk.Error != nil {
				t.Fatalf("stream chunk error: %v", chunk.Error)
			}
			full.WriteString(chunk.Content)
			if chunk.Done {
				doneChunk = chunk
			}
		}

		if doneChunk == nil {
			t.Fatal("stream closed without Done chunk")
		}
		checkTokens(t, "stream", doneChunk.InputTokens, doneChunk.OutputTokens, doneChunk.TokensUsed)
		t.Logf("streamed=%q finish=%s input=%d output=%d total=%d reasoning=%d cache_read=%d",
			full.String(), doneChunk.FinishReason,
			doneChunk.InputTokens, doneChunk.OutputTokens, doneChunk.TokensUsed,
			doneChunk.ReasoningTokens, doneChunk.CacheUsage.ReadTokens)
	})

	t.Run("embed", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		embedClient := newIntegrationClient(t,
			"openai", "openai",
			creds.OpenAIBaseURL, creds.OpenAIAPIKey,
			"openai/text-embedding-3-small",
		)

		resp, err := embedClient.Embed(reqCtx, &EmbedRequest{
			Input: []string{
				"The quick brown fox jumps over the lazy dog",
				"Go is an open source programming language",
			},
			Model: "text-embedding-3-small",
		})
		if err != nil {
			if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
				t.Skipf("Embed endpoint not available: %v", err)
			}
			t.Fatalf("Embed: %v", err)
		}
		if len(resp.Embeddings) != 2 {
			t.Fatalf("expected 2 embeddings, got %d", len(resp.Embeddings))
		}
		for i, emb := range resp.Embeddings {
			if len(emb) == 0 {
				t.Errorf("embedding[%d] is empty", i)
			}
		}
		t.Logf("embed: model=%s vectors=%d dim=%d tokens=%d",
			resp.Model, len(resp.Embeddings), len(resp.Embeddings[0]), resp.TokensUsed)
	})

	t.Run("cached_tokens", func(t *testing.T) {
		// OpenAI caches automatically for prompts >=1024 tokens.
		// Short prompts will not be cached - just log the result.
		makeReq := func() *Request {
			return &Request{
				Messages:  []Message{TextMessage("user", "What is 2+2?")},
				MaxTokens: 16,
			}
		}

		reqCtx1, cancel1 := context.WithTimeout(ctx, 90*time.Second)
		defer cancel1()

		r1, err := client.Complete(reqCtx1, makeReq())
		if err != nil {
			t.Fatalf("cached_tokens request 1: %v", err)
		}
		t.Logf("r1: input=%d output=%d cache_read=%d", r1.InputTokens, r1.OutputTokens, r1.CacheUsage.ReadTokens)

		reqCtx2, cancel2 := context.WithTimeout(ctx, 90*time.Second)
		defer cancel2()

		r2, err := client.Complete(reqCtx2, makeReq())
		if err != nil {
			t.Fatalf("cached_tokens request 2: %v", err)
		}
		t.Logf("r2: input=%d output=%d cache_read=%d (may be 0 for short prompts - OpenAI auto-caches >=1024 tokens)",
			r2.InputTokens, r2.OutputTokens, r2.CacheUsage.ReadTokens)
	})

	t.Run("prompt_cache_key_and_retention", func(t *testing.T) {
		longPrompt := buildLongSystemPrompt()

		makeReq := func() *Request {
			return &Request{
				Messages: []Message{
					TextMessage("system", longPrompt),
					TextMessage("user", "Summarise your role in one sentence."),
				},
				MaxTokens: 64,
				Cache: &CacheOptions{
					Key:       "integration-test-openai-cache",
					Retention: "in_memory",
				},
			}
		}

		reqCtx1, cancel1 := context.WithTimeout(ctx, 90*time.Second)
		defer cancel1()

		r1, err := client.Complete(reqCtx1, makeReq())
		if err != nil {
			t.Fatalf("prompt_cache_key request 1: %v", err)
		}
		t.Logf("r1: input=%d output=%d cache_read=%d",
			r1.InputTokens, r1.OutputTokens, r1.CacheUsage.ReadTokens)

		reqCtx2, cancel2 := context.WithTimeout(ctx, 90*time.Second)
		defer cancel2()

		r2, err := client.Complete(reqCtx2, makeReq())
		if err != nil {
			t.Fatalf("prompt_cache_key request 2: %v", err)
		}
		t.Logf("r2: input=%d output=%d cache_read=%d",
			r2.InputTokens, r2.OutputTokens, r2.CacheUsage.ReadTokens)

		if r2.CacheUsage.ReadTokens == 0 {
			t.Log("second keyed OpenAI request did not report cached tokens")
		}
	})

	t.Run("stats", func(t *testing.T) {
		total, input, output, success, failed := client.Stats.Get()
		cacheRead := client.Stats.CacheReadTokens.Load()
		t.Logf("stats: total=%d input=%d output=%d success=%d failed=%d cache_reads=%d",
			total, input, output, success, failed, cacheRead)

		if total <= 0 {
			t.Error("Stats.TotalRequests want >0")
		}
		if success <= 0 {
			t.Error("Stats.SuccessRequests want >0")
		}
		if failed != 0 {
			t.Errorf("Stats.FailedRequests want 0, got %d", failed)
		}
	})
}

func TestIntegrationGemini(t *testing.T) {
	requireIntegrationEnabled(t)

	creds := loadTestCreds(t)
	if creds.GeminiAPIKey == "" || creds.GeminiBaseURL == "" {
		t.Skip("Gemini credentials not configured")
	}

	client := newIntegrationClient(t,
		"gemini", "gemini",
		creds.GeminiBaseURL, creds.GeminiAPIKey,
		"gemini/gemini-3.1-pro",
	)

	ctx := context.Background()

	t.Run("complete", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		resp, err := client.Complete(reqCtx, &Request{
			Messages:  []Message{TextMessage("user", "Say hello in one sentence.")},
			MaxTokens: 64,
		})
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if resp.Content == "" {
			t.Error("response Content is empty")
		}
		checkTokens(t, "complete", resp.InputTokens, resp.OutputTokens, resp.TokensUsed)
		t.Logf("content=%q model=%s provider=%s input=%d output=%d total=%d finish=%s reasoning=%d cache_read=%d",
			resp.Content, resp.Model, resp.Provider,
			resp.InputTokens, resp.OutputTokens, resp.TokensUsed,
			resp.FinishReason, resp.ReasoningTokens, resp.CacheUsage.ReadTokens)
		t.Logf("reasoning_tokens=%d (populated for thinking models only)", resp.ReasoningTokens)
	})

	t.Run("stream", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		ch, err := client.Stream(reqCtx, &Request{
			Messages:  []Message{TextMessage("user", "Count to three, one number per line.")},
			MaxTokens: 64,
		})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}

		var full strings.Builder
		var doneChunk *StreamChunk
		for chunk := range ch {
			if chunk.Error != nil {
				t.Fatalf("stream chunk error: %v", chunk.Error)
			}
			full.WriteString(chunk.Content)
			if chunk.Done {
				doneChunk = chunk
			}
		}

		if doneChunk == nil {
			t.Fatal("stream closed without Done chunk")
		}
		checkTokens(t, "stream", doneChunk.InputTokens, doneChunk.OutputTokens, doneChunk.TokensUsed)
		t.Logf("streamed=%q finish=%s input=%d output=%d total=%d reasoning=%d cache_read=%d",
			full.String(), doneChunk.FinishReason,
			doneChunk.InputTokens, doneChunk.OutputTokens, doneChunk.TokensUsed,
			doneChunk.ReasoningTokens, doneChunk.CacheUsage.ReadTokens)
	})

	t.Run("explicit_cache_lifecycle", func(t *testing.T) {
		largeContext := strings.Repeat(buildLongSystemPrompt(), 4)

		reqCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		cache, err := client.CreateCache(reqCtx, "gemini", &CreateCacheRequest{
			Model:       "gemini-3.1-pro",
			DisplayName: "integration-test-cache",
			Messages: []Message{
				TextMessage("system", "You are a concise assistant."),
				TextMessage("user", largeContext),
			},
			TTL: 10 * time.Minute,
		})
		if err != nil {
			if strings.Contains(err.Error(), "404") ||
				strings.Contains(err.Error(), "not found") ||
				strings.Contains(err.Error(), "UNIMPLEMENTED") {
				t.Skipf("Gemini explicit cache API unavailable: %v", err)
			}
			t.Fatalf("CreateCache: %v", err)
		}
		t.Cleanup(func() {
			delCtx, delCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer delCancel()
			if err := client.DeleteCache(delCtx, "gemini", cache.Name); err != nil {
				t.Logf("cleanup delete cache %q: %v", cache.Name, err)
			}
		})

		if cache.Name == "" {
			t.Fatal("CreateCache returned an empty cache name")
		}

		got, err := client.GetCache(reqCtx, "gemini", cache.Name)
		if err != nil {
			t.Fatalf("GetCache: %v", err)
		}
		if got.Name != cache.Name {
			t.Fatalf("GetCache.Name = %q, want %q", got.Name, cache.Name)
		}

		updated, err := client.UpdateCache(reqCtx, "gemini", &UpdateCacheRequest{
			Name: cache.Name,
			TTL:  15 * time.Minute,
		})
		if err != nil {
			t.Fatalf("UpdateCache: %v", err)
		}
		if updated.Name != cache.Name {
			t.Fatalf("UpdateCache.Name = %q, want %q", updated.Name, cache.Name)
		}

		list, err := client.ListCaches(reqCtx, "gemini", &ListCachesRequest{PageSize: 10})
		if err != nil {
			t.Fatalf("ListCaches: %v", err)
		}
		found := false
		for _, item := range list.Items {
			if item.Name == cache.Name {
				found = true
				break
			}
		}
		if !found {
			t.Logf("created cache %q was not found in the first list page", cache.Name)
		}

		resp, err := client.Complete(reqCtx, &Request{
			Messages:  []Message{TextMessage("user", "Summarise the cached document in one sentence.")},
			MaxTokens: 64,
			Cache: &CacheOptions{
				CachedContent: cache.Name,
			},
		})
		if err != nil {
			t.Fatalf("Complete with cached content: %v", err)
		}
		if resp.CacheUsage.ReadTokens == 0 {
			t.Log("Gemini explicit cache use did not report cached tokens")
		}
		t.Logf("explicit cache use: input=%d output=%d cache_read=%d",
			resp.InputTokens, resp.OutputTokens, resp.CacheUsage.ReadTokens)
	})

	t.Run("stats", func(t *testing.T) {
		total, input, output, success, failed := client.Stats.Get()
		cacheRead := client.Stats.CacheReadTokens.Load()
		t.Logf("stats: total=%d input=%d output=%d success=%d failed=%d cache_reads=%d",
			total, input, output, success, failed, cacheRead)

		if total <= 0 {
			t.Error("Stats.TotalRequests want >0")
		}
		if success <= 0 {
			t.Error("Stats.SuccessRequests want >0")
		}
		if failed != 0 {
			t.Errorf("Stats.FailedRequests want 0, got %d", failed)
		}
	})
}

func TestIntegrationMultiProvider(t *testing.T) {
	requireIntegrationEnabled(t)

	creds := loadTestCreds(t)
	if creds.AnthropicAPIKey == "" || creds.AnthropicBaseURL == "" {
		t.Skip("Anthropic credentials not configured")
	}
	if creds.OpenAIAPIKey == "" || creds.OpenAIBaseURL == "" {
		t.Skip("OpenAI credentials not configured")
	}

	// Verify the Anthropic endpoint is reachable before building the multi-provider client.
	{
		probe := newIntegrationClient(t, "anthropic", "anthropic",
			creds.AnthropicBaseURL, creds.AnthropicAPIKey, "anthropic/claude-sonnet-4-6")
		pCtx, pCancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, pErr := probe.Complete(pCtx, &Request{
			Messages:  []Message{TextMessage("user", "Hi.")},
			MaxTokens: 8,
		})
		pCancel()
		if pErr != nil {
			t.Skipf("Anthropic endpoint unavailable: %v", pErr)
		}
	}

	client, err := NewClient(Config{
		Providers: map[string]ProviderConfig{
			"anthropic": {
				Type:    "anthropic",
				APIKey:  creds.AnthropicAPIKey,
				BaseURL: creds.AnthropicBaseURL,
			},
			"openai": {
				Type:    "openai",
				APIKey:  creds.OpenAIAPIKey,
				BaseURL: creds.OpenAIBaseURL,
			},
		},
		Primary:   "anthropic/claude-sonnet-4-6",
		Fallbacks: []string{"openai/gpt-5.4"},
		Retry:     0,
		Timeout:   60,
	})
	if err != nil {
		t.Fatalf("NewClient (multi-provider): %v", err)
	}

	ctx := context.Background()

	t.Run("primary_succeeds", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		resp, err := client.Complete(reqCtx, &Request{
			Messages:  []Message{TextMessage("user", "What provider are you from? One word.")},
			MaxTokens: 32,
		})
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		t.Logf("primary_succeeds: content=%q provider=%s model=%s input=%d output=%d total=%d",
			resp.Content, resp.Provider, resp.Model,
			resp.InputTokens, resp.OutputTokens, resp.TokensUsed)
	})

	t.Run("aggregate_stats", func(t *testing.T) {
		total, input, output, success, failed := client.Stats.Get()
		cacheCreated := client.Stats.CacheCreatedTokens.Load()
		cacheRead := client.Stats.CacheReadTokens.Load()
		t.Logf("aggregate_stats: total=%d input=%d output=%d success=%d failed=%d cache_writes=%d cache_reads=%d",
			total, input, output, success, failed, cacheCreated, cacheRead)

		if total <= 0 {
			t.Error("Stats.TotalRequests want >0")
		}
		if input+output <= 0 {
			t.Error("Stats.InputTokens+Stats.OutputTokens want >0")
		}
	})

	t.Run("stream", func(t *testing.T) {
		reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()

		ch, err := client.Stream(reqCtx, &Request{
			Messages:  []Message{TextMessage("user", "Write a haiku about Go concurrency.")},
			MaxTokens: 64,
		})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}

		var full strings.Builder
		var doneChunk *StreamChunk
		for chunk := range ch {
			if chunk.Error != nil {
				t.Fatalf("stream chunk error: %v", chunk.Error)
			}
			full.WriteString(chunk.Content)
			if chunk.Done {
				doneChunk = chunk
			}
		}

		if doneChunk == nil {
			t.Fatal("stream closed without Done chunk")
		}
		t.Logf("stream: content=%q finish=%s input=%d output=%d total=%d",
			full.String(), doneChunk.FinishReason,
			doneChunk.InputTokens, doneChunk.OutputTokens, doneChunk.TokensUsed)
	})
}
