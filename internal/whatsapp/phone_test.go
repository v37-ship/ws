package whatsapp

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"0812-3456-7890", "6281234567890"},
		{"0812 3456 7890", "6281234567890"},
		{"+62 812 3456 7890", "6281234567890"},
		{"6281234567890", "6281234567890"},
		{"812 3456 7890", "6281234567890"},
		{"(0812) 3456-7890", "6281234567890"},
		{"021-1234567", "62211234567"},
		{"", ""},
		{"   ", ""},
		{"12345", ""},
		{"12345678901234567890", ""},
		{"not a phone", ""},
	}
	for _, c := range cases {
		if got := NormalizePhone(c.in); got != c.want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMaskTarget pins the log-safe rendering (ruling R5): only the last four
// visible characters survive, a JID suffix stays readable, and anything at or
// under four characters collapses to the ellipsis alone so a short number is
// never recoverable from logs.
func TestMaskTarget(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"0812-3456-7890", "…7890"},
		{"+62 812 3456 7890", "…7890"},
		{"6281234567890", "…7890"},
		{"120363-429@g.us", "…3429@g.us"},
		{"6281234567890@s.whatsapp.net", "…7890@s.whatsapp.net"},
		{"0812-3456-7890 ", "…7890"},
		{"12345", "…2345"},
		{"1234", "…"},
		{"123", "…"},
		{"ab-cd", "…"}, // four visible characters still hide fully
		{"----", "…"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := MaskTarget(c.in); got != c.want {
			t.Errorf("MaskTarget(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
