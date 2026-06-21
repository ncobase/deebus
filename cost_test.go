package deebus

import (
	"math"
	"testing"
)

func TestEstimateResponseCostSeparatesCacheAndReasoning(t *testing.T) {
	resp := &Response{
		InputTokens:     1000,
		OutputTokens:    500,
		ReasoningTokens: 100,
		CacheUsage: CacheUsage{
			CreatedTokens: 200,
			ReadTokens:    300,
		},
	}
	cost := EstimateResponseCost(resp, TokenPricing{
		Currency:        "USD",
		InputPer1K:      1.0,
		OutputPer1K:     2.0,
		CacheWritePer1K: 1.25,
		CacheReadPer1K:  0.1,
		ReasoningPer1K:  3.0,
	})

	if cost.RegularInputTokens != 500 {
		t.Fatalf("regular input tokens = %d, want 500", cost.RegularInputTokens)
	}
	if cost.RegularOutputTokens != 400 {
		t.Fatalf("regular output tokens = %d, want 400", cost.RegularOutputTokens)
	}
	want := 0.5 + 0.8 + 0.25 + 0.03 + 0.3
	if math.Abs(cost.TotalCost-want) > 1e-9 {
		t.Fatalf("total cost = %v, want %v (%#v)", cost.TotalCost, want, cost)
	}
}

func TestEstimateEmbeddingCostUsesInputPricing(t *testing.T) {
	cost := EstimateEmbeddingCost(&EmbedResponse{TokensUsed: 2500}, TokenPricing{
		Currency:   "USD",
		InputPer1K: 0.02,
	})
	if math.Abs(cost.TotalCost-0.05) > 1e-9 || cost.InputTokens != 2500 {
		t.Fatalf("embedding cost = %#v, want total 0.05 input 2500", cost)
	}
}
