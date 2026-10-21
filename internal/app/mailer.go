package app

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// MailConfig configures outgoing email (invoices). Port 465 uses implicit TLS;
// other ports use STARTTLS when the server offers it.
type MailConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string // e.g. "Acme Roofing <billing@acme.com>"
}

func (c MailConfig) Configured() bool { return c.Host != "" && c.From != "" }

type attachment struct {
	Name        string
	ContentType string
	Data        []byte
}

type outgoingMail struct {
	To, Cc      []string
	ReplyTo     string
	Subject     string
	Text        string
	Attachments []attachment
}

// parseAddressList validates a comma/semicolon separated list of email addresses.
func parseAddressList(field, raw string, required bool) ([]string, error) {
	raw = strings.ReplaceAll(raw, ";", ",")
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, err := mail.ParseAddress(part)
		if err != nil {
			return nil, errBad("%s: %q is not a valid email address", field, part)
		}
		out = append(out, a.Address)
	}
	if required && len(out) == 0 {
		return nil, errBad("%s is required", field)
	}
	if len(out) > 20 {
		return nil, errBad("%s: too many recipients", field)
	}
	return out, nil
}

func buildMessage(from string, m outgoingMail) ([]byte, error) {
	for _, v := range []string{m.Subject, m.ReplyTo} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, errors.New("header values may not contain line breaks")
		}
	}
	var buf bytes.Buffer
	idBytes := make([]byte, 12)
	rand.Read(idBytes)
	domain := "localhost"
	if a, err := mail.ParseAddress(from); err == nil {
		if at := strings.LastIndex(a.Address, "@"); at >= 0 {
			domain = a.Address[at+1:]
		}
	}
	w := multipart.NewWriter(&buf)
	hdr := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	hdr("From", from)
	hdr("To", strings.Join(m.To, ", "))
	if len(m.Cc) > 0 {
		hdr("Cc", strings.Join(m.Cc, ", "))
	}
	if m.ReplyTo != "" {
		hdr("Reply-To", m.ReplyTo)
	}
	hdr("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	hdr("Date", time.Now().Format(time.RFC1123Z))
	hdr("Message-ID", fmt.Sprintf("<%s@%s>", hex.EncodeToString(idBytes), domain))
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", "multipart/mixed; boundary="+w.Boundary())
	buf.WriteString("\r\n")

	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return nil, err
	}
	qp := quotedprintable.NewWriter(part)
	qp.Write([]byte(strings.ReplaceAll(m.Text, "\n", "\r\n")))
	qp.Close()

	for _, a := range m.Attachments {
		part, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {a.ContentType},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": a.Name})},
		})
		if err != nil {
			return nil, err
		}
		enc := base64.StdEncoding.EncodeToString(a.Data)
		for len(enc) > 76 {
			part.Write([]byte(enc[:76] + "\r\n"))
			enc = enc[76:]
		}
		part.Write([]byte(enc + "\r\n"))
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c MailConfig) send(m outgoingMail) error {
	if !c.Configured() {
		return errors.New("email is not configured (set SMTP_HOST and SMTP_FROM)")
	}
	fromAddr, err := mail.ParseAddress(c.From)
	if err != nil {
		return fmt.Errorf("SMTP_FROM: %w", err)
	}
	msg, err := buildMessage(c.From, m)
	if err != nil {
		return err
	}
	port := c.Port
	if port == "" {
		port = "587"
	}
	addr := net.JoinHostPort(c.Host, port)
	tlsCfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}

	var client *smtp.Client
	if port == "465" {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, tlsCfg)
		if err != nil {
			return err
		}
		if client, err = smtp.NewClient(conn, c.Host); err != nil {
			return err
		}
	} else {
		conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
		if err != nil {
			return err
		}
		if client, err = smtp.NewClient(conn, c.Host); err != nil {
			return err
		}
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				client.Close()
				return err
			}
		}
	}
	defer client.Close()
	if c.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(fromAddr.Address); err != nil {
		return err
	}
	for _, rcpt := range append(append([]string{}, m.To...), m.Cc...) {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("recipient %s: %w", rcpt, err)
		}
	}
	wc, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return client.Quit()
}
