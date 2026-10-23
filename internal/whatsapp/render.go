package whatsapp

import (
	"fmt"
	"strings"
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
