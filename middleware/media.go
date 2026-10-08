package middleware

import (
	"context"

	"github.com/ncobase/deebus/providers"
)

func imageGenerator(p providers.Provider) (providers.ImageGenerator, error) {
	generator, ok := p.(providers.ImageGenerator)
	if !ok {
		return nil, providers.Unsupported(p.Name(), "image generation")
	}
	return generator, nil
}

func speechSynthesizer(p providers.Provider) (providers.SpeechSynthesizer, error) {
	synth, ok := p.(providers.SpeechSynthesizer)
	if !ok {
		return nil, providers.Unsupported(p.Name(), "speech synthesis")
	}
	return synth, nil
}

func transcriber(p providers.Provider) (providers.Transcriber, error) {
	item, ok := p.(providers.Transcriber)
	if !ok {
		return nil, providers.Unsupported(p.Name(), "transcription")
	}
	return item, nil
}

func reranker(p providers.Provider) (providers.Reranker, error) {
	item, ok := p.(providers.Reranker)
	if !ok {
		return nil, providers.Unsupported(p.Name(), "rerank")
	}
	return item, nil
}

func (m *LoggingMiddleware) GenerateImage(ctx context.Context, req *providers.ImageRequest) (*providers.ImageResponse, error) {
	inner, err := imageGenerator(m.provider)
	if err != nil {
		return nil, err
	}
	return inner.GenerateImage(ctx, req)
}

func (m *LoggingMiddleware) SynthesizeSpeech(ctx context.Context, req *providers.SpeechRequest) (*providers.SpeechResponse, error) {
	inner, err := speechSynthesizer(m.provider)
	if err != nil {
		return nil, err
	}
	return inner.SynthesizeSpeech(ctx, req)
}

func (m *LoggingMiddleware) Transcribe(ctx context.Context, req *providers.TranscribeRequest) (*providers.TranscribeResponse, error) {
	inner, err := transcriber(m.provider)
	if err != nil {
		return nil, err
	}
	return inner.Transcribe(ctx, req)
}

func (m *LoggingMiddleware) Rerank(ctx context.Context, req *providers.RerankRequest) (*providers.RerankResponse, error) {
	inner, err := reranker(m.provider)
	if err != nil {
		return nil, err
	}
	return inner.Rerank(ctx, req)
}

func (m *RetryMiddleware) GenerateImage(ctx context.Context, req *providers.ImageRequest) (*providers.ImageResponse, error) {
	return retryValue(m, ctx, func() (*providers.ImageResponse, error) {
		inner, err := imageGenerator(m.provider)
		if err != nil {
			return nil, err
		}
		return inner.GenerateImage(ctx, req)
	})
}

func (m *RetryMiddleware) SynthesizeSpeech(ctx context.Context, req *providers.SpeechRequest) (*providers.SpeechResponse, error) {
	return retryValue(m, ctx, func() (*providers.SpeechResponse, error) {
		inner, err := speechSynthesizer(m.provider)
		if err != nil {
			return nil, err
		}
		return inner.SynthesizeSpeech(ctx, req)
	})
}

func (m *RetryMiddleware) Transcribe(ctx context.Context, req *providers.TranscribeRequest) (*providers.TranscribeResponse, error) {
	return retryValue(m, ctx, func() (*providers.TranscribeResponse, error) {
		inner, err := transcriber(m.provider)
		if err != nil {
			return nil, err
		}
		return inner.Transcribe(ctx, req)
	})
}

func (m *RetryMiddleware) Rerank(ctx context.Context, req *providers.RerankRequest) (*providers.RerankResponse, error) {
	return retryValue(m, ctx, func() (*providers.RerankResponse, error) {
		inner, err := reranker(m.provider)
		if err != nil {
			return nil, err
		}
		return inner.Rerank(ctx, req)
	})
}

func (m *CircuitBreakerMiddleware) GenerateImage(ctx context.Context, req *providers.ImageRequest) (*providers.ImageResponse, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	inner, err := imageGenerator(m.provider)
	if err != nil {
		m.record(err)
		return nil, err
	}
	resp, err := inner.GenerateImage(ctx, req)
	m.record(err)
	return resp, err
}

func (m *CircuitBreakerMiddleware) SynthesizeSpeech(ctx context.Context, req *providers.SpeechRequest) (*providers.SpeechResponse, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	inner, err := speechSynthesizer(m.provider)
	if err != nil {
		m.record(err)
		return nil, err
	}
	resp, err := inner.SynthesizeSpeech(ctx, req)
	m.record(err)
	return resp, err
}

func (m *CircuitBreakerMiddleware) Transcribe(ctx context.Context, req *providers.TranscribeRequest) (*providers.TranscribeResponse, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	inner, err := transcriber(m.provider)
	if err != nil {
		m.record(err)
		return nil, err
	}
	resp, err := inner.Transcribe(ctx, req)
	m.record(err)
	return resp, err
}

func (m *CircuitBreakerMiddleware) Rerank(ctx context.Context, req *providers.RerankRequest) (*providers.RerankResponse, error) {
	if err := m.allow(); err != nil {
		return nil, err
	}
	inner, err := reranker(m.provider)
	if err != nil {
		m.record(err)
		return nil, err
	}
	resp, err := inner.Rerank(ctx, req)
	m.record(err)
	return resp, err
}

func (r *RateLimitMiddleware) GenerateImage(ctx context.Context, req *providers.ImageRequest) (*providers.ImageResponse, error) {
	inner, err := imageGenerator(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return inner.GenerateImage(ctx, req)
}

func (r *RateLimitMiddleware) SynthesizeSpeech(ctx context.Context, req *providers.SpeechRequest) (*providers.SpeechResponse, error) {
	inner, err := speechSynthesizer(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return inner.SynthesizeSpeech(ctx, req)
}

func (r *RateLimitMiddleware) Transcribe(ctx context.Context, req *providers.TranscribeRequest) (*providers.TranscribeResponse, error) {
	inner, err := transcriber(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return inner.Transcribe(ctx, req)
}

func (r *RateLimitMiddleware) Rerank(ctx context.Context, req *providers.RerankRequest) (*providers.RerankResponse, error) {
	inner, err := reranker(r.provider)
	if err != nil {
		return nil, err
	}
	if err := r.acquire(ctx); err != nil {
		return nil, err
	}
	return inner.Rerank(ctx, req)
}
