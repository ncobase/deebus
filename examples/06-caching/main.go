// Example 06: Prompt caching for repeated static context.
//
// Shows how to:
// - Mark system prompts and tool definitions for Anthropic prompt caching.
// - Observe CacheUsage (created/read tokens) in the response.
// - Mark large user-turn documents (e.g. a retrieved PDF) for caching.
// - Read OpenAI's automatic cache stats (no markers required).
// - Use UserID for per-user attribution on both providers.
//
// Provider support:
// - Anthropic: explicit cache_control markers and top-level automatic caching.
// Minimum cacheable block size varies by model; consult Anthropic's current
// prompt-caching documentation when sizing reusable prefixes.
// - OpenAI: automatic prefix caching with request-level cache hints via
// Request.Cache.Key and Request.Cache.Retention.
// - Other providers: CacheControl markers are silently ignored.
//
// Run:
//
//	ANTHROPIC_API_KEY=sk-... go run .
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/ncobase/deebus"
)

func main() {
	client, err := deebus.NewClient(deebus.Config{
		Providers: map[string]deebus.ProviderConfig{
			"anthropic": {
				Type:    "anthropic",
				APIKey:  requireEnv("ANTHROPIC_API_KEY"),
				BaseURL: "https://api.anthropic.com",
			},
		},
		Primary: "anthropic/claude-haiku-4-5-20251001",
		Timeout: 60,
		Retry:   1,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	cacheSystemPrompt(ctx, client)
	cacheToolDefinitions(ctx, client)
	cacheLargeDocument(ctx, client)
}

// cacheSystemPrompt caches a long, static system prompt.
// The first request writes the prefix. Later requests reuse it within TTL.
func cacheSystemPrompt(ctx context.Context, client *deebus.Client) {
	fmt.Println("Cached system prompt")

	// The prompt exceeds Anthropic's minimum cacheable block size.
	systemText := buildLargeSystemPrompt()

	makeRequest := func(turn int) {
		resp, err := client.Complete(ctx, &deebus.Request{
			Messages: []deebus.Message{
				{
					Role: "system",
					Content: []deebus.ContentBlock{
						// Mark this block as the cache boundary.
						// Omitted TTL uses Anthropic's default 5-minute lifetime.
						deebus.TextContent{
							Type:         "text",
							Text:         systemText,
							CacheControl: &deebus.CacheControl{Type: "ephemeral"},
						},
					},
				},
				deebus.TextMessage("user", "Summarise your role in one sentence."),
			},
			MaxTokens: 64,
			UserID:    "example-user-001", // forwarded as metadata.user_id
		})
		if err != nil {
			log.Fatalf("turn %d: %v", turn, err)
		}

		fmt.Printf("Turn %d: %s\n", turn, resp.Content)
		printCacheStats(resp.CacheUsage)
	}

	// First call writes the prefix.
	makeRequest(1)
	// Second call can reuse the cached prefix.
	makeRequest(2)
	fmt.Println()
}

// cacheToolDefinitions marks the last tool as the cache boundary.
// Repeated requests can reuse the tool schema prefix.
func cacheToolDefinitions(ctx context.Context, client *deebus.Client) {
	fmt.Println("Cached tool definitions")

	tools := []deebus.Tool{
		{
			Type: "function",
			Function: deebus.FunctionSchema{
				Name:        "search_knowledge_base",
				Description: "Search the internal knowledge base for relevant documents.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"query": map[string]any{"type": "string"}},
					"required":   []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: deebus.FunctionSchema{
				Name:        "get_customer_record",
				Description: "Fetch a customer record by ID.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"customer_id": map[string]any{"type": "string"}},
					"required":   []string{"customer_id"},
				},
			},
			// Cache the full tools array at this boundary.
			CacheControl: &deebus.CacheControl{Type: "ephemeral"},
		},
	}

	resp, err := client.Complete(ctx, &deebus.Request{
		Messages: []deebus.Message{
			deebus.TextMessage("user", "What tools do you have access to?"),
		},
		Tools:      tools,
		ToolChoice: "none", // ask model to describe tools without calling them
		MaxTokens:  128,
	})
	if err != nil {
		log.Fatalf("tool cache: %v", err)
	}

	fmt.Printf("Response: %s\n", resp.Content)
	printCacheStats(resp.CacheUsage)
	fmt.Println()
}

// cacheLargeDocument caches a large retrieved document in the user turn.
// This fits repeated follow-up questions over the same source content.
func cacheLargeDocument(ctx context.Context, client *deebus.Client) {
	fmt.Println("Cached document in user turn")

	// The document exceeds the provider's minimum cache threshold.
	document := buildLargeDocument()

	askQuestion := func(question string) {
		resp, err := client.Complete(ctx, &deebus.Request{
			Messages: []deebus.Message{
				deebus.TextMessage("system", "You are a document analysis assistant."),
				{
					Role: "user",
					Content: []deebus.ContentBlock{
						// Only the question changes between requests.
						deebus.TextContent{
							Type:         "text",
							Text:         "Document:\n\n" + document,
							CacheControl: &deebus.CacheControl{Type: "ephemeral"},
						},
						deebus.TextContent{
							Type: "text",
							Text: "Question: " + question,
						},
					},
				},
			},
			MaxTokens: 128,
		})
		if err != nil {
			log.Fatalf("document cache: %v", err)
		}

		fmt.Printf("Q: %s\nA: %s\n", question, resp.Content)
		printCacheStats(resp.CacheUsage)
	}

	askQuestion("What is the main topic of this document?")
	askQuestion("List three key points from the document.")
	fmt.Println()
}

func printCacheStats(u deebus.CacheUsage) {
	if u.CreatedTokens > 0 || u.ReadTokens > 0 {
		fmt.Printf("  cache: wrote=%d read=%d\n", u.CreatedTokens, u.ReadTokens)
	} else {
		fmt.Println("  cache: no cache activity (token count may be below minimum threshold)")
	}
}

// buildLargeSystemPrompt generates a system prompt large enough to exceed
// the 1024-token minimum for Anthropic prompt caching (Haiku).
func buildLargeSystemPrompt() string {
	base := `You are an expert enterprise assistant with deep knowledge across multiple domains.

Your responsibilities include:
1. Providing accurate, well-reasoned answers to technical and business questions.
2. Analysing documents, data, and code with precision and clarity.
3. Following company policies and compliance requirements at all times.
4. Escalating ambiguous situations to human reviewers when appropriate.
5. Maintaining confidentiality of sensitive business information.

Communication standards:
- Use clear, concise language appropriate to the audience.
- Structure responses with headers and bullet points where helpful.
- Cite sources and acknowledge uncertainty when relevant.
- Avoid speculation without clearly labelling it as such.

`
	// Repeat to exceed the minimum token threshold for caching.
	return strings.Repeat(base, 6)
}

// buildLargeDocument generates a document large enough to be worth caching.
func buildLargeDocument() string {
	section := `Section: Enterprise Architecture Principles

Modern enterprise systems must balance scalability, maintainability, and cost efficiency.
Key architectural patterns include microservices, event-driven design, and domain-driven
design (DDD). Each pattern has trade-offs that must be evaluated against business needs.

Scalability considerations:
- Horizontal scaling is preferred for stateless services.
- Vertical scaling may be appropriate for database workloads.
- Caching at multiple layers (CDN, application, database) reduces load.
- Asynchronous processing via message queues decouples producer and consumer.

`
	return strings.Repeat(section, 8)
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("environment variable %s is required", key)
	}
	return v
}
