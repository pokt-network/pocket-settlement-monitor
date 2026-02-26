package logging

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Logger is the application-wide logger type.
type Logger = zerolog.Logger

// NewLogger creates a new zerolog logger with the given level and format.
func NewLogger(level, format string) Logger {
	var w io.Writer
	if strings.EqualFold(format, "console") || strings.EqualFold(format, "text") {
		w = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}
	} else {
		w = os.Stderr
	}

	lvl, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil {
		lvl = zerolog.InfoLevel
	}

	return zerolog.New(w).Level(lvl).With().Timestamp().Logger()
}

// ForComponent returns a sub-logger tagged with the given component name.
func ForComponent(parent Logger, component string) Logger {
	return parent.With().Str("component", component).Logger()
}
