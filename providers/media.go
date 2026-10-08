package providers

import (
	"context"
	"fmt"
	"net/http"
)

// ImageGenerator creates images from a text prompt.
type ImageGenerator interface {
	GenerateImage(ctx context.Context, req *ImageRequest) (*ImageResponse, error)
}

// ImageEditor edits or extends source images from a prompt.
// The retired images variations endpoint is intentionally not exposed.
type ImageEditor interface {
	EditImage(ctx context.Context, req *ImageEditRequest) (*ImageResponse, error)
}

// SpeechSynthesizer turns text into spoken audio.
type SpeechSynthesizer interface {
	SynthesizeSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error)
}

// Transcriber turns spoken audio into text.
type Transcriber interface {
	Transcribe(ctx context.Context, req *TranscribeRequest) (*TranscribeResponse, error)
}

// Reranker orders documents by relevance to a query.
type Reranker interface {
	Rerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error)
}

// ImageRequest is a provider-neutral image generation request.
type ImageRequest struct {
	Model          string // provider model, or "provider/model" on Client.GenerateImage
	Prompt         string
	N              int    // image count; providers that accept one image ignore values above 1
	Size           string // pixels such as 1024x1024, or a provider aspect token
	Quality        string // provider quality token
	Style          string // provider style token
	ResponseFormat string // "b64_json" or "url" when the provider supports it
	UserID         string // end-user id when the provider accepts one
}

// Image is one generated image.
type Image struct {
	B64JSON       string
	URL           string
	RevisedPrompt string
	MediaType     string
}

// ImageResponse is the result of image generation or editing.
type ImageResponse struct {
	Images   []Image
	Model    string
	Provider string
}

// ImageInput is one source image. Set exactly one of Data, URL, or FileID.
type ImageInput struct {
	Data      []byte
	URL       string
	FileID    string
	MediaType string
	FileName  string
}

// ImageEditRequest edits one or more source images.
// OpenAI accepts bytes, URLs, and file IDs. Gemini requires image bytes.
type ImageEditRequest struct {
	Model          string
	Prompt         string
	Images         []ImageInput
	Mask           *ImageInput // optional edit mask
	N              int
	Size           string
	Quality        string
	Background     string // OpenAI background token
	OutputFormat   string // png, jpeg, or webp when supported
	ResponseFormat string // "b64_json" or "url" when supported
	UserID         string
}

// SpeechTurn is one spoken line. Gemini uses it for multi-speaker audio.
// OpenAI-compatible speech does not accept multiple speakers.
type SpeechTurn struct {
	Speaker string
	Text    string
	Style   string
	Voice   string
}

// SpeechRequest is a provider-neutral text-to-speech request.
// OpenAI defaults to voice alloy and mp3. Gemini defaults to voice Kore and wav.
type SpeechRequest struct {
	Model        string
	Input        string       // spoken text; unused when Speakers is set
	Voice        string       // provider voice name
	Format       string       // mp3, wav, opus, aac, flac, pcm, mulaw, or alaw
	Speed        float64      // OpenAI speed; 0 uses the provider default
	Instructions string       // OpenAI style instructions, or Gemini single-speaker style
	Language     string       // BCP-47 language hint where the provider accepts one
	Speakers     []SpeechTurn // Gemini multi-speaker turns
}

// SpeechResponse contains synthesized audio bytes.
type SpeechResponse struct {
	Audio     []byte
	MediaType string
	Model     string
	Provider  string
}

// TranscribeRequest is a provider-neutral speech-to-text request.
type TranscribeRequest struct {
	Model     string
	Data      []byte // audio bytes
	FileName  string // upload name; empty uses audio.mp3 for OpenAI
	MediaType string // audio MIME type
	Language  string // language hint
	Prompt    string // optional transcription hint
	Format    string // json or text
}

// TranscribeResponse contains the transcript.
type TranscribeResponse struct {
	Text     string
	Model    string
	Provider string
}

// RerankRequest orders documents against a query.
type RerankRequest struct {
	Model     string // Cohere rerank model
	Query     string
	Documents []string
	TopN      int // 0 returns the provider default
}

// RerankResult is one ranked document.
type RerankResult struct {
	Index    int
	Score    float64
	Document string
}

// RerankResponse contains documents ordered by relevance.
type RerankResponse struct {
	Results  []RerankResult
	Model    string
	Provider string
}

// Unsupported reports that a provider cannot perform a capability.
// The error is eligible for fallback and does not count as a bad request
// against the circuit breaker when StatusCode is 400.
func Unsupported(provider, capability string) error {
	return unsupportedCapability(provider, capability)
}

func unsupportedCapability(provider, capability string) *ProviderError {
	return &ProviderError{
		Type:       ErrTypeInvalidReq,
		Provider:   provider,
		StatusCode: http.StatusBadRequest,
		Message:    fmt.Sprintf("%s does not support %s", provider, capability),
		Retryable:  false,
		Fallback:   true,
	}
}
