package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIResponsesInputUsesResponsesContentTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		input := body["input"].([]any)
		message := input[0].(map[string]any)
		content := message["content"].([]any)

		text := content[0].(map[string]any)
		if text["type"] != "input_text" || text["text"] != "describe this layout" {
			t.Fatalf("text content = %#v", text)
		}

		image := content[1].(map[string]any)
		if image["type"] != "input_image" || image["file_id"] != "file_img_123" || image["detail"] != "high" {
			t.Fatalf("image content = %#v", image)
		}

		file := content[2].(map[string]any)
		if file["type"] != "input_file" || file["file_url"] != "https://example.com/spec.pdf" {
			t.Fatalf("file content = %#v", file)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"model":  "gpt-4o",
			"output": []map[string]any{
				{"type": "message", "content": []map[string]any{{"type": "output_text", "text": "ok"}}},
			},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
		})
	}))
	defer server.Close()

	provider := NewOpenAI(Config{APIKey: "sk-test", BaseURL: server.URL, APIMode: "responses", Timeout: time.Second})
	_, err := provider.Complete(context.Background(), &Request{
		Model: "gpt-4o",
		Messages: []Message{
			{
				Role: "user",
				Content: []ContentBlock{
					TextContent{Type: "text", Text: "describe this layout"},
					ImageContent{
						Type:   "image",
						Detail: "high",
						Source: ImageSource{Type: "file_id", FileID: "file_img_123"},
					},
					DocumentContent{
						Type:   "document",
						Source: DocumentSource{Type: "url", MediaType: "application/pdf", URL: "https://example.com/spec.pdf"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestAnthropicFormatPreservesDocumentURL(t *testing.T) {
	messages := ConvertToAnthropicFormat([]Message{
		{
			Role: "user",
			Content: []ContentBlock{
				DocumentContent{
					Type:   "document",
					Source: DocumentSource{Type: "url", MediaType: "application/pdf", URL: "https://example.com/spec.pdf"},
				},
			},
		},
	})

	content := messages[0]["content"].([]map[string]any)
	source := content[0]["source"].(map[string]any)
	if source["type"] != "url" || source["url"] != "https://example.com/spec.pdf" || source["media_type"] != "application/pdf" {
		t.Fatalf("document source = %#v", source)
	}
}

func TestAnthropicSystemUsesTextCacheBlocks(t *testing.T) {
	system := BuildAnthropicSystem([]Message{
		{
			Role: "system",
			Content: []ContentBlock{
				TextContent{
					Type:         "text",
					Text:         "Use the brand guide.",
					CacheControl: &CacheControl{Type: "ephemeral"},
				},
			},
		},
	})

	parts, ok := system.([]map[string]any)
	if !ok {
		t.Fatalf("system = %#v, want content blocks", system)
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "Use the brand guide." || parts[0]["cache_control"] == nil {
		t.Fatalf("system text block = %#v", parts[0])
	}
}

func TestGeminiFormatSerializesAudioAndDocuments(t *testing.T) {
	messages := ConvertToGeminiFormat([]Message{
		{
			Role: "user",
			Content: []ContentBlock{
				ImageContent{
					Type:   "image",
					Source: ImageSource{Type: "url", MediaType: "image/png", URL: "https://example.com/preview.png"},
				},
				AudioContent{
					Type:   "audio",
					Source: AudioSource{Type: "base64", Data: "UklGRg==", Format: "audio/wav"},
				},
				DocumentContent{
					Type:   "document",
					Source: DocumentSource{Type: "base64", MediaType: "application/pdf", Data: "JVBERi0="},
				},
				DocumentContent{
					Type:   "document",
					Source: DocumentSource{Type: "url", MediaType: "application/pdf", URL: "https://example.com/spec.pdf"},
				},
			},
		},
	})

	parts := messages[0]["parts"].([]map[string]any)
	imageFile := parts[0]["file_data"].(map[string]any)
	if imageFile["file_uri"] != "https://example.com/preview.png" || imageFile["mime_type"] != "image/png" {
		t.Fatalf("image file_data = %#v", imageFile)
	}

	audioInline := parts[1]["inline_data"].(map[string]any)
	if audioInline["mime_type"] != "audio/wav" || audioInline["data"] != "UklGRg==" {
		t.Fatalf("audio inline_data = %#v", audioInline)
	}

	docInline := parts[2]["inline_data"].(map[string]any)
	if docInline["mime_type"] != "application/pdf" || docInline["data"] != "JVBERi0=" {
		t.Fatalf("document inline_data = %#v", docInline)
	}

	docFile := parts[3]["file_data"].(map[string]any)
	if docFile["file_uri"] != "https://example.com/spec.pdf" || docFile["mime_type"] != "application/pdf" {
		t.Fatalf("document file_data = %#v", docFile)
	}
}
