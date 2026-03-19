package providers

import "encoding/json"

// SimpleMessage creates a single-text-block message.
func SimpleMessage(role, text string) Message {
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

// ConvertToolsToAnthropic converts the unified Tool slice to Anthropic's format.
// Anthropic uses "input_schema" instead of "parameters" and does not wrap
// tools in a "function" envelope.
func ConvertToolsToAnthropic(tools []Tool) []map[string]any {
	result := make([]map[string]any, len(tools))
	for i, t := range tools {
		result[i] = map[string]any{
			"name":         t.Function.Name,
			"description":  t.Function.Description,
			"input_schema": t.Function.Parameters,
		}
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
// "auto" → AUTO (default), "none" → NONE, "required" → ANY.
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

// ConvertToOpenAIFormat converts messages to the OpenAI chat-completions format.
func ConvertToOpenAIFormat(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		// Tool result messages use a flat string content
		if msg.Role == "tool" {
			out = append(out, map[string]any{
				"role":    "tool",
				"content": ExtractText(msg.Content),
			})
			continue
		}

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
		out = append(out, map[string]any{"role": msg.Role, "content": parts})
	}
	return out
}

// ConvertToAnthropicFormat converts non-system messages to the Anthropic
// Messages API format. System messages must be extracted separately via
// ExtractSystemMessage.
func ConvertToAnthropicFormat(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "system" {
			continue // handled by caller via ExtractSystemMessage
		}
		parts := make([]map[string]any, 0, len(msg.Content))
		for _, block := range msg.Content {
			switch b := block.(type) {
			case TextContent:
				parts = append(parts, map[string]any{"type": "text", "text": b.Text})
			case ImageContent:
				src := map[string]any{"type": b.Source.Type}
				switch b.Source.Type {
				case "base64":
					src["media_type"] = b.Source.MediaType
					src["data"] = b.Source.Data
				case "url":
					src["url"] = b.Source.URL
				}
				parts = append(parts, map[string]any{"type": "image", "source": src})
			case DocumentContent:
				parts = append(parts, map[string]any{
					"type": "document",
					"source": map[string]any{
						"type":       b.Source.Type,
						"media_type": b.Source.MediaType,
						"data":       b.Source.Data,
					},
				})
			}
		}
		out = append(out, map[string]any{"role": msg.Role, "content": parts})
	}
	return out
}

// ConvertToGeminiFormat converts non-system messages to the Gemini
// generateContent format. System messages must be handled via systemInstruction.
func ConvertToGeminiFormat(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "system" {
			continue // handled by caller via systemInstruction
		}
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
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
		out = append(out, map[string]any{"role": role, "parts": parts})
	}
	return out
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
	return json.Marshal(struct {
		Role    string           `json:"role"`
		Content []map[string]any `json:"content"`
	}{Role: m.Role, Content: parts})
}
