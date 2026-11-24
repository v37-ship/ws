package whatsapp

import (
	"strings"
	"unicode"
)

// NormalizePhone converts an Indonesian phone number to the digits-only form
// WhatsApp expects (country code, no plus or separators):
//
//	"0812-3456-7890"      -> "6281234567890"
//	"+62 812 3456 7890"   -> "6281234567890"
//	"812 3456 7890"       -> "6281234567890"  (common local shorthand)
//
// It returns "" for input that cannot be a usable number, so callers can skip
// the send instead of handing Evolution garbage.
func NormalizePhone(raw string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, raw)
	switch {
	case strings.HasPrefix(digits, "62"):
		// already international
	case strings.HasPrefix(digits, "0"):
		digits = "62" + strings.TrimPrefix(digits, "0")
	case strings.HasPrefix(digits, "8"):
		digits = "62" + digits
	default:
		return ""
	}
	// 62 + 8..13 subscriber digits; anything shorter/longer is a typo.
	if len(digits) < 10 || len(digits) > 15 {
		return ""
	}
	return digits
}

// MaskTarget renders a send destination safe to log: only the last four
// visible (letter or digit) characters before the JID suffix survive, prefixed
// with an ellipsis. A number or group id of four characters or fewer masks
// completely, and an empty target stays empty. A WhatsApp JID suffix
// (`@g.us`, `@s.whatsapp.net`) is preserved so support can still tell a group
// from a number — nothing else about the destination is recoverable (ruling
// R5: a mistyped number must not be resurrectable from logs).
func MaskTarget(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	local, suffix := s, ""
	if i := strings.IndexByte(s, '@'); i >= 0 {
		local, suffix = s[:i], s[i:]
	}
	visible := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, local)
	runes := []rune(visible)
	if len(runes) <= 4 {
		return "…" + suffix
	}
	return "…" + string(runes[len(runes)-4:]) + suffix
}
