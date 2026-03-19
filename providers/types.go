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

// ─── Cache control ────────────────────────────────────────────────────────────

// CacheControl enables prompt caching on supported providers (currently Anthropic).
// Set this on content blocks or tools to mark them as cacheable at that boundary.
//
// Anthropic processes cache breakpoints in order: Tools → System → Messages.
// Cache hits are billed at 10% of the normal input-token rate.
// Cache writes are billed at 125% (5-min TTL) or 200% (1-hour TTL).
// Minimum block size: 1024 tokens (Sonnet); 4096 tokens (Opus, Haiku 4.5+).
// Maximum 4 breakpoints per request.
//
// OpenAI caching is fully automatic — no CacheControl field is needed.
type CacheControl struct {
	// Type is the caching strategy. "ephemeral" is the only supported value.
	Type string `json:"type"` // "ephemeral"

	// TTL is the optional cache duration (Anthropic only).
	// "5m" — 5-minute TTL, 1.25× write cost (default when omitted).
	// "1h" — 1-hour TTL, 2× write cost; use for prompts queried frequently
	//         over longer periods (amortises the higher write cost quickly).
	TTL string `json:"ttl,omitempty"` // "5m" | "1h"
}

// CacheUsage reports prompt-cache activity returned by the provider.
// Zero values mean the provider does not support or did not report caching.
type CacheUsage struct {
	// CreatedTokens is the number of tokens written to the cache this request
	// (Anthropic: cache_creation_input_tokens). A cache write is billed at 125%.
	CreatedTokens int

	// ReadTokens is the number of tokens served from the cache (Anthropic:
	// cache_read_input_tokens; OpenAI: prompt_tokens_details.cached_tokens).
	// Cache reads are billed at 10% (Anthropic) or 50% (OpenAI) of normal rate.
	ReadTokens int
}

// ─── Content blocks ───────────────────────────────────────────────────────────

// ContentBlock is the sealed interface for message content types.
type ContentBlock interface {
	contentBlock()
}

// TextContent holds a plain text content block.
type TextContent struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"` // Anthropic prompt caching
}

func (TextContent) contentBlock() {}

// ImageContent holds an image content block.
type ImageContent struct {
	Type         string        `json:"type"`
	Source       ImageSource   `json:"source"`
	Detail       string        `json:"detail,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"` // Anthropic prompt caching
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
	Type         string         `json:"type"`
	Source       DocumentSource `json:"source"`
	CacheControl *CacheControl  `json:"cache_control,omitempty"` // Anthropic prompt caching
}

func (DocumentContent) contentBlock() {}

// DocumentSource describes document data.
type DocumentSource struct {
	Type      string `json:"type"`                // "base64", "url"
	MediaType string `json:"media_type,omitempty"` // e.g. "application/pdf"
	Data      string `json:"data,omitempty"`       // base64-encoded bytes
	URL       string `json:"url,omitempty"`
}

// ─── Message ──────────────────────────────────────────────────────────────────

// Message is a single turn in a conversation.
type Message struct {
	Role    string         `json:"role"`    // "user", "assistant", "system", "tool"
	Content []ContentBlock `json:"content"`

	// ToolCallID is the ID of the tool call this message is responding to.
	// Required when Role == "tool".
	ToolCallID string `json:"tool_call_id,omitempty"`

	// ToolCalls holds tool invocations made by the model.
	// Populated when Role == "assistant" and the model called one or more tools.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// Name is the tool function name for role="tool" messages.
	// Required by Gemini to construct the functionResponse.
	Name string `json:"name,omitempty"`
}

// ─── Tool ─────────────────────────────────────────────────────────────────────

// Tool describes a function that the model may call.
type Tool struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`

	// CacheControl marks this tool definition as a caching boundary (Anthropic only).
	// Place on the last tool in the slice to cache the entire tools array.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
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
	Index    int    `json:"index,omitempty"` // position in parallel tool calls (streaming)
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded arguments
	} `json:"function"`
}

// ─── Request / Response ───────────────────────────────────────────────────────

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

	// UserID is an optional end-user identifier forwarded to providers that support
	// user attribution (Anthropic metadata.user_id, OpenAI user field).
	// Useful for abuse detection and per-user rate limiting on the provider side.
	UserID string
}

// Response is the unified non-streaming response.
type Response struct {
	Content        string
	Model          string
	Provider       string
	InputTokens    int        // total input tokens (including cache-read and cache-write tokens)
	OutputTokens   int        // total output tokens (including reasoning/thinking tokens)
	TokensUsed     int        // InputTokens + OutputTokens
	ReasoningTokens int       // subset of OutputTokens used for internal reasoning (OpenAI o-series: reasoning_tokens; Gemini thinking: thoughtsTokenCount)
	FinishReason   string
	ToolCalls      []ToolCall
	CacheUsage     CacheUsage // non-zero when the provider reports cache activity
	CreatedAt      time.Time
}

// StreamChunk is one piece of a streaming response.
type StreamChunk struct {
	Content         string
	ToolCalls       []ToolCall // populated in the final Done chunk when tools were called
	Done            bool
	FinishReason    string
	InputTokens     int        // populated in the final Done chunk when the provider reports it
	OutputTokens    int        // populated in the final Done chunk when the provider reports it
	TokensUsed      int        // InputTokens + OutputTokens
	ReasoningTokens int        // subset of OutputTokens used for internal reasoning
	CacheUsage      CacheUsage // populated in the final Done chunk when caching is active
	Error           error
}

// ─── Embed ────────────────────────────────────────────────────────────────────

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
