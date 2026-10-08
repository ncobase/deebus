package deebus

import (
	"sync"

	internallog "github.com/ncobase/deebus/internal/log"
)

// Logger is the structured logging interface used by the client.
// Implement this to plug in slog, zap, logrus, or any other logger.
//
// Example:
//
//	type MyLogger struct{}
//	func (l MyLogger) Info(msg string, fields ...any)  { slog.Info(msg, fields...) }
//	func (l MyLogger) Debug(msg string, fields ...any) { slog.Debug(msg, fields...) }
//	func (l MyLogger) Warn(msg string, fields ...any)  { slog.Warn(msg, fields...) }
//	func (l MyLogger) Error(msg string, fields ...any) { slog.Error(msg, fields...) }
//
//	client.SetLogger(MyLogger{})
type Logger = internallog.Logger

// NoopLogger silently discards all log output. Used as the default logger.
type NoopLogger struct{}

// Debug discards the message.
func (NoopLogger) Debug(string, ...any) {}

// Info discards the message.
func (NoopLogger) Info(string, ...any) {}

// Warn discards the message.
func (NoopLogger) Warn(string, ...any) {}

// Error discards the message.
func (NoopLogger) Error(string, ...any) {}

// sharedLogger is a thread-safe Logger holder whose backing implementation
// can be swapped at runtime via set(). All middleware that is created at
// NewClient time holds a pointer to the same sharedLogger, so a call to
// client.SetLogger propagates to every layer of the middleware stack.
type sharedLogger struct {
	mu sync.RWMutex
	l  Logger
}

func newSharedLogger(l Logger) *sharedLogger {
	return &sharedLogger{l: l}
}

func (s *sharedLogger) set(l Logger) {
	s.mu.Lock()
	s.l = l
	s.mu.Unlock()
}

// Debug forwards to the current logger.
func (s *sharedLogger) Debug(msg string, fields ...any) {
	s.mu.RLock()
	l := s.l
	s.mu.RUnlock()
	l.Debug(msg, fields...)
}

// Info forwards to the current logger.
func (s *sharedLogger) Info(msg string, fields ...any) {
	s.mu.RLock()
	l := s.l
	s.mu.RUnlock()
	l.Info(msg, fields...)
}

// Warn forwards to the current logger.
func (s *sharedLogger) Warn(msg string, fields ...any) {
	s.mu.RLock()
	l := s.l
	s.mu.RUnlock()
	l.Warn(msg, fields...)
}

// Error forwards to the current logger.
func (s *sharedLogger) Error(msg string, fields ...any) {
	s.mu.RLock()
	l := s.l
	s.mu.RUnlock()
	l.Error(msg, fields...)
}
