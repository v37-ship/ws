package i18n

import (
	"net/http"
	"strings"
)

const (
	EN         = "en"
	ID         = "id"
	Default    = ID
	CookieName = "wangga-lang"
)

// T returns the translation for lang/key, falling back to English then key.
func T(lang, key string) string {
	if m, ok := dict[normalize(lang)]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	if v, ok := dict[EN][key]; ok {
		return v
	}
	return key
}

// Resolve picks the request language: ?lang= wins (caller persists cookie),
// then cookie, then Accept-Language, defaulting to Indonesian.
func Resolve(r *http.Request) string {
	if r != nil {
		if q := normalize(r.URL.Query().Get("lang")); q != "" {
			return q
		}
		if c, err := r.Cookie(CookieName); err == nil {
			if l := normalize(c.Value); l != "" {
				return l
			}
		}
		al := strings.ToLower(r.Header.Get("Accept-Language"))
		if strings.Contains(al, "en") && !strings.Contains(al, "id") {
			// Only prefer English when Indonesian is absent.
			// e.g. "en-US,en;q=0.9" -> en, "id,en;q=0.8" -> id.
			return EN
		}
	}
	return Default
}

// QueryOverride reports whether ?lang= carried a valid language.
func QueryOverride(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	if l := normalize(r.URL.Query().Get("lang")); l != "" {
		return l, true
	}
	return "", false
}

func SetCookie(w http.ResponseWriter, lang string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: normalize(lang), Path: "/",
		MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode, Secure: secure,
	})
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(s, "id"), s == "in", strings.HasPrefix(s, "in-"):
		return ID
	case strings.HasPrefix(s, "en"):
		return EN
	default:
		return ""
	}
}
