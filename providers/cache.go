package providers

import (
	"context"
	"time"
)

// CacheOptions configures provider-native request-time caching features.
//
// Provider mapping:
// - Anthropic: Control -> top-level automatic cache_control
// - OpenAI: Key -> prompt_cache_key, Retention -> prompt_cache_retention
// - Gemini: CachedContent -> explicit cached content resource name
type CacheOptions struct {
	// Control maps to Anthropic's top-level cache_control field.
	Control *CacheControl `json:"cache_control,omitempty"`

	// Key maps to OpenAI's prompt_cache_key.
	Key string `json:"prompt_cache_key,omitempty"`

	// Retention maps to OpenAI's prompt_cache_retention.
	// Accepted values are "in_memory" and "24h". "in-memory" is also accepted
	// by the library and normalised to "in_memory".
	Retention string `json:"prompt_cache_retention,omitempty"`

	// CachedContent maps to Gemini's cachedContent field and should contain a
	// cache resource name such as "cachedContents/abc123".
	CachedContent string `json:"cached_content,omitempty"`
}

// Cache represents metadata for an explicit provider-managed cache resource.
// The current implementation is populated by Gemini cachedContents APIs.
type Cache struct {
	Name        string
	Provider    string
	Model       string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExpiresAt   time.Time
	Usage       CacheUsageMetadata
}

// CacheUsageMetadata is metadata reported for an explicit cache resource.
type CacheUsageMetadata struct {
	TotalTokenCount int
}

// CreateCacheRequest creates an explicit provider-managed cache resource.
type CreateCacheRequest struct {
	Model       string
	DisplayName string
	Messages    []Message
	Tools       []Tool
	ToolChoice  string

	// TTL is the desired cache lifetime. Zero means provider default.
	TTL time.Duration

	// ExpiresAt sets an absolute expiration time when the provider supports it.
	// TTL and ExpiresAt are mutually exclusive.
	ExpiresAt time.Time
}

// UpdateCacheRequest updates an explicit provider-managed cache resource.
// Only TTL or expiration updates are supported.
type UpdateCacheRequest struct {
	Name string

	// TTL and ExpiresAt are mutually exclusive.
	TTL       time.Duration
	ExpiresAt time.Time
}

// ListCachesRequest lists explicit provider-managed cache resources.
type ListCachesRequest struct {
	PageSize  int
	PageToken string
}

// ListCachesResponse contains a page of explicit cache resources.
type ListCachesResponse struct {
	Items         []Cache
	NextPageToken string
}

// CacheProvider is implemented by providers that support explicit cache
// resource lifecycle management.
type CacheProvider interface {
	CreateCache(ctx context.Context, req *CreateCacheRequest) (*Cache, error)
	GetCache(ctx context.Context, name string) (*Cache, error)
	ListCaches(ctx context.Context, req *ListCachesRequest) (*ListCachesResponse, error)
	UpdateCache(ctx context.Context, req *UpdateCacheRequest) (*Cache, error)
	DeleteCache(ctx context.Context, name string) error
}
