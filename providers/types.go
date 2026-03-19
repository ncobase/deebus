package providers

import (
	"context"
	"time"
)

// Provider is the interface that every AI backend must implement.
type Provider interface {
	// Complete performs a single-turn or multi-turn completion.
	Complete(ctx context.Context, req *Request) (*Response, error)

	// Stream performs a streaming completion, returning a channel of chunks.
	// The channel is closed when the stream ends or an error occurs.
	Stream(ctx context.Context, req *Request) (<-chan *StreamChunk, error)

	// Embed generates vector embeddings for the given input strings.
	Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error)

	// Name returns the provider's canonical identifier (e.g. "openai").
	Name() string

	// Health performs a lightweight liveness check.
	Health(ctx context.Context) error
}

// Config is the low-level configuration passed to each provider constructor.
type Config struct {
	APIKey  string
	BaseURL string
	Timeout time.Duration
}

// ContentBlock is the sealed interface for message content types.
type ContentBlock interface {
	contentBlock()
}

// TextContent holds a plain text content block.
type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (TextContent) contentBlock() {}

// ImageContent holds an image content block.
type ImageContent struct {
	Type   string      `json:"type"`
	Source ImageSource `json:"source"`
	Detail string      `json:"detail,omitempty"`
}

func (ImageContent) contentBlock() {}

// ImageSource describes where an image comes from.
type ImageSource struct {
	Type      string `json:"type"`                // "url", "base64", "file_id"
	MediaType string `json:"media_type,omitempty"` // e.g. "image/jpeg"
	Data      string `json:"data,omitempty"`       // base64-encoded bytes
	URL       string `json:"url,omitempty"`
	FileID    string `json:"file_id,omitempty"`
}

// AudioContent holds an audio content block.
type AudioContent struct {
	Type   string      `json:"type"`
	Source AudioSource `json:"source"`
}

func (AudioContent) contentBlock() {}

// AudioSource describes audio data.
type AudioSource struct {
	Type   string `json:"type"`             // "base64"
	Data   string `json:"data,omitempty"`   // base64-encoded bytes
	Format string `json:"format,omitempty"` // "wav", "mp3", "ogg"
}

// DocumentContent holds a PDF or other document content block.
type DocumentContent struct {
	Type   string         `json:"type"`
	Source DocumentSource `json:"source"`
}

func (DocumentContent) contentBlock() {}

// DocumentSource describes document data.
type DocumentSource struct {
	Type      string `json:"type"`                // "base64", "url"
	MediaType string `json:"media_type,omitempty"` // e.g. "application/pdf"
	Data      string `json:"data,omitempty"`       // base64-encoded bytes
	URL       string `json:"url,omitempty"`
}

// Message is a single turn in a conversation.
type Message struct {
	Role    string         `json:"role"`    // "user", "assistant", "system", "tool"
	Content []ContentBlock `json:"content"`
}

// Tool describes a function that the model may call.
type Tool struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

// FunctionSchema describes a callable function.
type FunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict,omitempty"` // OpenAI structured outputs
}

// ToolCall is a model's request to invoke a function.
type ToolCall struct {
	Index int    `json:"index,omitempty"` // position in parallel tool calls (streaming)
	ID    string `json:"id"`
	Type  string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded arguments
	} `json:"function"`
}

// Request is the unified completion/streaming request.
type Request struct {
	Messages    []Message
	Model       string
	MaxTokens   int
	Temperature float64
	Stream      bool
	Tools       []Tool
	ToolChoice  string         // "auto", "none", "required", or specific function name
	Options     map[string]any // provider-specific extras (e.g. Ollama parameters)
}

// Response is the unified non-streaming response.
type Response struct {
	Content      string
	Model        string
	Provider     string
	TokensUsed   int
	FinishReason string
	ToolCalls    []ToolCall
	CreatedAt    time.Time
}

// StreamChunk is one piece of a streaming response.
type StreamChunk struct {
	Content      string
	ToolCalls    []ToolCall // populated in the final Done chunk when tools were called
	Done         bool
	FinishReason string
	TokensUsed   int   // populated in the final Done chunk when the provider reports it
	Error        error
}

// EmbedRequest is the unified embedding request.
type EmbedRequest struct {
	Input     []string
	Model     string
	InputType string // hint for retrieval: "search_query", "search_document", "classification", "clustering"
}

// EmbedResponse is the unified embedding response.
type EmbedResponse struct {
	Embeddings [][]float64
	Model      string
	TokensUsed int
}
