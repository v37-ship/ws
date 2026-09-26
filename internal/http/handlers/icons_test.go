package handlers

import (
	"strings"
	"testing"
)

// TestIconNames guards the icon set: every name referenced by templates must
// exist, and every entry must be a bare SVG fragment (the <svg> wrapper lives
// in Icon). See icons.go for the emoji-vs-SVG policy.
func TestIconNames(t *testing.T) {
	used := []string{
		"plus", "hospital", "grid", "calendar", "users", "user",
		"user-plus", "sparkles", "utensils", "image", "ticket", "crown",
		"shield", "chart", "cash", "qris", "chat", "pin", "phone",
		"mail", "clock", "instagram", "home", "building", "briefcase",
		"play", "check-circle", "alert", "x-circle", "printer",
		"download", "key", "info", "smile", "heart",
	}
	if len(used) != len(icons) {
		t.Errorf("test lists %d icons but map has %d; keep them in sync", len(used), len(icons))
	}
	for _, name := range used {
		body, ok := icons[name]
		if !ok {
			t.Errorf("template uses unknown icon %q", name)
			continue
		}
		if body == "" || strings.Contains(body, "<svg") || strings.Contains(body, "fill=") {
			t.Errorf("icon %q must be a bare stroke fragment", name)
		}
	}
	for name := range icons {
		found := false
		for _, u := range used {
			if u == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("icon %q is defined but never used", name)
		}
	}
	if out := Icon("nope"); out != "" {
		t.Errorf("unknown icon must render empty, got %q", out)
	}
	if out := Icon("chat"); !strings.HasPrefix(string(out), "<svg") || !strings.HasSuffix(string(out), "</svg>") {
		t.Errorf("icon must render a wrapped svg, got %q", out)
	}
}
