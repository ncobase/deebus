package deebus

import (
	"context"
	"fmt"
	"strings"

	"github.com/ncobase/deebus/providers"
)

// Submit starts an official long-running image or video task.
// The returned ID is provider-specific. Store it and poll with GetOperation.
func (c *Client) Submit(ctx context.Context, req *OperationRequest) (*Operation, error) {
	if req == nil || strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("operation prompt required")
	}
	if req.Kind != OperationImage && req.Kind != OperationVideo {
		return nil, fmt.Errorf("operation kind must be image or video")
	}
	limits := c.config.RequestPolicy.Limits
	if err := c.rejectMediaLimits(limits.rejectText("operation prompt", len(req.Prompt)+len(req.NegativePrompt))); err != nil {
		return nil, err
	}
	if req.Image != nil {
		if err := c.rejectMediaLimits(limits.rejectMedia("operation image", len(req.Image.Data))); err != nil {
			return nil, err
		}
	}
	attempt := *req
	if req.Image != nil {
		image := *req.Image
		attempt.Image = &image
	}
	return dispatch(c, ctx, req.Model, func(p providers.Provider, model string) (*Operation, error) {
		client, ok := p.(providers.OperationClient)
		if !ok {
			return nil, providers.Unsupported(p.Name(), "long-running operations")
		}
		attempt.Model = model
		return client.SubmitOperation(ctx, &attempt)
	})
}

// GetOperation fetches one task from the provider that created it.
func (c *Client) GetOperation(ctx context.Context, providerName, id string) (*Operation, error) {
	client, err := c.operationClient(providerName)
	if err != nil {
		return nil, err
	}
	return client.GetOperation(ctx, id)
}

// CancelOperation cancels a task when the provider has an official cancel method.
func (c *Client) CancelOperation(ctx context.Context, providerName, id string) error {
	client, err := c.operationClient(providerName)
	if err != nil {
		return err
	}
	return client.CancelOperation(ctx, id)
}

// ReadOperation downloads a finished task result using the provider credentials.
// The download is capped at 64 MiB.
func (c *Client) ReadOperation(ctx context.Context, providerName, id string) (*OperationAsset, error) {
	client, err := c.operationClient(providerName)
	if err != nil {
		return nil, err
	}
	return client.ReadOperation(ctx, id)
}

func (c *Client) operationClient(providerName string) (providers.OperationClient, error) {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil, fmt.Errorf("operation provider required")
	}
	p, ok := c.getProvider(providerName)
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", providerName)
	}
	client, ok := p.(providers.OperationClient)
	if !ok {
		return nil, fmt.Errorf("provider %q does not support long-running operations", providerName)
	}
	return client, nil
}
