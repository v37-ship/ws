package whatsapp

import "strings"

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
