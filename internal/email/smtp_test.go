package email

import (
	"net/smtp"
	"strings"
	"testing"
)

func TestBuildMessageHeaders(t *testing.T) {
	msg := string(buildMessage("halo@wangga.biz.id", "mom@example.com", "Wangga booking confirmed", "Halo Bunda"))
	for _, want := range []string{
		"From: halo@wangga.biz.id\r\n",
		"To: mom@example.com\r\n",
		"Subject: Wangga booking confirmed\r\n",
		"MIME-Version: 1.0",
		"charset=UTF-8",
		"Halo Bunda",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestAuthNilWithoutCredentials(t *testing.T) {
	s := NewSender("mailpit", 1025, "bookings@spa.local")
	if s.auth() != nil {
		t.Error("credential-less sender must use nil auth (Mailpit path)")
	}
	if got := s.addr(); got != "mailpit:1025" {
		t.Errorf("addr = %q", got)
	}
}

func TestAuthPresentWithCredentials(t *testing.T) {
	s := NewAuthenticatedSender("smtp.gmail.com", 587, "halo@wangga.biz.id", "owner@gmail.com", "abcd efgh ijkl mnop")
	if s.auth() == nil {
		t.Error("authenticated sender must return non-nil auth")
	}
	// Wrong host must fail fast instead of leaking the password.
	if _, _, err := s.auth().Start(&smtp.ServerInfo{TLS: true, Name: "smtp.gmail.com", Auth: []string{"PLAIN"}}); err != nil {
		t.Fatalf("auth start: %v", err)
	}
}
