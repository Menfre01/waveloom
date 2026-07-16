package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Package logging provides a thin wrapper around log/slog.
//
// It initialises the global slog logger to write structured JSON to a
// log file and, when the configured level is Debug, human-readable text
// to stderr.
//
// Usage (once, early in main):

// Init initialises the global slog logger.
//
// - logDir: directory for the log file (created if missing).
// - level: minimum level for the file handler. When level == Debug,
//   an additional text handler writes to stderr.
//
// Returns a cleanup function that closes the log file.  If logDir
// cannot be created or the file cannot be opened, logging silently
// falls back to io.Discard for the file handler.
func Init(logDir string, level slog.Level) (cleanup func()) {
	// Ensure log directory exists.
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		// Can't create log dir → file handler discards.
		initSlog(io.Discard, level)
		return func() {}
	}

	logPath := filepath.Join(logDir, "waveloom.log")
	oldPath := logPath + ".1"

	// Rotate: waveloom.log → waveloom.log.1 (discard .2 and older).
	if _, err := os.Stat(logPath); err == nil {
		_ = os.Remove(oldPath)
		_ = os.Rename(logPath, oldPath)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		initSlog(io.Discard, level)
		return func() {}
	}

	initSlog(f, level)

	return func() {
		// Flush any buffered writes before closing.
		_ = f.Sync()
		_ = f.Close()
	}
}

// initSlog wires up the global logger with the file writer and an
// optional stderr writer.
func initSlog(fileWriter io.Writer, level slog.Level) {
	leveler := newLevelFilter(level)

	var handlers []slog.Handler

	// JSON handler for the file.
	handlers = append(handlers, slog.NewJSONHandler(fileWriter, &slog.HandlerOptions{
		Level: leveler,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(time.Now().Format(time.RFC3339))
			}
			return a
		},
	}))

	// Text handler for stderr — only when Debug level is set.
	if level <= slog.LevelDebug {
		handlers = append(handlers, slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: leveler,
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					a.Value = slog.StringValue(time.Now().Format("15:04:05.000"))
				}
				return a
			},
		}))
	}

	slog.SetDefault(slog.New(newMultiHandler(handlers...)))
}

// levelFilter wraps a slog.Leveler to implement Enabled.
type levelFilter struct {
	level slog.Level
}

func newLevelFilter(level slog.Level) *levelFilter {
	return &levelFilter{level: level}
}

func (f *levelFilter) Level() slog.Level { return f.level }

// multiHandler fans out log records to all handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) *multiHandler {
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h2 := range h.handlers {
		if h2.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h2 := range h.handlers {
		if h2.Enabled(ctx, r.Level) {
			// Clone the record so each handler gets independent state.
			if err := h2.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, h2 := range h.handlers {
		handlers[i] = h2.WithAttrs(attrs)
	}
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, h2 := range h.handlers {
		handlers[i] = h2.WithGroup(name)
	}
	return &multiHandler{handlers: handlers}
}
