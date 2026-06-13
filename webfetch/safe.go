package webfetch

import (
	"io"
	"log/slog"
)

// safeClose closes c, logging any error via logger instead of panicking.
//
// It is nil-safe for both the closer and the logger: a nil closer is a no-op,
// and a nil logger falls back to slog.Default(). This exists so call sites never
// need to write `_ = c.Close()` (which silently drops errors) or a bare
// `c.Close()` (which panics on a nil receiver).
func safeClose(logger *slog.Logger, c io.Closer) {
	if c == nil {
		return
	}
	if err := c.Close(); err != nil {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("failed to close resource", slog.Any("error", err))
	}
}
