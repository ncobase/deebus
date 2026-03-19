// Example 04: Embeddings — vector generation and semantic search.
//
// Shows how to:
//   - Embed a corpus of documents with InputType hints.
//   - Embed a query and find the most similar documents using cosine similarity.
//   - Compare embedding providers (OpenAI vs Cohere vs Gemini vs Ollama).
//
// Run:
//
//	OPENAI_API_KEY=sk-... go run .
package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"sort"

	"github.com/ncobase/deebus"
)

func main() {
	client, err := deebus.NewClient(deebus.Config{
		Providers: map[string]deebus.ProviderConfig{
			"openai": {
				Type:    "openai",
				APIKey:  requireEnv("OPENAI_API_KEY"),
				BaseURL: "https://api.openai.com",
			},
		},
		Primary: "openai/text-embedding-3-small",
		Timeout: 30,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	semanticSearch(ctx, client)
	batchDimensions(ctx, client)
}

// ── Corpus ────────────────────────────────────────────────────────────────────

var corpus = []string{
	"Go is a statically typed, compiled programming language designed at Google.",
	"The circuit breaker pattern prevents repeated calls to a failing service.",
	"Exponential backoff with jitter reduces thundering herd during retries.",
	"Kubernetes orchestrates containerised workloads across clusters of machines.",
	"Transformer models use self-attention to process sequences in parallel.",
	"A token bucket algorithm controls the rate of requests over time.",
	"gRPC uses Protocol Buffers to define strongly-typed service contracts.",
	"Context propagation in Go allows cancellation signals to flow through layers.",
}

// ── Semantic search ───────────────────────────────────────────────────────────

func semanticSearch(ctx context.Context, client *deebus.Client) {
	fmt.Println("── Semantic search ──────────────────────────────────────────────────")

	// Embed the corpus as retrieval documents.
	docResp, err := client.Embed(ctx, &deebus.EmbedRequest{
		Input:     corpus,
		InputType: "search_document",
	})
	if err != nil {
		log.Fatalf("embed documents: %v", err)
	}
	fmt.Printf("Embedded %d documents  dim=%d\n\n", len(docResp.Embeddings), len(docResp.Embeddings[0]))

	queries := []string{
		"How does Go handle concurrency?",
		"What prevents cascading failures in microservices?",
		"How do neural networks process text?",
	}

	for _, q := range queries {
		// Embed the query with the search_query hint for retrieval-optimised vectors.
		qResp, err := client.Embed(ctx, &deebus.EmbedRequest{
			Input:     []string{q},
			InputType: "search_query",
		})
		if err != nil {
			log.Fatalf("embed query: %v", err)
		}

		results := rankBySimilarity(qResp.Embeddings[0], docResp.Embeddings, corpus)

		fmt.Printf("Query: %q\n", q)
		for i, r := range results[:3] {
			fmt.Printf("  %d. [%.3f] %s\n", i+1, r.score, r.text)
		}
		fmt.Println()
	}
}

// ── Dimension inspection ──────────────────────────────────────────────────────

func batchDimensions(ctx context.Context, client *deebus.Client) {
	fmt.Println("── Batch embedding ──────────────────────────────────────────────────")

	sentences := []string{
		"Short sentence.",
		"A slightly longer sentence with more context.",
		"This is a much longer sentence that contains significantly more tokens and information.",
	}

	resp, err := client.Embed(ctx, &deebus.EmbedRequest{Input: sentences})
	if err != nil {
		log.Fatalf("embed batch: %v", err)
	}

	fmt.Printf("Model: %s\n", resp.Model)
	for i, vec := range resp.Embeddings {
		norm := l2Norm(vec)
		fmt.Printf("  [%d] dim=%-5d  l2_norm=%.4f  text=%q\n",
			i, len(vec), norm, truncate(sentences[i], 40))
	}
	fmt.Println()
}

// ── Similarity helpers ────────────────────────────────────────────────────────

type result struct {
	text  string
	score float64
}

func rankBySimilarity(query []float64, docs [][]float64, texts []string) []result {
	results := make([]result, len(docs))
	for i, doc := range docs {
		results[i] = result{text: texts[i], score: cosine(query, doc)}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})
	return results
}

// cosine returns the cosine similarity between two equal-length vectors.
func cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// l2Norm returns the L2 norm (Euclidean length) of a vector.
func l2Norm(v []float64) float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	return math.Sqrt(sum)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("environment variable %s is required", key)
	}
	return v
}
