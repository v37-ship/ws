package whatsapp

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Payload is the decoded `wa_outbox.payload` JSON document.
type Payload map[string]any

// messageTemplates is the registry of rendered copy. All customer copy is
// Indonesian; the brand name stays identical in both languages.
var messageTemplates = map[string]func(Payload) string{
	"booking_confirm": func(p Payload) string {
		var b strings.Builder
		fmt.Fprintf(&b, "Halo %s 👋\n", str(p, "name"))
		fmt.Fprintf(&b, "Booking *%s* sudah dikonfirmasi.\n\n", str(p, "code"))
		fmt.Fprintf(&b, "✨ %s\n", str(p, "service"))
		fmt.Fprintf(&b, "📅 %s\n", str(p, "when"))
		fmt.Fprintf(&b, "👩‍⚕️ %s · 📍 %s\n", str(p, "staff"), str(p, "location"))
		if homes := str(p, "address"); homes != "" {
			fmt.Fprintf(&b, "🏠 %s\n", homes)
		}
		fmt.Fprintf(&b, "Total: *Rp %s*\n\n", rp(int64Val(p, "amount")))
		if boolean(p, "paid") {
			b.WriteString("Status: LUNAS ✅\n")
		} else if link := str(p, "pay_url"); link != "" {
			fmt.Fprintf(&b, "Bayar sekarang via QRIS:\n%s\n", link)
			b.WriteString("atau bayar di kasir saat kunjungan.\n")
		} else {
			b.WriteString("Pembayaran bisa dilakukan di kasir saat kunjungan.\n")
		}
		b.WriteString("\nSampai jumpa! — Wangga")
		return b.String()
	},
	"payment_receipt": func(p Payload) string {
		var b strings.Builder
		b.WriteString("Pembayaran diterima ✅\n\n")
		fmt.Fprintf(&b, "*%s* · Rp %s · %s\n", str(p, "code"), rp(int64Val(p, "amount")), str(p, "method"))
		if when := str(p, "paid_at"); when != "" {
			fmt.Fprintf(&b, "%s\n", when)
		}
		if note := str(p, "note"); note != "" {
			fmt.Fprintf(&b, "Catatan: %s\n", note)
		}
		b.WriteString("\nLunas. Terima kasih! — Wangga")
		return b.String()
	},
	"staff_paid_claim": func(p Payload) string {
		var b strings.Builder
		b.WriteString("💳 Klaim bayar — ")
		b.WriteString(str(p, "code"))
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s · %s\n", str(p, "name"), str(p, "service"))
		fmt.Fprintf(&b, "%s · Rp %s\n", str(p, "when"), rp(int64Val(p, "amount")))
		if link := str(p, "portal_url"); link != "" {
			fmt.Fprintf(&b, "Cek: %s\n", link)
		}
		return b.String()
	},
	"claim_rejected": func(p Payload) string {
		return fmt.Sprintf("Bukti pembayaran *%s* belum kami terima. Mohon kirim ulang bukti transfer ke nomor ini, atau bayar di kasir. — Wangga",
			str(p, "code"))
	},
	"booking_remind": func(p Payload) string {
		var b strings.Builder
		fmt.Fprintf(&b, "Halo %s 👋\n", str(p, "name"))
		b.WriteString("Pengingat jadwal kunjungan Anda:\n\n")
		fmt.Fprintf(&b, "✨ %s\n", str(p, "service"))
		fmt.Fprintf(&b, "📅 %s\n", str(p, "when"))
		fmt.Fprintf(&b, "👩‍⚕️ %s · 📍 %s\n", str(p, "staff"), str(p, "location"))
		if homes := str(p, "address"); homes != "" {
			fmt.Fprintf(&b, "🏠 %s\n", homes)
		}
		fmt.Fprintf(&b, "Total: *Rp %s*\n\n", rp(int64Val(p, "amount")))
		b.WriteString("Kalau tidak bisa hadir, mohon kabari kami ya.\n")
		if link := str(p, "pay_url"); link != "" {
			fmt.Fprintf(&b, "Detail & pembatalan:\n%s\n", link)
		}
		b.WriteString("\nSampai jumpa! — Wangga")
		return b.String()
	},
	"booking_cancelled": func(p Payload) string {
		var b strings.Builder
		fmt.Fprintf(&b, "Halo %s 👋\n", str(p, "name"))
		fmt.Fprintf(&b, "Booking *%s* (%s) pada %s sudah dibatalkan.\n", str(p, "code"), str(p, "service"), str(p, "when"))
		if note := str(p, "note"); note != "" {
			fmt.Fprintf(&b, "Catatan: %s\n", note)
		}
		b.WriteString("\nKalau sudah ada pembayaran, tim kami akan menghubungi soal pengembalian dana.\n")
		b.WriteString("Sampai jumpa lain waktu — Wangga")
		return b.String()
	},
	"booking_rescheduled": func(p Payload) string {
		var b strings.Builder
		fmt.Fprintf(&b, "Halo %s 👋\n", str(p, "name"))
		fmt.Fprintf(&b, "Jadwal booking *%s* (%s) sudah diubah.\n\n", str(p, "code"), str(p, "service"))
		if old := str(p, "old_when"); old != "" {
			fmt.Fprintf(&b, "Sebelumnya: %s\n", old)
		}
		fmt.Fprintf(&b, "Sekarang: *%s*\n", str(p, "when"))
		fmt.Fprintf(&b, "👩‍⚕️ %s · 📍 %s\n", str(p, "staff"), str(p, "location"))
		b.WriteString("\nSampai jumpa! — Wangga")
		return b.String()
	},
	"booking_expired": func(p Payload) string {
		var b strings.Builder
		fmt.Fprintf(&b, "Halo %s 👋\n", str(p, "name"))
		fmt.Fprintf(&b, "Booking *%s* (%s) pada %s hangus karena belum diselesaikan dalam batas waktu.\n", str(p, "code"), str(p, "service"), str(p, "when"))
		b.WriteString("\nSilakan booking ulang kapan saja ya.\n")
		if link := str(p, "book_url"); link != "" {
			fmt.Fprintf(&b, "%s\n", link)
		}
		b.WriteString("\n— Wangga")
		return b.String()
	},
	"review_request": func(p Payload) string {
		var b strings.Builder
		fmt.Fprintf(&b, "Terima kasih sudah mempercayakan perawatan Anda kepada kami, %s 🙏\n\n", str(p, "name"))
		if svc := str(p, "service"); svc != "" {
			fmt.Fprintf(&b, "✨ %s", svc)
			if staff := str(p, "staff"); staff != "" {
				fmt.Fprintf(&b, " · 👩‍⚕️ %s", staff)
			}
			b.WriteString("\n")
		}
		b.WriteString("\nCerita Anda sangat membantu bunda lain. Yuk tulis ulasan singkat di Google:\n")
		if link := str(p, "review_url"); link != "" {
			fmt.Fprintf(&b, "%s\n", link)
		}
		b.WriteString("\n— Wangga")
		return b.String()
	},
	"staff_booking": func(p Payload) string {
		var b strings.Builder
		fmt.Fprintf(&b, "🆕 Booking baru — %s\n\n", str(p, "when"))
		fmt.Fprintf(&b, "✨ %s\n", str(p, "service"))
		fmt.Fprintf(&b, "👩‍⚕️ %s\n", str(p, "staff"))
		fmt.Fprintf(&b, "👤 %s · %s\n", str(p, "name"), str(p, "phone"))
		if homes := str(p, "address"); homes != "" {
			b.WriteString("🏠 " + homes)
			if band := str(p, "band"); band != "" {
				fmt.Fprintf(&b, " (%s)", band)
			}
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Total: *Rp %s*", rp(int64Val(p, "amount")))
		if boolean(p, "paid") {
			b.WriteString(" · LUNAS ✅\n")
		} else {
			b.WriteString(" · BELUM BAYAR\n")
		}
		if src := str(p, "source"); src != "" {
			fmt.Fprintf(&b, "Sumber: %s\n", src)
		}
		if link := str(p, "portal_url"); link != "" {
			fmt.Fprintf(&b, "Detail: %s\n", link)
		}
		return b.String()
	},
	"staff_cancelled": func(p Payload) string {
		var b strings.Builder
		fmt.Fprintf(&b, "❌ %s — %s\n\n", str(p, "label"), str(p, "when"))
		fmt.Fprintf(&b, "✨ %s · 👩‍⚕️ %s\n", str(p, "service"), str(p, "staff"))
		fmt.Fprintf(&b, "👤 %s · oleh %s\n", str(p, "name"), str(p, "actor"))
		b.WriteString("Slot ini sudah kosong.\n")
		if link := str(p, "portal_url"); link != "" {
			fmt.Fprintf(&b, "Detail: %s\n", link)
		}
		return b.String()
	},
	"staff_rescheduled": func(p Payload) string {
		var b strings.Builder
		fmt.Fprintf(&b, "🔁 %s — %s\n\n", str(p, "label"), str(p, "service"))
		if old := str(p, "old_when"); old != "" {
			fmt.Fprintf(&b, "Dari: %s\n", old)
		}
		fmt.Fprintf(&b, "Ke: *%s*\n", str(p, "when"))
		fmt.Fprintf(&b, "👩‍⚕️ %s · 👤 %s\n", str(p, "staff"), str(p, "name"))
		if link := str(p, "portal_url"); link != "" {
			fmt.Fprintf(&b, "Detail: %s\n", link)
		}
		return b.String()
	},
	"staff_digest": func(p Payload) string {
		var b strings.Builder
		fmt.Fprintf(&b, "☀️ *Agenda Wangga* — %s\n\n", str(p, "date"))
		items := list(p, "items")
		if len(items) == 0 {
			b.WriteString("_Belum ada booking hari ini._\n")
		}
		for _, it := range items {
			fmt.Fprintf(&b, "• %s ✨ %s · 👩‍⚕️ %s · 👤 %s", str(it, "time"), str(it, "service"), str(it, "staff"), str(it, "who"))
			if place := str(it, "place"); place != "" {
				fmt.Fprintf(&b, " · %s", place)
			}
			if pay := str(it, "pay"); pay != "" {
				fmt.Fprintf(&b, " · %s", pay)
			}
			b.WriteString("\n")
		}
		if claims := int64Val(p, "claims"); claims > 0 {
			fmt.Fprintf(&b, "💳 Klaim bayar menunggu verifikasi: %d\n", claims)
		}
		fmt.Fprintf(&b, "Besok: %d booking\n", int64Val(p, "tomorrow"))
		if link := str(p, "portal_url"); link != "" {
			fmt.Fprintf(&b, "Portal: %s\n", link)
		}
		return b.String()
	},
}

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

// list reads an array payload field; JSON decodes arrays as []any.
func list(p Payload, key string) []Payload {
	if typed, ok := p[key].([]Payload); ok {
		return typed
	}
	raw, ok := p[key].([]any)
	if !ok {
		return nil
	}
	out := make([]Payload, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// str reads a payload field as a string (empty when absent/not a string).
func str(p Payload, key string) string {
	s, _ := p[key].(string)
	return s
}

// boolean reads a payload flag, tolerating JSON decoding.
func boolean(p Payload, key string) bool {
	switch v := p[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// rp formats an amount with Indonesian thousands separators (no prefix).
func rp(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if n < 0 {
		return "-" + b.String()
	}
	return b.String()
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

// ---- editable copy (admin overrides) ----
//
// Only customer-facing templates are editable; the staff group pings keep
// their code copy because they are operational, not brand voice. An override
// is a body with {placeholders}; the allowlist is the set of fields the
// template actually reads, so a typo can never ship a message with a literal
// "{nmae}" in it.

var editableTemplates = []string{
	"booking_confirm",
	"payment_receipt",
	"claim_rejected",
	"booking_remind",
	"booking_cancelled",
	"booking_rescheduled",
	"booking_expired",
	"review_request",
}

var templateFields = map[string][]string{
	"booking_confirm":     {"name", "code", "service", "when", "staff", "location", "address", "amount", "paid", "pay_url"},
	"payment_receipt":     {"code", "amount", "method", "paid_at", "note"},
	"claim_rejected":      {"code"},
	"booking_remind":      {"name", "service", "when", "staff", "location", "address", "amount", "pay_url"},
	"booking_cancelled":   {"name", "code", "service", "when", "note"},
	"booking_rescheduled": {"name", "code", "service", "old_when", "when", "staff", "location"},
	"booking_expired":     {"name", "code", "service", "when", "book_url"},
	"review_request":      {"name", "service", "staff", "review_url"},
}

// sampleValues is realistic data for the admin preview and the default-copy
// render; keys mirror the payload the services build.
var sampleValues = map[string]any{
	"name":       "Bunda Sari",
	"phone":      "6281234567890",
	"code":       "SPA-1a2b3c4d5e6f7a8b",
	"service":    "Baby Massage",
	"staff":      "Bidan Ayu",
	"when":       "Senin, 26 Oktober 2026 · 10:45 WIB",
	"old_when":   "Minggu, 25 Oktober 2026 · 09:00 WIB",
	"location":   "Kayu Putih",
	"address":    "Jl. Melati No. 5, Jakarta Timur",
	"band":       "≤5 km",
	"source":     "web",
	"amount":     int64(175000),
	"paid":       false,
	"pay_url":    "https://wangga.biz.id/bookings/example/pay",
	"book_url":   "https://wangga.biz.id/book",
	"review_url": "https://g.page/r/CTLPrFudX25eEBM/review",
	"portal_url": "https://wangga.biz.id/portal/bookings",
	"method":     "QRIS",
	"paid_at":    "Senin, 26 Oktober 2026 · 10:50 WIB",
	"note":       "Tolong siapkan ruangan yang hangat.",
	"label":      "BATAL",
	"actor":      "customer",
	"date":       "Senin, 26 Oktober 2026",
	"claims":     int64(2),
	"tomorrow":   int64(3),
}

var placeholderRe = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// EditableTemplates lists the customer templates whose copy an admin may
// override, in display order.
func EditableTemplates() []string {
	return append([]string(nil), editableTemplates...)
}

// Editable reports whether a template's copy may be overridden.
func Editable(key string) bool { return templateFields[key] != nil }

// Fields lists the {placeholders} a template accepts.
func Fields(key string) []string { return templateFields[key] }

// SamplePayload returns preview data for a template (empty for unknown keys).
func SamplePayload(key string) Payload {
	out := Payload{}
	for _, f := range templateFields[key] {
		if v, ok := sampleValues[f]; ok {
			out[f] = v
		}
	}
	return out
}

// DefaultCopy renders the built-in copy for a template with sample data.
func DefaultCopy(key string) string {
	body, _ := Render(key, SamplePayload(key))
	return body
}

// ValidateOverride checks an admin-supplied body: known template, non-empty,
// bounded length, and only placeholders the template accepts.
func ValidateOverride(key, body string) error {
	fields, ok := templateFields[key]
	if !ok {
		return fmt.Errorf("template %q is not editable", key)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("message cannot be empty")
	}
	if utf8.RuneCountInString(body) > 1000 {
		return errors.New("message is too long (max 1000 characters)")
	}
	allowed := make(map[string]bool, len(fields))
	for _, f := range fields {
		allowed[f] = true
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(body, -1) {
		if !allowed[m[1]] {
			return fmt.Errorf("unknown placeholder {%s}", m[1])
		}
	}
	return nil
}

// RenderOverride renders an admin body, substituting {placeholders} from the
// payload. It re-validates, so a stored body can never inject a bad field.
func RenderOverride(key, body string, payload Payload) (string, error) {
	if err := ValidateOverride(key, body); err != nil {
		return "", err
	}
	out := placeholderRe.ReplaceAllStringFunc(body, func(m string) string {
		return fieldValue(payload, m[1:len(m)-1])
	})
	return strings.TrimSpace(out), nil
}

// fieldValue formats one placeholder for the message body.
func fieldValue(p Payload, key string) string {
	switch key {
	case "amount":
		return rp(int64Val(p, key))
	case "paid":
		if boolean(p, key) {
			return "LUNAS"
		}
		return "BELUM BAYAR"
	case "claims", "tomorrow":
		return fmt.Sprintf("%d", int64Val(p, key))
	default:
		return str(p, key)
	}
}
