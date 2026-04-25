package providers

import "encoding/json"

// TextMessage creates a single-text-block message.
func TextMessage(role, text string) Message {
	return Message{
		Role:    role,
		Content: []ContentBlock{TextContent{Type: "text", Text: text}},
	}
}

// ImageMessage creates a message containing an image and optional text.
func ImageMessage(role string, src ImageSource, text string) Message {
	content := []ContentBlock{ImageContent{Type: "image", Source: src}}
	if text != "" {
		content = append(content, TextContent{Type: "text", Text: text})
	}
	return Message{Role: role, Content: content}
}

// AudioMessage creates a message containing base64-encoded audio.
func AudioMessage(role, audioData, format string) Message {
	return Message{
		Role: role,
		Content: []ContentBlock{
			AudioContent{
				Type:   "audio",
				Source: AudioSource{Type: "base64", Data: audioData, Format: format},
			},
		},
	}
}

// DocumentMessage creates a message containing a base64-encoded document (e.g. PDF).
func DocumentMessage(role, docData, mediaType string) Message {
	return Message{
		Role: role,
		Content: []ContentBlock{
			DocumentContent{
				Type:   "document",
				Source: DocumentSource{Type: "base64", MediaType: mediaType, Data: docData},
			},
		},
	}
}

// AssistantMessage creates an assistant message, optionally including tool calls.
// Use this when building conversation history after the model has called tools.
func AssistantMessage(content string, toolCalls []ToolCall) Message {
	var blocks []ContentBlock
	if content != "" {
		blocks = []ContentBlock{TextContent{Type: "text", Text: content}}
	}
	return Message{
		Role:      "assistant",
		Content:   blocks,
		ToolCalls: toolCalls,
	}
}

// ToolResultMessage creates a tool result message for multi-turn tool calling.
// - toolCallID: the ID from the ToolCall that was executed
// - name:       the function name (required by Gemini for functionResponse)
// - result:     the tool's output, either plain text or JSON
func ToolResultMessage(toolCallID, name, result string) Message {
	return Message{
		Role:       "tool",
		Content:    []ContentBlock{TextContent{Type: "text", Text: result}},
		ToolCallID: toolCallID,
		Name:       name,
	}
}

