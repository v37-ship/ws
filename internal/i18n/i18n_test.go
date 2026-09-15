package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTFallback(t *testing.T) {
	if got := T("id", "hero.book"); got != "Booking sesi" {
		t.Errorf("id hero.book = %q", got)
	}
	if got := T("en", "hero.book"); got != "Book a session" {
		t.Errorf("en hero.book = %q", got)
	}
	if got := T("", "hero.book"); got != "Book a session" {
		t.Errorf("empty lang should fall back to en, got %q", got)
	}
	if got := T("id", "no.such.key"); got != "no.such.key" {
		t.Errorf("missing key should echo, got %q", got)
	}
}

func TestResolveDefaultIndonesian(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if got := Resolve(r); got != ID {
		t.Errorf("default = %q, want id", got)
	}
	r2 := httptest.NewRequest("GET", "/?lang=en", nil)
	if got := Resolve(r2); got != EN {
		t.Errorf("?lang=en = %q", got)
	}
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.AddCookie(&http.Cookie{Name: CookieName, Value: "en"})
	if got := Resolve(r3); got != EN {
		t.Errorf("cookie en = %q", got)
	}
}
