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

// ConvertToOpenAIFormat converts messages to the OpenAI chat-completions format.
func ConvertToOpenAIFormat(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
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

// ConvertToAnthropicFormat converts messages to the Anthropic Messages API format.
func ConvertToAnthropicFormat(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
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

// ConvertToGeminiFormat converts messages to the Gemini generateContent format.
func ConvertToGeminiFormat(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
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
						"file_data": map[string]any{
							"file_uri": b.Source.URL,
						},
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
