package deebus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const defaultCacheBreakerReplacement = "stable"

var anthropicBillingCCHPattern = regexp.MustCompile(`(?s)(^\s*x-anthropic-billing-header:.*?cch=)[^;[:space:]]+`)

// RequestPolicy is an optional gateway governance layer applied before a
// request reaches a provider.
//
// The zero value is a no-op. Applications can use it directly, or configure it
// on Client Config to apply it automatically for Complete and Stream calls.
type RequestPolicy struct {
	// Limits reject oversized or malformed gateway input before provider calls.
	Limits RequestLimits `yaml:"limits"`

	// Defaults fills safe provider-neutral request defaults when callers omit
	// them. Defaults never overwrite an explicitly set request field.
	Defaults RequestDefaults `yaml:"defaults"`

	// PromptCache injects OpenAI prompt-cache hints and can derive stable keys
	// from gateway scope, client identity, provider, model, user, and metadata.
	PromptCache PromptCachePolicy `yaml:"promptCache"`

	// CacheBreaker rewrites known provider/client cache-busting markers.
	CacheBreaker CacheBreakerPolicy `yaml:"cacheBreaker"`

	// Fingerprint controls the request fingerprint and snapshot returned in the
	// policy report.
	Fingerprint FingerprintOptions `yaml:"fingerprint"`

	// Reporter receives policy reports when the policy is applied through Client
	// or ApplyContext. It is programmatic only and is not loaded from YAML.
	Reporter RequestPolicyReporter `yaml:"-"`

	// FailOnReporterError rejects the request when Reporter returns an error.
	// The default is false so observability outages do not block model calls.
	FailOnReporterError bool `yaml:"failOnReporterError"`
}

// Enabled reports whether the policy has any behavior to apply.
func (p RequestPolicy) Enabled() bool {
	return p.Limits.Enabled() ||
		p.Defaults.Enabled() ||
		p.PromptCache.Enabled ||
		p.CacheBreaker.Enabled ||
		p.Fingerprint.Enabled() ||
		p.Reporter != nil
}

// Apply validates and mutates req according to the configured policy. It never
// logs or stores prompts. The returned report contains only metadata, hashes,
// counters, and change names suitable for request auditing.
func (p RequestPolicy) Apply(provider, model string, req *Request) (*RequestPolicyReport, error) {
	return p.ApplyContext(context.Background(), provider, model, req)
}

// ApplyContext is Apply with caller context propagated to the optional reporter.
func (p RequestPolicy) ApplyContext(ctx context.Context, provider, model string, req *Request) (*RequestPolicyReport, error) {
	if req == nil {
		return nil, fmt.Errorf("request policy: request is nil")
	}

	report := &RequestPolicyReport{
		Provider: provider,
		Model:    model,
	}

	if err := p.Limits.Validate(req); err != nil {
		report.Rejected = true
		report.Error = err.Error()
		report.Snapshot = SnapshotRequest(provider, model, req, p.Fingerprint)
		report.Fingerprint = report.Snapshot.Fingerprint
		if reportErr := p.report(ctx, report); reportErr != nil {
			return report, reportErr
		}
		return report, err
	}

	p.Defaults.apply(req, report)
	p.PromptCache.apply(provider, model, req, report)
	p.CacheBreaker.apply(req, report)

	if err := p.Limits.Validate(req); err != nil {
		report.Rejected = true
		report.Error = err.Error()
		report.Snapshot = SnapshotRequest(provider, model, req, p.Fingerprint)
		report.Fingerprint = report.Snapshot.Fingerprint
		if reportErr := p.report(ctx, report); reportErr != nil {
			return report, reportErr
		}
		return report, err
	}

	report.Snapshot = SnapshotRequest(provider, model, req, p.Fingerprint)
	report.Fingerprint = report.Snapshot.Fingerprint
	if err := p.report(ctx, report); err != nil {
		return report, err
	}
	return report, nil
}

// RequestPolicyReport describes changes and a safe request snapshot produced by
// RequestPolicy.Apply.
type RequestPolicyReport struct {
	Provider    string                `json:"provider,omitempty"`
	Model       string                `json:"model,omitempty"`
	Changes     []RequestPolicyChange `json:"changes,omitempty"`
	Fingerprint string                `json:"fingerprint,omitempty"`
	Snapshot    RequestSnapshot       `json:"snapshot"`
	Rejected    bool                  `json:"rejected,omitempty"`
	Error       string                `json:"error,omitempty"`
	ReportError string                `json:"report_error,omitempty"`
}

