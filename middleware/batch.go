package middleware

import (
	"context"
	"fmt"

	"github.com/ncobase/deebus/providers"
)

func batchClient(p providers.Provider) (providers.BatchClient, error) {
	client, ok := p.(providers.BatchClient)
	if !ok {
		return nil, fmt.Errorf("provider %q does not support message batches", p.Name())
	}
	return client, nil
}

func (m *LoggingMiddleware) SubmitBatch(ctx context.Context, req *providers.BatchRequest) (*providers.Batch, error) {
	inner, err := batchClient(m.provider)
	if err != nil {
		return nil, err
	}
	return inner.SubmitBatch(ctx, req)
}

func (m *LoggingMiddleware) GetBatch(ctx context.Context, id string) (*providers.Batch, error) {
	inner, err := batchClient(m.provider)
	if err != nil {
		return nil, err
	}
	return inner.GetBatch(ctx, id)
}

func (m *LoggingMiddleware) CancelBatch(ctx context.Context, id string) error {
	inner, err := batchClient(m.provider)
	if err != nil {
		return err
	}
	return inner.CancelBatch(ctx, id)
}

func (m *LoggingMiddleware) ReadBatch(ctx context.Context, id string) ([]providers.BatchResult, error) {
	inner, err := batchClient(m.provider)
	if err != nil {
		return nil, err
	}
	return inner.ReadBatch(ctx, id)
}

func (m *RetryMiddleware) SubmitBatch(ctx context.Context, req *providers.BatchRequest) (*providers.Batch, error) {
	return retryValue(m, ctx, func() (*providers.Batch, error) {
		inner, err := batchClient(m.provider)
		if err != nil {
			return nil, err
		}
		return inner.SubmitBatch(ctx, req)
	})
}

func (m *RetryMiddleware) GetBatch(ctx context.Context, id string) (*providers.Batch, error) {
	return retryValue(m, ctx, func() (*providers.Batch, error) {
		inner, err := batchClient(m.provider)
		if err != nil {
			return nil, err
		}
		return inner.GetBatch(ctx, id)
	})
}

func (m *RetryMiddleware) CancelBatch(ctx context.Context, id string) error {
	_, err := retryValue(m, ctx, func() (struct{}, error) {
		inner, err := batchClient(m.provider)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, inner.CancelBatch(ctx, id)
	})
	return err
}

func (m *RetryMiddleware) ReadBatch(ctx context.Context, id string) ([]providers.BatchResult, error) {
	return retryValue(m, ctx, func() ([]providers.BatchResult, error) {
		inner, err := batchClient(m.provider)
		if err != nil {
			return nil, err
		}
		return inner.ReadBatch(ctx, id)
	})
}

func (m *CircuitBreakerMiddleware) SubmitBatch(ctx context.Context, req *providers.BatchRequest) (*providers.Batch, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	inner, err := batchClient(m.provider)
	if err != nil {
		m.record(err)
		return nil, err
	}
	resp, err := inner.SubmitBatch(ctx, req)
	m.record(err)
	return resp, err
}

func (m *CircuitBreakerMiddleware) GetBatch(ctx context.Context, id string) (*providers.Batch, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	inner, err := batchClient(m.provider)
	if err != nil {
		m.record(err)
		return nil, err
	}
	resp, err := inner.GetBatch(ctx, id)
	m.record(err)
	return resp, err
}

func (m *CircuitBreakerMiddleware) CancelBatch(ctx context.Context, id string) error {
	if err := m.allow(); err != nil {
		return err
	}
	inner, err := batchClient(m.provider)
	if err != nil {
		m.record(err)
		return err
	}
	err = inner.CancelBatch(ctx, id)
	m.record(err)
	return err
}

func (m *CircuitBreakerMiddleware) ReadBatch(ctx context.Context, id string) ([]providers.BatchResult, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	inner, err := batchClient(m.provider)
	if err != nil {
		m.record(err)
		return nil, err
	}
	resp, err := inner.ReadBatch(ctx, id)
	m.record(err)
	return resp, err
}

func (r *RateLimitMiddleware) SubmitBatch(ctx context.Context, req *providers.BatchRequest) (*providers.Batch, error) {
	inner, err := batchClient(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return inner.SubmitBatch(ctx, req)
}

func (r *RateLimitMiddleware) GetBatch(ctx context.Context, id string) (*providers.Batch, error) {
	inner, err := batchClient(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return inner.GetBatch(ctx, id)
}

func (r *RateLimitMiddleware) CancelBatch(ctx context.Context, id string) error {
	inner, err := batchClient(r.provider)
	if err != nil {
		return err
	}
	if err := r.acquire(ctx); err != nil {
		return err
	}
	return inner.CancelBatch(ctx, id)
}

func (r *RateLimitMiddleware) ReadBatch(ctx context.Context, id string) ([]providers.BatchResult, error) {
	inner, err := batchClient(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return inner.ReadBatch(ctx, id)
}
