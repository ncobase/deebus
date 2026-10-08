package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

func (p *GeminiProvider) postGemini(ctx context.Context, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	endpoint, err := p.geminiURL(creds, path, "")
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
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
		return parseError(resp.StatusCode, readErrorBody(resp.Body), resp.Header, p.Name())
	}
	raw, err := readResponseBody(resp.Body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
