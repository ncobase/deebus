package deebus

import (
	"context"
	"fmt"

	"github.com/ncobase/deebus/providers"
)

// CreateCache creates an explicit provider-managed cache resource.
func (c *Client) CreateCache(ctx context.Context, provider string, req *CreateCacheRequest) (*Cache, error) {
	if req == nil {
		return nil, fmt.Errorf("create cache request required")
	}
	cp, err := c.getCacheProvider(provider)
	if err != nil {
		return nil, err
	}
	r := *req
	return cp.CreateCache(ctx, &r)
}

// GetCache retrieves metadata for an explicit provider-managed cache resource.
func (c *Client) GetCache(ctx context.Context, provider, name string) (*Cache, error) {
	cp, err := c.getCacheProvider(provider)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("cache name required")
	}
	return cp.GetCache(ctx, name)
}

// ListCaches lists explicit provider-managed cache resources.
func (c *Client) ListCaches(ctx context.Context, provider string, req *ListCachesRequest) (*ListCachesResponse, error) {
	cp, err := c.getCacheProvider(provider)
	if err != nil {
		return nil, err
	}
	r := &providers.ListCachesRequest{}
	if req != nil {
		copied := *req
		r = &copied
	}
	return cp.ListCaches(ctx, r)
}

// UpdateCache updates an explicit provider-managed cache resource.
func (c *Client) UpdateCache(ctx context.Context, provider string, req *UpdateCacheRequest) (*Cache, error) {
	if req == nil {
		return nil, fmt.Errorf("update cache request required")
	}
	cp, err := c.getCacheProvider(provider)
	if err != nil {
		return nil, err
	}
	r := *req
	return cp.UpdateCache(ctx, &r)
}

// DeleteCache deletes an explicit provider-managed cache resource.
func (c *Client) DeleteCache(ctx context.Context, provider, name string) error {
	cp, err := c.getCacheProvider(provider)
	if err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("cache name required")
	}
	return cp.DeleteCache(ctx, name)
}

// getCacheProvider resolves a configured provider and asserts cache support.
func (c *Client) getCacheProvider(name string) (providers.CacheProvider, error) {
	p, ok := c.getProvider(name)
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", name)
	}
	cp, ok := p.(providers.CacheProvider)
	if !ok {
		return nil, fmt.Errorf("provider %q does not support cache management", name)
	}
	return cp, nil
}
