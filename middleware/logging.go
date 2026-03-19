package middleware

import (
	"context"
	"time"

	"github.com/ncobase/deebus/internal/log"
	"github.com/ncobase/deebus/providers"
)

// LoggingMiddleware logs the start, outcome, and duration of every provider call.
type LoggingMiddleware struct {
	provider providers.Provider
	logger   log.Logger
}

// NewLogging wraps p with structured request/response logging.
func NewLogging(p providers.Provider, logger log.Logger) *LoggingMiddleware {
	return &LoggingMiddleware{provider: p, logger: logger}
}

func (m *LoggingMiddleware) Name() string { return m.provider.Name() }

func (m *LoggingMiddleware) Complete(ctx context.Context, req *providers.Request) (*providers.Response, error) {
	start := time.Now()
	m.logger.Debug("complete.start",
		"provider", m.provider.Name(),
		"model", req.Model,
		"messages", len(req.Messages),
	)

	resp, err := m.provider.Complete(ctx, req)

	dur := time.Since(start)
	if err != nil {
		m.logger.Error("complete.error",
			"provider", m.provider.Name(),
			"model", req.Model,
			"duration", dur,
			"error", err,
		)
	} else {
		m.logger.Info("complete.ok",
			"provider", m.provider.Name(),
			"model", resp.Model,
			"duration", dur,
			"tokens", resp.TokensUsed,
			"finish_reason", resp.FinishReason,
		)
	}

	return resp, err
}

func (m *LoggingMiddleware) Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error) {
	start := time.Now()
	m.logger.Debug("stream.start", "provider", m.provider.Name(), "model", req.Model)

	ch, err := m.provider.Stream(ctx, req)
	if err != nil {
		m.logger.Error("stream.error",
			"provider", m.provider.Name(),
			"model", req.Model,
			"duration", time.Since(start),
			"error", err,
		)
		return nil, err
	}

	m.logger.Info("stream.opened",
		"provider", m.provider.Name(),
		"model", req.Model,
		"duration", time.Since(start),
	)
	return ch, nil
}

func (m *LoggingMiddleware) Embed(ctx context.Context, req *providers.EmbedRequest) (*providers.EmbedResponse, error) {
	start := time.Now()
	m.logger.Debug("embed.start", "provider", m.provider.Name(), "model", req.Model)

	resp, err := m.provider.Embed(ctx, req)

	dur := time.Since(start)
	if err != nil {
		m.logger.Error("embed.error",
			"provider", m.provider.Name(),
			"model", req.Model,
			"duration", dur,
			"error", err,
		)
	} else {
		m.logger.Info("embed.ok",
			"provider", m.provider.Name(),
			"model", req.Model,
			"duration", dur,
			"tokens", resp.TokensUsed,
		)
	}

	return resp, err
}

func (m *LoggingMiddleware) Health(ctx context.Context) error {
	return m.provider.Health(ctx)
}
