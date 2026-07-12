package main

import (
	"testing"

	"github.com/KilimcininKorOglu/M365Bridge/pkg/logging"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  logging.LogLevel
	}{
		{name: "default", want: logging.LevelInfo},
		{name: "info", value: "info", want: logging.LevelInfo},
		{name: "debug", value: " DEBUG ", want: logging.LevelDebug},
		{name: "warn", value: "warning", want: logging.LevelWarn},
		{name: "error", value: "error", want: logging.LevelError},
		{name: "unknown", value: "verbose", want: logging.LevelInfo},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseLogLevel(test.value); got != test.want {
				t.Fatalf("parseLogLevel(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}
