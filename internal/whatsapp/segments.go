package whatsapp

import (
	"unicode/utf8"
)

// WhatsApp segments a message by the GSM-7 or UCS-2 alphabet. The limits below
// are the real client limits: a single GSM-7 message carries 160 septets and a
// concatenated one 153 per part; UCS-2 drops to 70/67 UTF-16 units because of
// the 16-bit header WhatsApp puts on every part.
const (
	gsmSingle    = 160
	gsmMultipart = 153
	ucsSingle    = 70
	ucsMultipart = 67
)

// gsm7Basic is the GSM 03.38 basic character set, including the Latin letters,
// the space and the two line-control characters. A rune outside this set (and
// outside gsm7Extension) forces the whole message into UCS-2.
const gsm7Basic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"

// gsm7Extension holds the characters that ride in an escape pair, costing two
// septets each.
const gsm7Extension = "^{}\\[~]|€"

var (
	gsmBasic     = runeSet(gsm7Basic)
	gsmExtension = runeSet(gsm7Extension)
)

func runeSet(s string) map[rune]bool {
	m := make(map[rune]bool, len(s))
	for _, r := range s {
		m[r] = true
	}
	return m
}

// Segments reports how WhatsApp will split a message body. encoding is "GSM-7"
// or "UCS-2"; chars is the rune count for display; segments is the number of
// chat bubbles (0 for an empty body, which is never sent).
func Segments(body string) (encoding string, chars, segments int) {
	chars = utf8.RuneCountInString(body)
	if chars == 0 {
		return "GSM-7", 0, 0
	}

	units := 0
	gsm := true
	for _, r := range body {
		switch {
		case gsmBasic[r]:
			units++
		case gsmExtension[r]:
			units += 2
		default:
			gsm = false
		}
		if !gsm {
			break
		}
	}
	if gsm {
		return "GSM-7", chars, segmentCount(units, gsmSingle, gsmMultipart)
	}

	// UCS-2 counts UTF-16 code units: a rune above the BMP (an emoji) is a
	// surrogate pair and therefore two units.
	units = 0
	for _, r := range body {
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return "UCS-2", chars, segmentCount(units, ucsSingle, ucsMultipart)
}

// segmentCount returns 1 while units fit a single message, else the number of
// parts at the concatenated limit.
func segmentCount(units, single, multipart int) int {
	if units <= single {
		return 1
	}
	return (units + multipart - 1) / multipart
}
