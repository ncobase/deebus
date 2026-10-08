package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// callAbsolute sends JSON to an absolute path on the provider base URL.
func (p *OpenAIProvider) callAbsolute(ctx context.Context, method, path string, body, out any) error {
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	creds, err := p.cfg.credentials(ctx)
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	p.setAuth(req, creds)
	return doProviderJSONRequest(ctx, p.client, req, p.Name(), out)
}

func sameHost(baseURL, raw string) bool {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Host == "" {
		return false
	}
	asset, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || asset.Host == "" {
		return false
	}
	return strings.EqualFold(base.Host, asset.Host)
}
