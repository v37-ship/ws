package whatsapp

import (
	"strings"
	"testing"
)

func TestSegments(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantEnc  string
		wantChar int
		wantSegs int
	}{
		{
			name:     "empty body is zero segments",
			body:     "",
			wantEnc:  "GSM-7",
			wantChar: 0,
			wantSegs: 0,
		},
		{
			name:     "160 ascii fits one gsm segment",
			body:     strings.Repeat("a", 160),
			wantEnc:  "GSM-7",
			wantChar: 160,
			wantSegs: 1,
		},
		{
			name:     "161 ascii needs two gsm segments",
			body:     strings.Repeat("a", 161),
			wantEnc:  "GSM-7",
			wantChar: 161,
			wantSegs: 2,
		},
		{
			name:     "306 septets is exactly two gsm segments",
			body:     strings.Repeat("a", 306),
			wantEnc:  "GSM-7",
			wantChar: 306,
			wantSegs: 2,
		},
		{
			name:     "307 septets needs three gsm segments",
			body:     strings.Repeat("a", 307),
			wantEnc:  "GSM-7",
			wantChar: 307,
			wantSegs: 3,
		},
		{
			name:     "extension char costs two septets at the boundary",
			body:     strings.Repeat("a", 159) + "€",
			wantEnc:  "GSM-7",
			wantChar: 160,
			wantSegs: 2,
		},
		{
			name:     "extension char exactly fills one gsm segment",
			body:     strings.Repeat("a", 158) + "€",
			wantEnc:  "GSM-7",
			wantChar: 159,
			wantSegs: 1,
		},
		{
			name:     "every extension char is two septets",
			body:     "^{}\\[~]|€",
			wantEnc:  "GSM-7",
			wantChar: 9,
			wantSegs: 1,
		},
		{
			name:     "newline is a gsm character",
			body:     "a\nb",
			wantEnc:  "GSM-7",
			wantChar: 3,
			wantSegs: 1,
		},
		{
			name:     "one emoji switches the whole body to ucs-2",
			body:     "Halo Bunda 👋",
			wantEnc:  "UCS-2",
			wantChar: 12,
			wantSegs: 1,
		},
		{
			name:     "emoji surrogate pair gives 70 ucs-2 units",
			body:     strings.Repeat("a", 68) + "😀",
			wantEnc:  "UCS-2",
			wantChar: 69,
			wantSegs: 1, // 68 + 2 units = 70 fits one segment
		},
		{
			name:     "emoji surrogate pair tips past one ucs-2 segment",
			body:     strings.Repeat("a", 69) + "😀",
			wantEnc:  "UCS-2",
			wantChar: 70,
			wantSegs: 2, // 69 + 2 units = 71
		},
		{
			name:     "70 ucs-2 units is one segment",
			body:     strings.Repeat("a", 69) + "→",
			wantEnc:  "UCS-2",
			wantChar: 70,
			wantSegs: 1,
		},
		{
			name:     "71 ucs-2 units needs two segments",
			body:     strings.Repeat("a", 70) + "→",
			wantEnc:  "UCS-2",
			wantChar: 71,
			wantSegs: 2,
		},
		{
			name:     "accented text outside gsm is ucs-2",
			body:     "Café ñandú",
			wantEnc:  "UCS-2",
			wantChar: 10,
			wantSegs: 1,
		},
		{
			name:     "cedilla lowercase is outside gsm",
			body:     "ação",
			wantEnc:  "UCS-2",
			wantChar: 4,
			wantSegs: 1,
		},
		{
			name:     "curly quote is ucs-2",
			body:     "Ibu’s spa",
			wantEnc:  "UCS-2",
			wantChar: 9,
			wantSegs: 1,
		},
		{
			name:     "long mixed body counts ucs-2 units",
			body:     strings.Repeat("a", 200) + "😀",
			wantEnc:  "UCS-2",
			wantChar: 201,
			wantSegs: 4, // 202 units / 67 = 3.01 -> 4
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, chars, segs := Segments(tt.body)
			if enc != tt.wantEnc || chars != tt.wantChar || segs != tt.wantSegs {
				t.Errorf("Segments(%q) = (%s, %d, %d), want (%s, %d, %d)",
					tt.body, enc, chars, segs, tt.wantEnc, tt.wantChar, tt.wantSegs)
			}
		})
	}
}
