package handlers

import (
	"log/slog"
	"testing"

	"spa-booking/internal/config"
)

// TestTemplatesParse ensures all embedded html/template files parse with the
// production FuncMap. New only parses templates, so nil services are fine.
func TestTemplatesParse(t *testing.T) {
	_, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
}
