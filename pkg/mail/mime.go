package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
)

// buildRaw renders msg as an RFC 5322 MIME message for the SMTP DATA
// command and the SESv2 raw send. To/Cc appear in headers; Bcc travels in
// the envelope only and is omitted here. Tags ride as X-Tag-* headers.
func buildRaw(fromAddr, fromName string, msg Message) ([]byte, error) {
	if err := Validate(msg); err != nil {
		return nil, err
	}
	from := fromAddr
	if msg.FromAddr != "" {
		from = msg.FromAddr
	}
	name := fromName
	if msg.FromName != "" {
		name = msg.FromName
	}
	var buf bytes.Buffer
	writeHeader := func(k, v string) {
		fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
	}
	writeHeader("From", formatAddr(name, from))
	writeHeader("To", strings.Join(msg.To, ", "))
	if len(msg.Cc) > 0 {
		writeHeader("Cc", strings.Join(msg.Cc, ", "))
	}
	if msg.ReplyTo != "" {
		writeHeader("Reply-To", msg.ReplyTo)
	}
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	writeHeader("Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader("MIME-Version", "1.0")
	for k, v := range msg.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		writeHeader(k, v)
	}
	for k, v := range msg.Tags {
		if strings.TrimSpace(k) == "" {
			continue
		}
		writeHeader("X-Tag-"+k, v)
	}

	hasText := strings.TrimSpace(msg.Text) != ""
	hasHTML := strings.TrimSpace(msg.HTML) != ""
	hasFiles := len(msg.Files) > 0

	switch {
	case hasFiles:
		mixed := boundary()
		writeHeader("Content-Type", "multipart/mixed; boundary="+mixed)
		buf.WriteString("\r\n")
		if hasText || hasHTML {
			fmt.Fprintf(&buf, "--%s\r\n", mixed)
			alt := boundary()
			fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", alt)
			writeAltPart(&buf, alt, msg.Text, msg.HTML)
			fmt.Fprintf(&buf, "--%s--\r\n", alt)
		}
		for _, f := range msg.Files {
			writeAttachment(&buf, mixed, f)
		}
		fmt.Fprintf(&buf, "--%s--\r\n", mixed)
	case hasText && hasHTML:
		alt := boundary()
		writeHeader("Content-Type", "multipart/alternative; boundary="+alt)
		buf.WriteString("\r\n")
		writeAltPart(&buf, alt, msg.Text, msg.HTML)
		fmt.Fprintf(&buf, "--%s--\r\n", alt)
	case hasHTML:
		writeHeader("Content-Type", "text/html; charset=UTF-8")
		writeHeader("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n" + msg.HTML + "\r\n")
	default:
		writeHeader("Content-Type", "text/plain; charset=UTF-8")
		writeHeader("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n" + msg.Text + "\r\n")
	}
	return buf.Bytes(), nil
}

// writeAltPart writes the text and HTML bodies inside an alternative part.
func writeAltPart(buf *bytes.Buffer, alt, text, html string) {
	if strings.TrimSpace(text) != "" {
		fmt.Fprintf(buf, "--%s\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n%s\r\n", alt, text)
	}
	if strings.TrimSpace(html) != "" {
		fmt.Fprintf(buf, "--%s\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n%s\r\n", alt, html)
	}
}

// writeAttachment appends one file part to a mixed boundary.
func writeAttachment(buf *bytes.Buffer, mixed string, f Attachment) {
	ctype := f.ContentType
	if strings.TrimSpace(ctype) == "" {
		ctype = "application/octet-stream"
	}
	disposition := "attachment"
	extra := ""
	if f.Inline {
		disposition = "inline"
		extra = fmt.Sprintf("\r\nContent-ID: <%s>", f.ContentID)
	}
	fmt.Fprintf(buf, "--%s\r\nContent-Type: %s; name=%q\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: %s; filename=%q%s\r\n\r\n",
		mixed, ctype, f.Filename, disposition, f.Filename, extra)
	enc := base64.StdEncoding.EncodeToString(f.Data)
	for len(enc) > 76 {
		buf.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	buf.WriteString(enc + "\r\n")
}

// formatAddr renders a display name + address pair, Q-encoding the name.
func formatAddr(name, addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.TrimSpace(name) == "" {
		return addr
	}
	return (&mail.Address{Name: name, Address: addr}).String()
}

// boundary mints a MIME boundary. Crypto-rand failure falls back to a
// timestamped constant rather than failing the whole send.
func boundary() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ginplate-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("ginplate-%x", b)
}

// envelopeRecipients flattens To+Cc+Bcc for the SMTP envelope.
func envelopeRecipients(msg Message) []string {
	return append(append(append([]string{}, msg.To...), msg.Cc...), msg.Bcc...)
}
