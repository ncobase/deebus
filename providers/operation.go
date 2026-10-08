package providers

import (
	"context"
	"fmt"
	"strings"
)

// OperationKind selects which official long-running product to call.
type OperationKind string

const (
	OperationImage OperationKind = "image"
	OperationVideo OperationKind = "video"
)

// OperationStatus is the normalized lifecycle of a provider task.
type OperationStatus string

const (
	OperationQueued    OperationStatus = "queued"
	OperationRunning   OperationStatus = "running"
	OperationSucceeded OperationStatus = "succeeded"
	OperationFailed    OperationStatus = "failed"
	OperationCancelled OperationStatus = "cancelled"
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
type OperationRequest struct {
	Model           string
	Kind            OperationKind
	Prompt          string
	NegativePrompt  string
	Size            string
	AspectRatio     string
	Resolution      string
	DurationSeconds int
	N               int
	Image           *ImageInput
}

// Operation is one provider task.
type Operation struct {
	ID       string
	Provider string
	Model    string
	Kind     OperationKind
	Status   OperationStatus
	Progress int
	Error    string
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
type MediaAsset struct {
	URL       string
	MediaType string
}

// OperationAsset is the bytes of a finished media result.
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
