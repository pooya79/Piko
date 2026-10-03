package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type Config struct {
	Address, From, Username, Password string
	RequireTLS                        bool
}

type Sender struct{ config Config }

func NewSender(config Config) (*Sender, error) {
	host, _, err := net.SplitHostPort(config.Address)
	if err != nil || host == "" {
		return nil, fmt.Errorf("SMTP address must include a host and port")
	}
	from, err := mail.ParseAddress(config.From)
	if err != nil || from.Address != config.From || strings.ContainsAny(config.From, "\r\n") {
		return nil, fmt.Errorf("invalid SMTP sender address")
	}
	if (config.Username == "") != (config.Password == "") {
		return nil, fmt.Errorf("SMTP username and password must be set together")
	}
	return &Sender{config: config}, nil
}

// Send requires STARTTLS when configured, so production never silently downgrades.
func (s *Sender) Send(ctx context.Context, to, subject, body string) error {
	if strings.ContainsAny(to+subject, "\r\n") {
		return fmt.Errorf("invalid mail header")
	}
	addr, err := mail.ParseAddress(to)
	if err != nil || addr.Address != to {
		return fmt.Errorf("invalid recipient address")
	}
	host, _, _ := net.SplitHostPort(s.config.Address)
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", s.config.Address)
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(20 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set SMTP deadline: %w", err)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("start SMTP client: %w", err)
	}
	defer client.Close()
	if s.config.RequireTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("negotiate SMTP STARTTLS: %w", err)
		}
	}
	if s.config.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.config.Username, s.config.Password, host)); err != nil {
			return fmt.Errorf("authenticate to SMTP server: %w", err)
		}
	}
	if err := client.Mail(s.config.From); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("open SMTP message: %w", err)
	}
	// Mail headers must be ASCII, and quoted-printable carries Persian body text
	// without relying on the receiving SMTP server's 8BITMIME support.
	var message strings.Builder
	message.WriteString("From: " + s.config.From + "\r\nTo: " + to + "\r\nSubject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	encodedBody := quotedprintable.NewWriter(&message)
	if _, err := io.WriteString(encodedBody, strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")); err != nil {
		return fmt.Errorf("encode SMTP body: %w", err)
	}
	if err := encodedBody.Close(); err != nil {
		return fmt.Errorf("finish SMTP body: %w", err)
	}
	if _, err := io.WriteString(writer, message.String()); err != nil {
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("close SMTP exchange: %w", err)
	}
	return nil
}
