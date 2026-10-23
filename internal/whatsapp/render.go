package whatsapp

import (
	"fmt"
	"strings"
)

// Payload is the decoded `wa_outbox.payload` JSON document.
type Payload map[string]any

// messageTemplates is the registry of rendered copy. Phase 1 fills it with the
// booking messages; until then an outbox row may carry a literal `text` in its
// payload so the pipeline can be exercised end to end.
var messageTemplates = map[string]func(Payload) string{}

// Render returns the message body for a template key.
func Render(key string, payload Payload) (string, error) {
	if fn, ok := messageTemplates[key]; ok {
		if body := strings.TrimSpace(fn(payload)); body != "" {
			return body, nil
		}
		return "", fmt.Errorf("template %q rendered empty", key)
	}
	if body, ok := payload["text"].(string); ok && strings.TrimSpace(body) != "" {
		return strings.TrimSpace(body), nil
	}
	return "", fmt.Errorf("unknown template %q", key)
}

// str reads a payload field as a string (empty when absent/not a string).
func str(p Payload, key string) string {
	s, _ := p[key].(string)
	return s
}

// int64Val reads a numeric payload field, tolerating JSON float decoding.
func int64Val(p Payload, key string) int64 {
	switch v := p[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}
