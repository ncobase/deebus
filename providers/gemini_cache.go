package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CreateCache creates a Gemini cachedContents resource.
func (p *GeminiProvider) CreateCache(ctx context.Context, req *CreateCacheRequest) (*Cache, error) {
	if req == nil {
		return nil, fmt.Errorf("create cache request required")
	}
	if req.Model == "" {
		return nil, fmt.Errorf("create cache request model required")
	}
	if err := validateCacheExpiry(req.TTL, req.ExpiresAt); err != nil {
		return nil, err
	}

	system, msgs := ExtractSystemMessage(req.Messages)

	body := map[string]any{
		"model":    geminiCacheModel(req.Model),
		"contents": ConvertToGeminiFormat(msgs),
	}
	if req.DisplayName != "" {
		body["displayName"] = req.DisplayName
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{
			"role":  "system",
			"parts": []map[string]any{{"text": system}},
		}
	}
	if len(req.Tools) > 0 {
		body["tools"] = ConvertToolsToGemini(req.Tools)
		if req.ToolChoice != "" {
			body["toolConfig"] = GeminiToolConfig(req.ToolChoice)
		}
	}
	if req.TTL > 0 {
		body["ttl"] = geminiDuration(req.TTL)
	}
	if !req.ExpiresAt.IsZero() {
		body["expireTime"] = req.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}

	endpoint, err := p.geminiURL(creds, "/v1beta/cachedContents", "")
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq, creds)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, parseError(resp.StatusCode, b, resp.Header, p.Name())
	}

	var result geminiCachedContent
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.toCache(p.Name()), nil
}

// GetCache reads metadata for a Gemini cachedContents resource.
func (p *GeminiProvider) GetCache(ctx context.Context, name string) (*Cache, error) {
	if name == "" {
		return nil, fmt.Errorf("cache name required")
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}

	endpoint, err := p.geminiURL(creds, "/v1beta/"+name, "")
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq, creds)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, parseError(resp.StatusCode, b, resp.Header, p.Name())
	}

	var result geminiCachedContent
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.toCache(p.Name()), nil
}

// ListCaches lists Gemini cachedContents metadata.
func (p *GeminiProvider) ListCaches(ctx context.Context, req *ListCachesRequest) (*ListCachesResponse, error) {
	query := url.Values{}
	if req != nil {
		if req.PageSize > 0 {
			query.Set("pageSize", strconv.Itoa(req.PageSize))
		}
		if req.PageToken != "" {
			query.Set("pageToken", req.PageToken)
		}
	}

	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}

	endpoint, err := p.geminiURL(creds, "/v1beta/cachedContents", query.Encode())
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq, creds)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, parseError(resp.StatusCode, b, resp.Header, p.Name())
	}

	var result struct {
		CachedContents []geminiCachedContent `json:"cachedContents"`
		NextPageToken  string                `json:"nextPageToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	items := make([]Cache, len(result.CachedContents))
	for i, item := range result.CachedContents {
		items[i] = *item.toCache(p.Name())
	}

	return &ListCachesResponse{
		Items:         items,
		NextPageToken: result.NextPageToken,
	}, nil
}

// UpdateCache updates a Gemini cachedContents resource TTL / expiration.
func (p *GeminiProvider) UpdateCache(ctx context.Context, req *UpdateCacheRequest) (*Cache, error) {
	if req == nil {
		return nil, fmt.Errorf("update cache request required")
	}
	if req.Name == "" {
		return nil, fmt.Errorf("update cache request name required")
	}
	if err := validateCacheExpiry(req.TTL, req.ExpiresAt); err != nil {
		return nil, err
	}

	body := map[string]any{}
	if req.TTL > 0 {
		body["ttl"] = geminiDuration(req.TTL)
	}
	if !req.ExpiresAt.IsZero() {
		body["expireTime"] = req.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("update cache request requires ttl or expiresAt")
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}

	endpoint, err := p.geminiURL(creds, "/v1beta/"+req.Name, "")
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq, creds)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, networkError(p.Name(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, parseError(resp.StatusCode, b, resp.Header, p.Name())
	}

	var result geminiCachedContent
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.toCache(p.Name()), nil
}

// DeleteCache deletes a Gemini cachedContents resource.
func (p *GeminiProvider) DeleteCache(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("cache name required")
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}

	endpoint, err := p.geminiURL(creds, "/v1beta/"+name, "")
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	p.setHeaders(httpReq, creds)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return networkError(p.Name(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return parseError(resp.StatusCode, b, resp.Header, p.Name())
	}
	return nil
}

type geminiCachedContent struct {
	Name          string `json:"name"`
	Model         string `json:"model"`
	DisplayName   string `json:"displayName"`
	CreateTime    string `json:"createTime"`
	UpdateTime    string `json:"updateTime"`
	ExpireTime    string `json:"expireTime"`
	UsageMetadata struct {
		TotalTokenCount int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

// toCache converts the raw Gemini cachedContents payload into the unified type.
func (c geminiCachedContent) toCache(provider string) *Cache {
	return &Cache{
		Name:        c.Name,
		Provider:    provider,
		Model:       strings.TrimPrefix(c.Model, "models/"),
		DisplayName: c.DisplayName,
		CreatedAt:   parseRFC3339Time(c.CreateTime),
		UpdatedAt:   parseRFC3339Time(c.UpdateTime),
		ExpiresAt:   parseRFC3339Time(c.ExpireTime),
		Usage: CacheUsageMetadata{
			TotalTokenCount: c.UsageMetadata.TotalTokenCount,
		},
	}
}

// geminiCacheModel normalizes a model name to Gemini's "models/..." format.
func geminiCacheModel(model string) string {
	if strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}

// validateCacheExpiry enforces the mutual exclusivity of TTL and expiration.
func validateCacheExpiry(ttl time.Duration, expiresAt time.Time) error {
	if ttl > 0 && !expiresAt.IsZero() {
		return fmt.Errorf("ttl and expiresAt are mutually exclusive")
	}
	if ttl < 0 {
		return fmt.Errorf("ttl must be >= 0")
	}
	return nil
}

// geminiDuration converts a Go duration into Gemini's duration string format.
func geminiDuration(d time.Duration) string {
	seconds := d.Seconds()
	return strconv.FormatFloat(seconds, 'f', -1, 64) + "s"
}

// parseRFC3339Time parses provider timestamps and returns the zero value on failure.
func parseRFC3339Time(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}
	}
	return t
}
