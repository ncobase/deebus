package providers

import "strings"

func outputTokenLimit(req *Request) int {
	if req.MaxOutputTokens > 0 {
		return req.MaxOutputTokens
	}
	return req.MaxTokens
}

func mergeOptions(body map[string]any, req *Request, skip map[string]bool) {
	for k, v := range req.Options {
		if skip != nil && skip[k] {
			continue
		}
		body[k] = v
	}
}

func applyCommonChatOptions(body map[string]any, req *Request) {
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}
	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}
	if req.Seed != nil {
		body["seed"] = *req.Seed
	}
	if req.Metadata != nil {
		body["metadata"] = req.Metadata
	}
	if req.Store != nil {
		body["store"] = *req.Store
	}
	if req.ParallelToolCalls != nil {
		body["parallel_tool_calls"] = *req.ParallelToolCalls
	}
}

func openAIResponseFormat(format *ResponseFormat) any {
	if format == nil || format.Type == "" || format.Type == "text" {
		return nil
	}
	if format.Type == "json_object" {
		return map[string]any{"type": "json_object"}
	}
	if format.Type == "json_schema" {
		name := format.Name
		if name == "" {
			name = "response"
		}
		return map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":        name,
				"description": format.Description,
				"schema":      format.Schema,
				"strict":      format.Strict,
			},
		}
	}
	return map[string]any{"type": format.Type}
}

func openAIResponsesText(format *ResponseFormat) any {
	if format == nil || format.Type == "" || format.Type == "text" {
		return nil
	}
	if format.Type == "json_object" {
		return map[string]any{"format": map[string]any{"type": "json_object"}}
	}
	if format.Type == "json_schema" {
		name := format.Name
		if name == "" {
			name = "response"
		}
		return map[string]any{"format": map[string]any{
			"type":        "json_schema",
			"name":        name,
			"description": format.Description,
			"schema":      format.Schema,
			"strict":      format.Strict,
		}}
	}
	return map[string]any{"format": map[string]any{"type": format.Type}}
}

func geminiResponseFormat(format *ResponseFormat) map[string]any {
	if format == nil || format.Type == "" || format.Type == "text" {
		return nil
	}
	out := map[string]any{"responseMimeType": "application/json"}
	if format.Type == "json_schema" && len(format.Schema) > 0 {
		out["responseSchema"] = format.Schema
	}
	return out
}

func ollamaFormat(format *ResponseFormat) any {
	if format == nil || format.Type == "" || format.Type == "text" {
		return nil
	}
	if format.Type == "json_object" {
		return "json"
	}
	if format.Type == "json_schema" && len(format.Schema) > 0 {
		return format.Schema
	}
	return nil
}

func reasoningMap(reasoning *ReasoningConfig) map[string]any {
	if reasoning == nil {
		return nil
	}
	out := map[string]any{}
	if reasoning.Effort != "" {
		out["effort"] = reasoning.Effort
	}
	if reasoning.BudgetTokens > 0 {
		out["budget_tokens"] = reasoning.BudgetTokens
	}
	if reasoning.IncludeThoughts {
		out["include_thoughts"] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func anthropicThinking(reasoning *ReasoningConfig) map[string]any {
	if reasoning == nil {
		return nil
	}
	out := map[string]any{"type": "enabled"}
	if reasoning.BudgetTokens > 0 {
		out["budget_tokens"] = reasoning.BudgetTokens
	}
	return out
}

func geminiThinkingConfig(reasoning *ReasoningConfig) map[string]any {
	if reasoning == nil {
		return nil
	}
	out := map[string]any{}
	if reasoning.BudgetTokens > 0 {
		out["thinkingBudget"] = reasoning.BudgetTokens
	}
	if reasoning.IncludeThoughts {
		out["includeThoughts"] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func boolString(v *bool) string {
	if v == nil {
		return ""
	}
	if *v {
		return "true"
	}
	return "false"
}

func normalizeAPIMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "chat_completions"
	}
	return mode
}
