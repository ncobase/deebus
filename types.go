package deebus

import "github.com/ncobase/deebus/providers"

// Type aliases expose provider types through the root package.
type (
	// Provider types.
	Provider           = providers.Provider
	Credentials        = providers.Credentials
	CredentialProvider = providers.CredentialProvider
	Request            = providers.Request
	Response           = providers.Response
	StreamChunk        = providers.StreamChunk
	EmbedRequest       = providers.EmbedRequest
	EmbedResponse      = providers.EmbedResponse

	// Cache types.
	CacheControl       = providers.CacheControl
	CacheUsage         = providers.CacheUsage
	CacheOptions       = providers.CacheOptions
	Cache              = providers.Cache
	CacheUsageMetadata = providers.CacheUsageMetadata
	CreateCacheRequest = providers.CreateCacheRequest
	UpdateCacheRequest = providers.UpdateCacheRequest
	ListCachesRequest  = providers.ListCachesRequest
	ListCachesResponse = providers.ListCachesResponse

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
	Tool            = providers.Tool
	FunctionSchema  = providers.FunctionSchema
	ToolCall        = providers.ToolCall
	ResponseFormat  = providers.ResponseFormat
	ReasoningConfig = providers.ReasoningConfig
)

// Message constructors expose providers helpers through the root package.
var (
	TextMessage       = providers.TextMessage
	ImageMessage      = providers.ImageMessage
	AudioMessage      = providers.AudioMessage
	DocumentMessage   = providers.DocumentMessage
	AssistantMessage  = providers.AssistantMessage
	ToolResultMessage = providers.ToolResultMessage
)
