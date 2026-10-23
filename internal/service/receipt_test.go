package service

import (
	"strings"
	"testing"
)

func TestValidateReceiptName(t *testing.T) {
	cases := []struct {
		name string
		size int64
		ok   bool
	}{
		{"transfer.JPG", 1 << 20, true},
		{"bukti.pdf", 4 << 20, true},
		{"bukti.webp", 100, true},
		{"bukti.gif", 100, false},
		{"script.sh", 100, false},
		{"huge.png", MaxReceiptBytes + 1, false},
	}
	for _, c := range cases {
		got, err := ValidateReceiptName(c.name, c.size)
		if c.ok {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
				continue
			}
			if !strings.HasSuffix(got, strings.ToLower(c.name[strings.LastIndex(c.name, "."):])) {
				t.Errorf("%s: stored name lost extension: %q", c.name, got)
			}
		} else if err == nil {
			t.Errorf("%s: expected rejection", c.name)
		}
	}
}