// ExtractText returns the text from the first TextContent block, or "".
func ExtractText(content []ContentBlock) string {
	for _, b := range content {
		if tc, ok := b.(TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// ExtractSystemMessage splits system-role messages out of the conversation.
// Returns the concatenated system text and the remaining messages.
// Most providers (Anthropic, Gemini) require the system prompt as a separate
// top-level field rather than a message in the array.
func ExtractSystemMessage(messages []Message) (system string, rest []Message) {
	for _, msg := range messages {
		if msg.Role == "system" {
			if system != "" {
				system += "\n"
			}
			system += ExtractText(msg.Content)
		} else {
			rest = append(rest, msg)
		}
	}
	return
}

// BuildAnthropicSystem returns the value for the Anthropic "system" request
// field. When any system message content block carries a CacheControl marker,
// the field is serialised as an array of content blocks (required by the API).
// Otherwise it falls back to a plain concatenated string.
func BuildAnthropicSystem(messages []Message) any {
	var sysBlocks []ContentBlock
	for _, msg := range messages {
		if msg.Role == "system" {
			sysBlocks = append(sysBlocks, msg.Content...)
		}
	}
	if len(sysBlocks) == 0 {
		return nil
	}

	// Check for any cache_control marker.
	needsBlocks := false
	for _, b := range sysBlocks {
		if tc, ok := b.(TextContent); ok && tc.CacheControl != nil {
			needsBlocks = true
			break
		}
		if dc, ok := b.(DocumentContent); ok && dc.CacheControl != nil {
			needsBlocks = true
			break
		}
	}

	if !needsBlocks {
		// Plain string - backward-compatible path.
		var sb string
		for _, b := range sysBlocks {
			if tc, ok := b.(TextContent); ok {
				if sb != "" {
					sb += "\n"
				}
				sb += tc.Text
			}
		}
		return sb
	}

	// Array of content blocks - required when cache_control is present.
	parts := make([]map[string]any, 0, len(sysBlocks))
	for _, b := range sysBlocks {
		switch tc := b.(type) {
		case TextContent:
			block := map[string]any{"type": "text", "text": tc.Text}
			if tc.CacheControl != nil {
				block["cache_control"] = tc.CacheControl
			}
			parts = append(parts, block)
		case DocumentContent:
			block := map[string]any{
				"type": "document",
				"source": map[string]any{
					"type":       tc.Source.Type,
					"media_type": tc.Source.MediaType,
					"data":       tc.Source.Data,
				},
			}
			if tc.CacheControl != nil {
				block["cache_control"] = tc.CacheControl
			}
			parts = append(parts, block)
		}
	}
	return parts
}

// ConvertToolsToAnthropic converts the unified Tool slice to Anthropic's format.
// Anthropic uses "input_schema" instead of "parameters" and does not wrap
// tools in a "function" envelope.
// If a tool carries a CacheControl marker it is included, enabling the caller
// to cache the tools array at a chosen boundary (typically the last tool).
func ConvertToolsToAnthropic(tools []Tool) []map[string]any {
	result := make([]map[string]any, len(tools))
	for i, t := range tools {
		m := map[string]any{
			"name":         t.Function.Name,
			"description":  t.Function.Description,
			"input_schema": t.Function.Parameters,
		}
		if t.Function.EagerInputStreaming {
			m["eager_input_streaming"] = true
		}
		if t.CacheControl != nil {
			m["cache_control"] = t.CacheControl
		}
		result[i] = m
	}
	return result
}

// ConvertToolsToGemini converts the unified Tool slice to Gemini's
// functionDeclarations format, wrapped in the tools array.
func ConvertToolsToGemini(tools []Tool) []map[string]any {
	decls := make([]map[string]any, len(tools))
	for i, t := range tools {
		decls[i] = map[string]any{
			"name":        t.Function.Name,
			"description": t.Function.Description,
			"parameters":  t.Function.Parameters,
		}
	}
	return []map[string]any{{"functionDeclarations": decls}}
}

// GeminiToolConfig converts a ToolChoice string to Gemini's toolConfig object.
// "auto" -> AUTO (default), "none" -> NONE, "required" -> ANY.
func GeminiToolConfig(choice string) map[string]any {
	mode := "AUTO"
	switch choice {
	case "none":
		mode = "NONE"
	case "required":
		mode = "ANY"
	}
	return map[string]any{
		"functionCallingConfig": map[string]any{"mode": mode},
	}
}

// AnthropicToolChoice converts a ToolChoice string to Anthropic's tool_choice object.
func AnthropicToolChoice(choice string) map[string]any {
	switch choice {
	case "none":
		return map[string]any{"type": "none"}
	case "required":
		return map[string]any{"type": "any"}
	case "auto", "":
		return map[string]any{"type": "auto"}
	default:
		// Specific function name
		return map[string]any{"type": "tool", "name": choice}
	}
}

// ConvertToOpenAIFormat converts messages to the OpenAI chat-completions format,
// including tool result messages (role="tool") and assistant messages with tool_calls.
func ConvertToOpenAIFormat(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "tool":
			// Tool result: flat string content with tool_call_id reference.
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": msg.ToolCallID,
				"content":      ExtractText(msg.Content),
			})

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				// Assistant message with tool calls - content may be nil/empty.
				m := map[string]any{
					"role":       "assistant",
					"tool_calls": msg.ToolCalls,
				}
				if text := ExtractText(msg.Content); text != "" {
					m["content"] = text
				} else {
					m["content"] = nil // OpenAI expects null when content is absent
				}
				out = append(out, m)
			} else {
				out = append(out, openAIContentMessage(msg))
			}

		default:
			out = append(out, openAIContentMessage(msg))
		}
	}
	return out
}

