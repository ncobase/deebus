package providers

import (
	"context"
	"fmt"
	"strings"
)

// OperationKind selects which official long-running product to call.
type OperationKind string

const (
	// OperationImage is an asynchronous image task, such as DashScope Wan or Jimeng.
	OperationImage OperationKind = "image"
	// OperationVideo is an asynchronous video task.
	OperationVideo OperationKind = "video"
)

// OperationStatus is the normalized lifecycle of a provider task.
type OperationStatus string

const (
	OperationQueued    OperationStatus = "queued"    // accepted, not started
	OperationRunning   OperationStatus = "running"   // provider is working
	OperationSucceeded OperationStatus = "succeeded" // result is available
	OperationFailed    OperationStatus = "failed"    // provider reported failure
	OperationCancelled OperationStatus = "cancelled" // provider reported cancellation
)

// OperationClient submits and inspects provider long-running tasks.
// The caller stores the ID and decides when to poll. The library does not
// queue or persist the task.
type OperationClient interface {
	SubmitOperation(ctx context.Context, req *OperationRequest) (*Operation, error)
	GetOperation(ctx context.Context, id string) (*Operation, error)
	CancelOperation(ctx context.Context, id string) error
	ReadOperation(ctx context.Context, id string) (*OperationAsset, error)
}

// OperationRequest is a provider-neutral long-running generation request.
// Model is "provider/model" on Client.Submit, or the vendor model on a direct provider call.
// For Jimeng video, Model is the official req_key.
type OperationRequest struct {
	Model           string
	Kind            OperationKind
	Prompt          string
	NegativePrompt  string      // DashScope video
	Size            string      // pixels, or DashScope width*height after normalization
	AspectRatio     string      // provider ratio such as 16:9
	Resolution      string      // provider resolution token such as 720P
	DurationSeconds int         // video length in seconds
	N               int         // image count where the provider accepts it
	Image           *ImageInput // reference image; URL or bytes according to the provider
}

// Operation is one provider task.
// ID is the value passed back to GetOperation. Kling IDs are "text2video/{task}",
// "image2video/{task}", or "images/{task}". Jimeng IDs are "{req_key}/{task}".
type Operation struct {
	ID       string
	Provider string
	Model    string
	Kind     OperationKind
	Status   OperationStatus
	Progress int    // 0 when the provider does not report progress
	Error    string // provider message when Status is failed
	Result   *OperationResult
}

// OperationResult holds the official output locations for a finished task.
type OperationResult struct {
	Images []Image
	Videos []MediaAsset
	Audio  []MediaAsset
	Text   string
}

// MediaAsset is a downloadable video or audio result.
// URL may be a temporary provider or CDN address. ReadOperation attaches
// credentials only when URL is on the provider host.
type MediaAsset struct {
	URL       string
	MediaType string
}

// OperationAsset is the bytes of a finished media result, capped at 64 MiB.
type OperationAsset struct {
	Data      []byte
	MediaType string
}

const maxOperationAssetBytes = 64 << 20

func validOperationID(id string) error {
	if strings.TrimSpace(id) == "" || len(id) > 512 || strings.Contains(id, "..") || strings.ContainsAny(id, " \r\n\x00") {
		return fmt.Errorf("invalid operation id")
	}
	return nil
}

func unsupportedTargeted(provider, capability string) error {
	return &ProviderError{
		Type:       ErrTypeInvalidReq,
		Provider:   provider,
		StatusCode: 400,
		Message:    fmt.Sprintf("%s does not support %s", provider, capability),
		Retryable:  false,
		Fallback:   false,
	}
}
