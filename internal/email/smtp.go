package email

import (
	"fmt"
	"net/smtp"
	"strings"
)

// Sender delivers transactional mail over SMTP.
// Without credentials it speaks plain unauthenticated SMTP (Mailpit local
// dev). With Username/Password it authenticates via PLAIN over STARTTLS,
// which is what Gmail's smtp.gmail.com:587 requires for App Password relay.
type Sender struct {
	Host     string
	Port     int
	From     string
	Username string
	Password string
}

func NewSender(host string, port int, from string) *Sender {
	return &Sender{Host: host, Port: port, From: from}
}

func NewAuthenticatedSender(host string, port int, from, username, password string) *Sender {
	return &Sender{Host: host, Port: port, From: from, Username: username, Password: password}
}

func (s *Sender) addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// auth returns nil for credential-less relays (Mailpit), keeping local dev
// byte-identical to before. Go's SendMail upgrades to STARTTLS on its own
// when the server advertises it (Gmail :587 does).
func (s *Sender) auth() smtp.Auth {
	if s.Username == "" {
		return nil
	}
	return smtp.PlainAuth("", s.Username, s.Password, s.Host)
}

func buildMessage(from, to, subject, body string) []byte {
	// Minimal headers; subject is server-controlled (no user input), so no
	// header-injection scrubbing is needed today.
	return []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
		strings.TrimSpace(from), strings.TrimSpace(to), strings.TrimSpace(subject), body))
}

func (s *Sender) Send(to, subject, body string) error {
	return smtp.SendMail(s.addr(), s.auth(), s.From, []string{to}, buildMessage(s.From, to, subject, body))
}
