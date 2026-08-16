package email

import (
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
)

// Sender delivers transactional email via SMTP. When no host is configured it
// logs messages instead of sending (development mode).
type Sender struct {
	host string
	port string
	user string
	pass string
	from string
	log  *slog.Logger
}

func New(host, port, user, pass, from string) *Sender {
	return &Sender{host: host, port: port, user: user, pass: pass, from: from, log: slog.Default()}
}

func (s *Sender) Send(to, subject, body string) error {
	if s.host == "" {
		s.log.Info("email (log-only)", "to", to, "subject", subject)
		return nil
	}
	from := s.from
	if from == "" {
		from = "pocketpds@" + s.host
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from, to, subject, body)

	addr := net.JoinHostPort(s.host, s.port)
	var auth smtp.Auth
	if s.user != "" {
		auth = smtp.PlainAuth("", s.user, s.pass, s.host)
	}
	return smtp.SendMail(addr, auth, from, []string{to}, []byte(strings.ReplaceAll(msg, "\n", "\r\n")))
}
