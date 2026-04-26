package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

func buildProviderEndpoint(baseURL, apiPath string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("invalid provider base URL: %w", err)
	}

	basePath := strings.TrimRight(u.Path, "/")
	targetPath := "/" + strings.TrimLeft(apiPath, "/")
	for _, prefix := range []string{"/api", "/v2", "/v1beta", "/v1"} {
		if strings.HasSuffix(basePath, prefix) && strings.HasPrefix(targetPath, prefix+"/") {
			targetPath = strings.TrimPrefix(targetPath, prefix)
			break
		}
	}

	u.Path = basePath + targetPath
	u.RawQuery = ""
	return u.String(), nil
}

func doProviderJSONRequest(
	ctx context.Context,
	client *http.Client,
	req *http.Request,
	provider string,
	out any,
) error {
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return networkError(provider, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		return parseError(resp.StatusCode, body, resp.Header, provider)
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

func normalizeModelNames(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	models := make([]string, 0, len(values))
	for _, value := range values {
		model := strings.TrimSpace(value)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}

	sort.Strings(models)
	return models
}

func supportsGeminiGeneration(methods []string) bool {
	for _, method := range methods {
		switch strings.TrimSpace(method) {
		case "generateContent", "streamGenerateContent":
			return true
		}
	}
	return false
}

func supportsCohereChat(endpoints, features []string) bool {
	for _, endpoint := range endpoints {
		if strings.EqualFold(strings.TrimSpace(endpoint), "chat") {
			return true
		}
	}
	for _, feature := range features {
		if strings.EqualFold(strings.TrimSpace(feature), "chat-completions") {
			return true
		}
	}
	return false
}
