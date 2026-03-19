// Package log defines the shared Logger interface used across deebus packages.
// Placing it here breaks the circular import that would otherwise arise between
// the middleware and root deebus packages.
package log

// Logger is a minimal structured logging interface.
type Logger interface {
	Debug(msg string, fields ...any)
	Info(msg string, fields ...any)
	Warn(msg string, fields ...any)
	Error(msg string, fields ...any)
}
