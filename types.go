package deebus

import "github.com/ncobase/deebus/providers"

// Type aliases — import deebus and get everything you need without touching
// the providers sub-package directly.
type (
	// Provider types.
	Provider        = providers.Provider
	Request         = providers.Request
	Response        = providers.Response
	StreamChunk     = providers.StreamChunk
	EmbedRequest    = providers.EmbedRequest
	EmbedResponse   = providers.EmbedResponse

	// Message types.
	Message         = providers.Message
	ContentBlock    = providers.ContentBlock
	TextContent     = providers.TextContent
	ImageContent    = providers.ImageContent
	ImageSource     = providers.ImageSource
	AudioContent    = providers.AudioContent
	AudioSource     = providers.AudioSource
	DocumentContent = providers.DocumentContent
	DocumentSource  = providers.DocumentSource

	// Tool types.
	Tool           = providers.Tool
	FunctionSchema = providers.FunctionSchema
	ToolCall       = providers.ToolCall
)

// Message constructors — convenience wrappers around the providers package.
var (
	SimpleMessage     = providers.SimpleMessage
	ImageMessage      = providers.ImageMessage
	AudioMessage      = providers.AudioMessage
	DocumentMessage   = providers.DocumentMessage
	AssistantMessage  = providers.AssistantMessage
	ToolResultMessage = providers.ToolResultMessage
)