// openAIContentMessage serialises a regular message's content blocks.
func openAIContentMessage(msg Message) map[string]any {
	parts := make([]map[string]any, 0, len(msg.Content))
	for _, block := range msg.Content {
		switch b := block.(type) {
		case TextContent:
			parts = append(parts, map[string]any{"type": "text", "text": b.Text})
		case ImageContent:
			imgBlock := map[string]any{"type": "image_url"}
			switch b.Source.Type {
			case "url":
				iu := map[string]any{"url": b.Source.URL}
				if b.Detail != "" {
					iu["detail"] = b.Detail
				}
				imgBlock["image_url"] = iu
			case "base64":
				dataURL := "data:" + b.Source.MediaType + ";base64," + b.Source.Data
				imgBlock["image_url"] = map[string]any{"url": dataURL}
			}
			parts = append(parts, imgBlock)
		case AudioContent:
			parts = append(parts, map[string]any{
				"type": "input_audio",
				"input_audio": map[string]any{
					"data":   b.Source.Data,
					"format": b.Source.Format,
				},
			})
		}
	}
	return map[string]any{"role": msg.Role, "content": parts}
}

// ConvertToAnthropicFormat converts non-system messages to the Anthropic Messages
// API format, including tool result and assistant+tool_calls messages.
// System messages must be extracted separately via ExtractSystemMessage.
func ConvertToAnthropicFormat(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			continue // handled by caller

		case "tool":
			// Anthropic wraps tool results inside a user turn.
			toolBlock := map[string]any{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     ExtractText(msg.Content),
			}
			// Propagate cache_control from the first content block, if set.
			if len(msg.Content) > 0 {
				if tc, ok := msg.Content[0].(TextContent); ok && tc.CacheControl != nil {
					toolBlock["cache_control"] = tc.CacheControl
				}
			}
			out = append(out, map[string]any{
				"role":    "user",
				"content": []map[string]any{toolBlock},
			})

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				// Build content array: optional text block + tool_use blocks.
				parts := []map[string]any{}
				if text := ExtractText(msg.Content); text != "" {
					parts = append(parts, map[string]any{"type": "text", "text": text})
				}
				for _, tc := range msg.ToolCalls {
					// input must be a JSON object, not a string.
					var input any
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
						input = map[string]any{} // fallback to empty object
					}
					parts = append(parts, map[string]any{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Function.Name,
						"input": input,
					})
				}
				out = append(out, map[string]any{"role": "assistant", "content": parts})
			} else {
				out = append(out, anthropicContentMessage(msg))
			}

		default:
			out = append(out, anthropicContentMessage(msg))
		}
	}
	return out
}

// anthropicContentMessage serialises a regular message's content blocks,
// including any cache_control markers for prompt caching.
func anthropicContentMessage(msg Message) map[string]any {
	parts := make([]map[string]any, 0, len(msg.Content))
	for _, block := range msg.Content {
		switch b := block.(type) {
		case TextContent:
			m := map[string]any{"type": "text", "text": b.Text}
			if b.CacheControl != nil {
				m["cache_control"] = b.CacheControl
			}
			parts = append(parts, m)
		case ImageContent:
			src := map[string]any{"type": b.Source.Type}
			switch b.Source.Type {
			case "base64":
				src["media_type"] = b.Source.MediaType
				src["data"] = b.Source.Data
			case "url":
				src["url"] = b.Source.URL
			}
			m := map[string]any{"type": "image", "source": src}
			if b.CacheControl != nil {
				m["cache_control"] = b.CacheControl
			}
			parts = append(parts, m)
		case DocumentContent:
			m := map[string]any{
				"type": "document",
				"source": map[string]any{
					"type":       b.Source.Type,
					"media_type": b.Source.MediaType,
					"data":       b.Source.Data,
				},
			}
			if b.CacheControl != nil {
				m["cache_control"] = b.CacheControl
			}
			parts = append(parts, m)
		}
	}
	return map[string]any{"role": msg.Role, "content": parts}
}