// RequestPolicyChange records a policy mutation without including prompt text.
type RequestPolicyChange struct {
	Type   string `json:"type"`
	Target string `json:"target,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func (r *RequestPolicyReport) addChange(kind, target, detail string) {
	r.Changes = append(r.Changes, RequestPolicyChange{
		Type:   kind,
		Target: target,
		Detail: detail,
	})
}

// RequestPolicyReporter receives policy reports for application audit or usage
// records. Implementations must not store prompt text because reports are
// intentionally prompt-safe.
type RequestPolicyReporter interface {
	ReportRequestPolicy(ctx context.Context, report RequestPolicyReport) error
}

// RequestPolicyReporterFunc adapts a function to RequestPolicyReporter.
type RequestPolicyReporterFunc func(context.Context, RequestPolicyReport) error

// ReportRequestPolicy implements RequestPolicyReporter.
func (f RequestPolicyReporterFunc) ReportRequestPolicy(ctx context.Context, report RequestPolicyReport) error {
	if f == nil {
		return nil
	}
	return f(ctx, report)
}

func (p RequestPolicy) report(ctx context.Context, report *RequestPolicyReport) error {
	if p.Reporter == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.Reporter.ReportRequestPolicy(ctx, *report); err != nil {
		report.ReportError = err.Error()
		if p.FailOnReporterError {
			return fmt.Errorf("request policy reporter: %w", err)
		}
	}
	return nil
}

// RequestDefaults contains provider-neutral defaults applied by RequestPolicy.
type RequestDefaults struct {
	// Store fills Request.Store when the caller does not set it. Use this to
	// enforce a gateway privacy default while preserving explicit caller intent.
	Store *bool `yaml:"store"`
}

func (d RequestDefaults) Enabled() bool {
	return d.Store != nil
}

func (d RequestDefaults) apply(req *Request, report *RequestPolicyReport) {
	if d.Store != nil && req.Store == nil {
		value := *d.Store
		req.Store = &value
		report.addChange("default", "store", fmt.Sprintf("%t", value))
	}
}

// PromptCachePolicy injects provider-native prompt-cache hints on Request.Cache.
type PromptCachePolicy struct {
	Enabled bool `yaml:"enabled"`

	// Key is an explicit prompt cache key. If empty, the policy builds a stable
	// key from Scope, Client, provider/model, user, and selected metadata.
	Key string `yaml:"key"`

	// Scope and Client let gateways isolate cache affinity by tenant, space,
	// application, or caller class without storing raw prompt content.
	Scope  string `yaml:"scope"`
	Client string `yaml:"client"`

	IncludeProvider bool     `yaml:"includeProvider"`
	IncludeModel    bool     `yaml:"includeModel"`
	IncludeUser     bool     `yaml:"includeUser"`
	MetadataKeys    []string `yaml:"metadataKeys"`

	// Retention maps to Request.Cache.Retention for providers that support it.
	Retention string `yaml:"retention"`

	// Override replaces an existing Request.Cache.Key. The default preserves
	// caller-provided cache keys.
	Override bool `yaml:"override"`

	// MaxLength caps the final cache key. Values <=0 use a conservative default.
	MaxLength int `yaml:"maxLength"`
}

func (p PromptCachePolicy) apply(provider, model string, req *Request, report *RequestPolicyReport) {
	if !p.Enabled {
		return
	}

	if req.Cache == nil {
		req.Cache = &CacheOptions{}
	}

	if p.Retention != "" && req.Cache.Retention == "" {
		req.Cache.Retention = p.Retention
		report.addChange("prompt_cache_retention", "cache.retention", p.Retention)
	}

	if req.Cache.Key != "" && !p.Override {
		return
	}

	key := strings.TrimSpace(p.Key)
	if key == "" {
		key = p.derivedKey(provider, model, req)
	}
	if key == "" {
		return
	}

	req.Cache.Key = BuildPromptCacheKey(p.MaxLength, strings.Split(key, ":")...)
	report.addChange("prompt_cache_key", "cache.key", hashShort(req.Cache.Key))
}

func (p PromptCachePolicy) derivedKey(provider, model string, req *Request) string {
	parts := make([]string, 0, 6+len(p.MetadataKeys))
	parts = append(parts, p.Scope, p.Client)
	if p.IncludeProvider {
		parts = append(parts, provider)
	}
	if p.IncludeModel {
		parts = append(parts, model)
	}
	if p.IncludeUser && req.UserID != "" {
		parts = append(parts, "user:"+req.UserID)
	}
	if len(p.MetadataKeys) > 0 {
		keys := append([]string(nil), p.MetadataKeys...)
		sort.Strings(keys)
		for _, key := range keys {
			if value := strings.TrimSpace(req.Metadata[key]); value != "" {
				parts = append(parts, key+":"+value)
			}
		}
	}
	if countNonEmptyParts(parts) == 0 {
		parts = append(parts, provider, model)
	}
	return strings.Join(parts, ":")
}

// BuildPromptCacheKey returns a stable, provider-safe cache-affinity key. Empty
// parts are ignored. Whitespace and separators are normalized to "-". When the
// normalized key exceeds maxLength, it is shortened with a SHA-256 suffix.
func BuildPromptCacheKey(maxLength int, parts ...string) string {
	if maxLength <= 0 {
		maxLength = 256
	}

	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := normalizeCacheKeyPart(part); value != "" {
			normalized = append(normalized, value)
		}
	}
	if len(normalized) == 0 {
		return ""
	}

	key := strings.Join(normalized, ":")
	if len(key) <= maxLength {
		return key
	}

	sum := sha256.Sum256([]byte(key))
	suffix := hex.EncodeToString(sum[:])[:16]
	if maxLength <= len(suffix)+1 {
		return suffix[:maxLength]
	}
	prefixLen := maxLength - len(suffix) - 1
	return strings.TrimRight(key[:prefixLen], ":-_") + ":" + suffix
}

func normalizeCacheKeyPart(part string) string {
	part = strings.TrimSpace(part)
	if part == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(part))
	lastDash := false
	for _, r := range part {
		allowed := r == '.' || r == '_' || r == '-' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9')
		if allowed {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if unicode.IsSpace(r) || r == ':' || r == '/' || r == '\\' || r == ';' || r == ',' {
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func countNonEmptyParts(parts []string) int {
	count := 0
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			count++
		}
	}
	return count
}

// CacheBreakerPolicy contains opt-in rewrites for known cache-busting request
// fragments generated by upstream clients.
type CacheBreakerPolicy struct {
	Enabled bool `yaml:"enabled"`

	// AnthropicBillingHeaderCCH normalizes cch=... in system prompts beginning
	// with x-anthropic-billing-header. This is useful for clients that inject
	// random cache-busting markers into otherwise stable prefixes.
	AnthropicBillingHeaderCCH bool `yaml:"anthropicBillingHeaderCCH"`

	// Replacement is the stable cch value. Empty uses a deterministic default.
	Replacement string `yaml:"replacement"`
}

func (p CacheBreakerPolicy) apply(req *Request, report *RequestPolicyReport) {
	if !p.Enabled || !p.AnthropicBillingHeaderCCH {
		return
	}

	replacement := normalizeCCHReplacement(p.Replacement)
	if replacement == "" {
		replacement = defaultCacheBreakerReplacement
	}

	count := NormalizeAnthropicBillingHeaderCCH(req, replacement)
	if count > 0 {
		report.addChange("cache_breaker_rewrite", "system.cch", fmt.Sprintf("%d", count))
	}
}

// NormalizeAnthropicBillingHeaderCCH rewrites random cch values in Anthropic
// billing-header system prompts. It returns the number of text blocks changed.
func NormalizeAnthropicBillingHeaderCCH(req *Request, replacement string) int {
	if req == nil {
		return 0
	}
	replacement = normalizeCCHReplacement(replacement)
	if replacement == "" {
		replacement = defaultCacheBreakerReplacement
	}

	changed := 0
	for msgIndex := range req.Messages {
		if req.Messages[msgIndex].Role != "system" {
			continue
		}
		for blockIndex, block := range req.Messages[msgIndex].Content {
			switch value := block.(type) {
			case TextContent:
				rewritten, ok := rewriteCCHText(value.Text, replacement)
				if ok {
					value.Text = rewritten
					req.Messages[msgIndex].Content[blockIndex] = value
					changed++
				}
			case *TextContent:
				if value == nil {
					continue
				}
				rewritten, ok := rewriteCCHText(value.Text, replacement)
				if ok {
					value.Text = rewritten
					changed++
				}
			}
		}
	}
	return changed
}

func rewriteCCHText(text, replacement string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(text), "x-anthropic-billing-header:") {
		return text, false
	}
	rewritten := anthropicBillingCCHPattern.ReplaceAllString(text, "${1}"+replacement)
	return rewritten, rewritten != text
}

func normalizeCCHReplacement(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hashShort(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
