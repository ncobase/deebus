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
	ImageRequest       = providers.ImageRequest
	Image              = providers.Image
	ImageInput         = providers.ImageInput
	ImageEditRequest   = providers.ImageEditRequest
	ImageResponse      = providers.ImageResponse
	SpeechTurn         = providers.SpeechTurn
	SpeechRequest      = providers.SpeechRequest
	SpeechResponse     = providers.SpeechResponse
	TranscribeRequest  = providers.TranscribeRequest
	TranscribeResponse = providers.TranscribeResponse
	RerankRequest      = providers.RerankRequest
	RerankResult       = providers.RerankResult
	RerankResponse     = providers.RerankResponse
	TokenCount         = providers.TokenCount
	OperationKind      = providers.OperationKind
	OperationStatus    = providers.OperationStatus
	OperationRequest   = providers.OperationRequest
	Operation          = providers.Operation
	OperationResult    = providers.OperationResult
	OperationAsset     = providers.OperationAsset
	MediaAsset         = providers.MediaAsset
	BatchItem          = providers.BatchItem
	BatchRequest       = providers.BatchRequest
	Batch              = providers.Batch
	BatchResult        = providers.BatchResult
	ProviderFactory    = providers.Factory

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
	TextMessage        = providers.TextMessage
	ImageMessage       = providers.ImageMessage
	AudioMessage       = providers.AudioMessage
	DocumentMessage    = providers.DocumentMessage
	AssistantMessage   = providers.AssistantMessage
	ToolResultMessage  = providers.ToolResultMessage
	RegisterProvider   = providers.Register
	OperationImage     = providers.OperationImage
	OperationVideo     = providers.OperationVideo
	OperationQueued    = providers.OperationQueued
	OperationRunning   = providers.OperationRunning
	OperationSucceeded = providers.OperationSucceeded
	OperationFailed    = providers.OperationFailed
	OperationCancelled = providers.OperationCancelled
)
