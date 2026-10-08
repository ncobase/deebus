package deebus

import (
	"context"
	"fmt"
	"strings"

	"github.com/ncobase/deebus/providers"
)

// SubmitBatch starts an official message batch. OpenAI uploads JSONL and creates
// a batch. Anthropic posts the requests directly. Store the returned ID and poll.
func (c *Client) SubmitBatch(ctx context.Context, req *BatchRequest) (*Batch, error) {
	if req == nil || len(req.Items) == 0 {
		return nil, fmt.Errorf("batch items required")
	}
	providerName := strings.TrimSpace(req.Provider)
	if providerName == "" {
		return nil, fmt.Errorf("batch provider required")
	}
	limits := c.config.RequestPolicy.Limits
	if err := c.rejectMediaLimits(limits.rejectCount("batch items", len(req.Items))); err != nil {
		return nil, err
	}
	textBytes := 0
	items := make([]BatchItem, len(req.Items))
	for i, item := range req.Items {
		if item.Request == nil {
			return nil, fmt.Errorf("batch item %d request required", i)
		}
		normalized, err := normalizeBatchRequest(providerName, item.Request)
		if err != nil {
			return nil, err
		}
		for _, message := range normalized.Messages {
			textBytes += len(providers.ExtractText(message.Content))
		}
		items[i] = BatchItem{CustomID: item.CustomID, Request: normalized}
	}
	if err := c.rejectMediaLimits(limits.rejectText("batch text", textBytes)); err != nil {
		return nil, err
	}
	client, err := c.batchClient(providerName)
	if err != nil {
		return nil, err
	}
	attempt := *req
	attempt.Items = items
	batch, err := client.SubmitBatch(ctx, &attempt)
	if err != nil {
		c.Stats.RecordRequest(false, 0, 0, 0, 0)
		return nil, err
	}
	c.Stats.RecordRequest(true, 0, 0, 0, 0)
	return batch, nil
}

// GetBatch fetches one batch from the provider that created it.
func (c *Client) GetBatch(ctx context.Context, providerName, id string) (*Batch, error) {
	client, err := c.batchClient(providerName)
	if err != nil {
		return nil, err
	}
	return client.GetBatch(ctx, id)
}

// CancelBatch cancels a batch when the provider has an official cancel method.
func (c *Client) CancelBatch(ctx context.Context, providerName, id string) error {
	client, err := c.batchClient(providerName)
	if err != nil {
		return err
	}
	return client.CancelBatch(ctx, id)
}

// ReadBatch downloads completed batch results.
func (c *Client) ReadBatch(ctx context.Context, providerName, id string) ([]BatchResult, error) {
	client, err := c.batchClient(providerName)
	if err != nil {
		return nil, err
	}
	return client.ReadBatch(ctx, id)
}

func (c *Client) batchClient(providerName string) (providers.BatchClient, error) {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil, fmt.Errorf("batch provider required")
	}
	p, ok := c.getProvider(providerName)
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", providerName)
	}
	client, ok := p.(providers.BatchClient)
	if !ok {
		return nil, fmt.Errorf("provider %q does not support message batches", providerName)
	}
	return client, nil
}

func normalizeBatchRequest(providerName string, req *Request) (*Request, error) {
	cloned := *req
	if strings.Contains(cloned.Model, "/") {
		name, model, err := parseModel(cloned.Model)
		if err != nil {
			return nil, err
		}
		if name != providerName {
			return nil, fmt.Errorf("batch item model %q does not belong to provider %q", cloned.Model, providerName)
		}
		cloned.Model = model
	}
	return &cloned, nil
}
