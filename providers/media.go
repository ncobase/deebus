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
	Model          string
	Prompt         string
	N              int
	Size           string
	Quality        string
	Style          string
	ResponseFormat string // "b64_json" or "url" when the provider supports it
	UserID         string
}

// Image is one generated image.
type Image struct {
	B64JSON       string
	URL           string
	RevisedPrompt string
	MediaType     string
}

// ImageResponse is the result of image generation.
type ImageResponse struct {
	Images   []Image
	Model    string
	Provider string
}

// SpeechRequest is a provider-neutral text-to-speech request.
type SpeechRequest struct {
	Model  string
	Input  string
	Voice  string
	Format string // mp3, wav, opus, aac, flac, pcm
	Speed  float64
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
	Data      []byte
	FileName  string
	MediaType string
	Language  string
	Prompt    string
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
	Model     string
	Query     string
	Documents []string
	TopN      int
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
