package middleware

import (
	"context"
	"fmt"

	"github.com/ncobase/deebus/providers"
)

func operationClient(p providers.Provider) (providers.OperationClient, error) {
	client, ok := p.(providers.OperationClient)
	if !ok {
		return nil, fmt.Errorf("provider %q does not support long-running operations", p.Name())
	}
	return client, nil
}

func (m *LoggingMiddleware) SubmitOperation(ctx context.Context, req *providers.OperationRequest) (*providers.Operation, error) {
	inner, err := operationClient(m.provider)
	if err != nil {
		return nil, err
	}
	return inner.SubmitOperation(ctx, req)
}

func (m *LoggingMiddleware) GetOperation(ctx context.Context, id string) (*providers.Operation, error) {
	inner, err := operationClient(m.provider)
	if err != nil {
		return nil, err
	}
	return inner.GetOperation(ctx, id)
}

func (m *LoggingMiddleware) CancelOperation(ctx context.Context, id string) error {
	inner, err := operationClient(m.provider)
	if err != nil {
		return err
	}
	return inner.CancelOperation(ctx, id)
}

func (m *LoggingMiddleware) ReadOperation(ctx context.Context, id string) (*providers.OperationAsset, error) {
	inner, err := operationClient(m.provider)
	if err != nil {
		return nil, err
	}
	return inner.ReadOperation(ctx, id)
}

func (m *RetryMiddleware) SubmitOperation(ctx context.Context, req *providers.OperationRequest) (*providers.Operation, error) {
	return retryValue(m, ctx, func() (*providers.Operation, error) {
		inner, err := operationClient(m.provider)
		if err != nil {
			return nil, err
		}
		return inner.SubmitOperation(ctx, req)
	})
}

func (m *RetryMiddleware) GetOperation(ctx context.Context, id string) (*providers.Operation, error) {
	return retryValue(m, ctx, func() (*providers.Operation, error) {
		inner, err := operationClient(m.provider)
		if err != nil {
			return nil, err
		}
		return inner.GetOperation(ctx, id)
	})
}

func (m *RetryMiddleware) CancelOperation(ctx context.Context, id string) error {
	_, err := retryValue(m, ctx, func() (struct{}, error) {
		inner, err := operationClient(m.provider)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, inner.CancelOperation(ctx, id)
	})
	return err
}

func (m *RetryMiddleware) ReadOperation(ctx context.Context, id string) (*providers.OperationAsset, error) {
	return retryValue(m, ctx, func() (*providers.OperationAsset, error) {
		inner, err := operationClient(m.provider)
		if err != nil {
			return nil, err
		}
		return inner.ReadOperation(ctx, id)
	})
}

func (m *CircuitBreakerMiddleware) SubmitOperation(ctx context.Context, req *providers.OperationRequest) (*providers.Operation, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	inner, err := operationClient(m.provider)
	if err != nil {
		m.record(err)
		return nil, err
	}
	resp, err := inner.SubmitOperation(ctx, req)
	m.record(err)
	return resp, err
}

func (m *CircuitBreakerMiddleware) GetOperation(ctx context.Context, id string) (*providers.Operation, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	inner, err := operationClient(m.provider)
	if err != nil {
		m.record(err)
		return nil, err
	}
	resp, err := inner.GetOperation(ctx, id)
	m.record(err)
	return resp, err
}

func (m *CircuitBreakerMiddleware) CancelOperation(ctx context.Context, id string) error {
	if err := m.allow(); err != nil {
		return err
	}
	inner, err := operationClient(m.provider)
	if err != nil {
		m.record(err)
		return err
	}
	err = inner.CancelOperation(ctx, id)
	m.record(err)
	return err
}

func (m *CircuitBreakerMiddleware) ReadOperation(ctx context.Context, id string) (*providers.OperationAsset, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	inner, err := operationClient(m.provider)
	if err != nil {
		m.record(err)
		return nil, err
	}
	resp, err := inner.ReadOperation(ctx, id)
	m.record(err)
	return resp, err
}

func (r *RateLimitMiddleware) SubmitOperation(ctx context.Context, req *providers.OperationRequest) (*providers.Operation, error) {
	inner, err := operationClient(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return inner.SubmitOperation(ctx, req)
}

func (r *RateLimitMiddleware) GetOperation(ctx context.Context, id string) (*providers.Operation, error) {
	inner, err := operationClient(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return inner.GetOperation(ctx, id)
}

func (r *RateLimitMiddleware) CancelOperation(ctx context.Context, id string) error {
	inner, err := operationClient(r.provider)
	if err != nil {
		return err
	}
	if err := r.acquire(ctx); err != nil {
		return err
	}
	return inner.CancelOperation(ctx, id)
}

func (r *RateLimitMiddleware) ReadOperation(ctx context.Context, id string) (*providers.OperationAsset, error) {
	inner, err := operationClient(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return inner.ReadOperation(ctx, id)
}
