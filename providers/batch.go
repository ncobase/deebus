package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
)

// BatchClient runs an official message batch. The caller stores the ID and polls.
type BatchClient interface {
	SubmitBatch(ctx context.Context, req *BatchRequest) (*Batch, error)
	GetBatch(ctx context.Context, id string) (*Batch, error)
	CancelBatch(ctx context.Context, id string) error
	ReadBatch(ctx context.Context, id string) ([]BatchResult, error)
}

// BatchItem is one request inside a batch.
type BatchItem struct {
	CustomID string   // caller id echoed in results; generated when empty
	Request  *Request // chat request; model may be "provider/model" on Client.SubmitBatch
}

// BatchRequest is a provider-neutral message batch.
type BatchRequest struct {
	Provider         string // configured provider name, such as "openai" or "anthropic"
	Endpoint         string // OpenAI batch endpoint; empty uses /v1/chat/completions
	CompletionWindow string // OpenAI window; empty uses 24h
	Items            []BatchItem
}

// Batch is one provider batch job.
type Batch struct {
	ID        string
	Provider  string
	Status    OperationStatus
	OutputRef string // OpenAI output file id; empty for Anthropic
	Error     string
	Succeeded int
	Errored   int
}

// BatchResult is one completed batch item.
type BatchResult struct {
	CustomID string
	Content  string // assistant text when the item succeeded
	Error    string
}

// SubmitBatch uploads JSONL to POST /v1/files and creates POST /v1/batches.
func (p *OpenAIProvider) SubmitBatch(ctx context.Context, req *BatchRequest) (*Batch, error) {
	lines, err := openAIBatchJSONL(req)
	if err != nil {
		return nil, err
	}
	fileID, err := p.uploadBatchFile(ctx, lines)
	if err != nil {
		return nil, err
	}
	window := req.CompletionWindow
	if window == "" {
		window = "24h"
	}
	endpoint := req.Endpoint
	if endpoint == "" {
		endpoint = "/v1/chat/completions"
	}
	var payload openAIBatch
	if err := p.postJSON(ctx, "", "/v1/batches", map[string]any{
		"input_file_id":     fileID,
		"endpoint":          endpoint,
		"completion_window": window,
	}, &payload); err != nil {
		return nil, err
	}
	return payload.asBatch(p.Name()), nil
}

// GetBatch calls GET /v1/batches/{id}.
func (p *OpenAIProvider) GetBatch(ctx context.Context, id string) (*Batch, error) {
	if err := validOperationID(id); err != nil {
		return nil, err
	}
	endpoint, err := p.openAIEndpoint("", "/v1/batches/"+id)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	p.setHeaders(httpReq, creds)
	var payload openAIBatch
	if err := doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), &payload); err != nil {
		return nil, err
	}
	return payload.asBatch(p.Name()), nil
}

// CancelBatch calls POST /v1/batches/{id}/cancel.
func (p *OpenAIProvider) CancelBatch(ctx context.Context, id string) error {
	if err := validOperationID(id); err != nil {
		return err
	}
	return p.postJSON(ctx, "", "/v1/batches/"+id+"/cancel", map[string]any{}, nil)
}

// ReadBatch downloads the completed output file from GET /v1/files/{id}/content.
func (p *OpenAIProvider) ReadBatch(ctx context.Context, id string) ([]BatchResult, error) {
	batch, err := p.GetBatch(ctx, id)
	if err != nil {
		return nil, err
	}
	if batch.OutputRef == "" {
		return nil, fmt.Errorf("openai batch has no output file")
	}
	endpoint, err := p.openAIEndpoint("", "/v1/files/"+batch.OutputRef+"/content")
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}
	p.setAuth(httpReq, creds)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp.StatusCode, raw, resp.Header, p.Name())
	}
	return parseOpenAIBatchResults(raw)
}

func (p *OpenAIProvider) uploadBatchFile(ctx context.Context, jsonl []byte) (string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("purpose", "batch"); err != nil {
		return "", err
	}
	part, err := writer.CreateFormFile("file", "batch.jsonl")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(jsonl); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	endpoint, err := p.openAIEndpoint("", "/v1/files")
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve credentials: %w", err)
	}
	p.setAuth(httpReq, creds)
	var payload struct {
		ID string `json:"id"`
	}
	if err := doProviderJSONRequest(ctx, p.client, httpReq, p.Name(), &payload); err != nil {
		return "", err
	}
	if payload.ID == "" {
		return "", fmt.Errorf("openai batch file id missing")
	}
	return payload.ID, nil
}

func openAIBatchJSONL(req *BatchRequest) ([]byte, error) {
	if req == nil || len(req.Items) == 0 {
		return nil, fmt.Errorf("batch items required")
	}
	var buf bytes.Buffer
	for i, item := range req.Items {
		if item.Request == nil {
			return nil, fmt.Errorf("batch item %d request required", i)
		}
		customID := item.CustomID
		if customID == "" {
			customID = fmt.Sprintf("item-%d", i)
		}
		body, err := openAIChatBody(item.Request)
		if err != nil {
			return nil, err
		}
		line, err := json.Marshal(map[string]any{
			"custom_id": customID,
			"method":    "POST",
			"url":       "/v1/chat/completions",
			"body":      body,
		})
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

type openAIBatch struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	OutputFileID string `json:"output_file_id"`
	ErrorFileID  string `json:"error_file_id"`
	Errors       *struct {
		Data []struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"errors"`
	RequestCounts struct {
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
	} `json:"request_counts"`
}

func (b openAIBatch) asBatch(provider string) *Batch {
	batch := &Batch{
		ID:        b.ID,
		Provider:  provider,
		Status:    mapOpenAIBatchStatus(b.Status),
		OutputRef: b.OutputFileID,
		Succeeded: b.RequestCounts.Completed,
		Errored:   b.RequestCounts.Failed,
	}
	if b.Errors != nil && len(b.Errors.Data) > 0 {
		batch.Error = b.Errors.Data[0].Message
	}
	return batch
}

func mapOpenAIBatchStatus(status string) OperationStatus {
	switch strings.ToLower(status) {
	case "validating", "in_progress", "finalizing", "cancelling":
		return OperationRunning
	case "completed":
		return OperationSucceeded
	case "failed", "expired":
		return OperationFailed
	case "cancelled", "canceled":
		return OperationCancelled
	default:
		return OperationQueued
	}
}

func parseOpenAIBatchResults(raw []byte) ([]BatchResult, error) {
	var results []BatchResult
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), maxBufferedBody)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row struct {
			CustomID string `json:"custom_id"`
			Error    *struct {
				Message string `json:"message"`
			} `json:"error"`
			Response struct {
				StatusCode int `json:"status_code"`
				Body       struct {
					Error *struct {
						Message string `json:"message"`
					} `json:"error"`
					Choices []struct {
						Message struct {
							Content string `json:"content"`
						} `json:"message"`
					} `json:"choices"`
				} `json:"body"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("decode batch result: %w", err)
		}
		item := BatchResult{CustomID: row.CustomID}
		if row.Error != nil {
			item.Error = row.Error.Message
		}
		if row.Response.Body.Error != nil && item.Error == "" {
			item.Error = row.Response.Body.Error.Message
		}
		if row.Response.StatusCode >= 400 && item.Error == "" {
			item.Error = fmt.Sprintf("status %d", row.Response.StatusCode)
		}
		if len(row.Response.Body.Choices) > 0 {
			item.Content = row.Response.Body.Choices[0].Message.Content
		}
		results = append(results, item)
	}
	return results, scanner.Err()
}
