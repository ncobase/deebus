package deebus

// TokenPricing describes caller-supplied model pricing. The library does not
// ship hard-coded prices because provider pricing changes over time.
type TokenPricing struct {
	Currency string

	InputPer1K  float64
	OutputPer1K float64

	// CacheWritePer1K and CacheReadPer1K apply to cache-created/read tokens.
	// Leave zero to charge those tokens at InputPer1K.
	CacheWritePer1K float64
	CacheReadPer1K  float64

	// ReasoningPer1K applies to reasoning output tokens when a provider prices
	// them separately. Leave zero to charge reasoning as regular output.
	ReasoningPer1K float64
}

// CostBreakdown is a deterministic estimate based on reported usage and a
// caller-owned pricing table.
type CostBreakdown struct {
	Currency string

	InputTokens        int
	OutputTokens       int
	CacheCreatedTokens int
	CacheReadTokens    int
	ReasoningTokens    int

	RegularInputTokens  int
	RegularOutputTokens int

	InputCost      float64
	OutputCost     float64
	CacheWriteCost float64
	CacheReadCost  float64
	ReasoningCost  float64
	TotalCost      float64
}

// EstimateResponseCost estimates a non-streaming completion response cost.
func EstimateResponseCost(resp *Response, pricing TokenPricing) CostBreakdown {
	if resp == nil {
		return estimateCost(0, 0, 0, CacheUsage{}, pricing)
	}
	return estimateCost(resp.InputTokens, resp.OutputTokens, resp.ReasoningTokens, resp.CacheUsage, pricing)
}

// EstimateStreamCost estimates an accumulated streaming response cost.
func EstimateStreamCost(result StreamResult, pricing TokenPricing) CostBreakdown {
	return estimateCost(result.InputTokens, result.OutputTokens, result.ReasoningTokens, result.CacheUsage, pricing)
}

// EstimateEmbeddingCost estimates embedding cost from reported token usage.
func EstimateEmbeddingCost(resp *EmbedResponse, pricing TokenPricing) CostBreakdown {
	if resp == nil {
		return estimateCost(0, 0, 0, CacheUsage{}, pricing)
	}
	return estimateCost(resp.TokensUsed, 0, 0, CacheUsage{}, pricing)
}

func estimateCost(inputTokens, outputTokens, reasoningTokens int, cacheUsage CacheUsage, pricing TokenPricing) CostBreakdown {
	cacheCreated := clampTokenCount(cacheUsage.CreatedTokens)
	cacheRead := clampTokenCount(cacheUsage.ReadTokens)
	inputTokens = clampTokenCount(inputTokens)
	outputTokens = clampTokenCount(outputTokens)
	reasoningTokens = clampTokenCount(reasoningTokens)

	regularInput := inputTokens - cacheCreated - cacheRead
	if regularInput < 0 {
		regularInput = 0
	}
	regularOutput := outputTokens
	if pricing.ReasoningPer1K > 0 {
		regularOutput -= reasoningTokens
		if regularOutput < 0 {
			regularOutput = 0
		}
	}

	cacheWriteRate := pricing.CacheWritePer1K
	if cacheWriteRate == 0 {
		cacheWriteRate = pricing.InputPer1K
	}
	cacheReadRate := pricing.CacheReadPer1K
	if cacheReadRate == 0 {
		cacheReadRate = pricing.InputPer1K
	}

	breakdown := CostBreakdown{
		Currency:            pricing.Currency,
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
		CacheCreatedTokens:  cacheCreated,
		CacheReadTokens:     cacheRead,
		ReasoningTokens:     reasoningTokens,
		RegularInputTokens:  regularInput,
		RegularOutputTokens: regularOutput,
		InputCost:           tokensCost(regularInput, pricing.InputPer1K),
		OutputCost:          tokensCost(regularOutput, pricing.OutputPer1K),
		CacheWriteCost:      tokensCost(cacheCreated, cacheWriteRate),
		CacheReadCost:       tokensCost(cacheRead, cacheReadRate),
	}
	if pricing.ReasoningPer1K > 0 {
		breakdown.ReasoningCost = tokensCost(reasoningTokens, pricing.ReasoningPer1K)
	}
	breakdown.TotalCost = breakdown.InputCost +
		breakdown.OutputCost +
		breakdown.CacheWriteCost +
		breakdown.CacheReadCost +
		breakdown.ReasoningCost

	return breakdown
}

func tokensCost(tokens int, per1K float64) float64 {
	if tokens <= 0 || per1K == 0 {
		return 0
	}
	return float64(tokens) / 1000 * per1K
}

func clampTokenCount(tokens int) int {
	if tokens < 0 {
		return 0
	}
	return tokens
}