// ConvertToGeminiFormat converts non-system messages to the Gemini
// generateContent format, including functionResponse and functionCall parts.
// System messages must be handled separately via systemInstruction.
func ConvertToGeminiFormat(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			continue // handled by caller

		case "tool":
			// Gemini expects functionResponse as a user turn.
			var response any
			text := ExtractText(msg.Content)
			if err := json.Unmarshal([]byte(text), &response); err != nil {
				// Non-JSON result: wrap in a response object.
				response = map[string]any{"result": text}
			}
			out = append(out, map[string]any{
				"role": "user",
				"parts": []map[string]any{
					{
						"functionResponse": map[string]any{
							"name":     msg.Name,
							"response": response,
						},
					},
				},
			})

		case "assistant":
			role := "model"
			if len(msg.ToolCalls) > 0 {
				// Build parts: optional text + functionCall parts.
				parts := []map[string]any{}
				if text := ExtractText(msg.Content); text != "" {
					parts = append(parts, map[string]any{"text": text})
				}
				for _, tc := range msg.ToolCalls {
					var args any
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
						args = map[string]any{}
					}
					parts = append(parts, map[string]any{
						"functionCall": map[string]any{
							"name": tc.Function.Name,
							"args": args,
						},
					})
				}
				out = append(out, map[string]any{"role": role, "parts": parts})
			} else {
				out = append(out, geminiContentMessage(msg, role))
			}

		default:
			out = append(out, geminiContentMessage(msg, msg.Role))
		}
	}
	return out
}

// geminiContentMessage serialises a regular message's content blocks.
func geminiContentMessage(msg Message, role string) map[string]any {
	parts := make([]map[string]any, 0, len(msg.Content))
	for _, block := range msg.Content {
		switch b := block.(type) {
		case TextContent:
			parts = append(parts, map[string]any{"text": b.Text})
		case ImageContent:
			if b.Source.Type == "base64" {
				parts = append(parts, map[string]any{
					"inline_data": map[string]any{
						"mime_type": b.Source.MediaType,
						"data":      b.Source.Data,
					},
				})
			} else if b.Source.Type == "url" {
				parts = append(parts, map[string]any{
					"file_data": map[string]any{"file_uri": b.Source.URL},
				})
			}
		}
	}
	return map[string]any{"role": role, "parts": parts}
}

// MarshalJSON implements json.Marshaler for Message, handling the ContentBlock
// interface so that messages can be serialised for logging or storage.
func (m Message) MarshalJSON() ([]byte, error) {
	parts := make([]map[string]any, 0, len(m.Content))
	for _, block := range m.Content {
		switch b := block.(type) {
		case TextContent:
			parts = append(parts, map[string]any{"type": "text", "text": b.Text})
		case ImageContent:
			item := map[string]any{"type": "image", "source": b.Source}
			if b.Detail != "" {
				item["detail"] = b.Detail
			}
			parts = append(parts, item)
		case AudioContent:
			parts = append(parts, map[string]any{"type": "audio", "source": b.Source})
		case DocumentContent:
			parts = append(parts, map[string]any{"type": "document", "source": b.Source})
		}
	}
	type wire struct {
		Role       string           `json:"role"`
		Content    []map[string]any `json:"content"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
		ToolCalls  []ToolCall       `json:"tool_calls,omitempty"`
		Name       string           `json:"name,omitempty"`
	}
	return json.Marshal(wire{
		Role:       m.Role,
		Content:    parts,
		ToolCallID: m.ToolCallID,
		ToolCalls:  m.ToolCalls,
		Name:       m.Name,
	})
}
