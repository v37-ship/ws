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
