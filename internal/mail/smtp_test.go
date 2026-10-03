package mail

import (
	"bufio"
	"context"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"testing"
)

// A receiving SMTP peer observes encoded Persian copy, not raw UTF-8 headers.
func TestSenderDeliversPersianMessage(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	serverErrors := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErrors <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		send := func(reply string) error {
			_, err := writer.WriteString(reply + "\r\n")
			if err != nil {
				return err
			}
			return writer.Flush()
		}
		if err := send("220 localhost ready"); err != nil {
			serverErrors <- err
			return
		}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverErrors <- err
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO "), strings.HasPrefix(line, "HELO "):
				err = send("250 localhost")
			case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				err = send("250 ok")
			case line == "DATA\r\n":
				if err = send("354 send message"); err == nil {
					var message strings.Builder
					for {
						part, readErr := reader.ReadString('\n')
						if readErr != nil {
							err = readErr
							break
						}
						if part == ".\r\n" {
							break
						}
						message.WriteString(part)
					}
					if err == nil {
						received <- message.String()
						err = send("250 queued")
					}
				}
			case line == "QUIT\r\n":
				err = send("221 goodbye")
				serverErrors <- err
				return
			default:
				serverErrors <- io.ErrUnexpectedEOF
				return
			}
			if err != nil {
				serverErrors <- err
				return
			}
		}
	}()
	sender, err := NewSender(Config{Address: listener.Addr().String(), From: "piko@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), "owner@example.test", "ایمیل خود را تأیید کنید", "برای فعال\u200cسازی حساب، ایمیل خود را تأیید کنید.\nhttps://example.test/verify?token=abc\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
	wire := <-received
	if !strings.Contains(wire, "Subject: =?utf-8?") {
		t.Fatal("Persian subject was not MIME encoded")
	}
	message, err := mail.ReadMessage(strings.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	if message.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Fatal("Persian body lacks a portable transfer encoding")
	}
	body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "برای فعال\u200cسازی حساب، ایمیل خود را تأیید کنید.") || !strings.Contains(string(body), "https://example.test/verify?token=abc") {
		t.Fatalf("decoded message body=%q", body)
	}
}
