package service

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

// EmailAttachment is intentionally a byte slice because manual invoice PDFs
// are retained in the private database table and must not be exposed through a
// public object URL before delivery.
type EmailAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

type smtpMessage struct {
	envelopeFrom string
	envelopeTo   string
	data         []byte
}

func buildSMTPMessage(config *SMTPConfig, to, subject, body string) (smtpMessage, error) {
	return buildSMTPMessageWithAttachments(config, to, subject, body, nil)
}

func buildSMTPMessageWithAttachments(config *SMTPConfig, to, subject, body string, attachments []EmailAttachment) (smtpMessage, error) {
	if config == nil {
		return smtpMessage{}, errors.New("missing SMTP configuration")
	}

	fromAddress, err := parseSMTPAddress(config.From, "from")
	if err != nil {
		return smtpMessage{}, err
	}
	recipientAddress, err := parseSMTPAddress(to, "recipient")
	if err != nil {
		return smtpMessage{}, err
	}
	messageID, err := generateEmailMessageID(fromAddress.Address, config.Host)
	if err != nil {
		return smtpMessage{}, fmt.Errorf("generate message ID: %w", err)
	}

	fromName := sanitizeEmailHeader(config.FromName)
	if strings.TrimSpace(fromName) == "" {
		fromName = fromAddress.Name
	}
	fromHeader := (&mail.Address{
		Name:    fromName,
		Address: fromAddress.Address,
	}).String()
	toHeader := (&mail.Address{
		Name:    recipientAddress.Name,
		Address: recipientAddress.Address,
	}).String()
	subjectHeader := mime.QEncoding.Encode("UTF-8", sanitizeEmailHeader(subject))

	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\n", fromHeader)
	fmt.Fprintf(&message, "To: %s\r\n", toHeader)
	fmt.Fprintf(&message, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&message, "Message-ID: %s\r\n", messageID)
	fmt.Fprintf(&message, "Subject: %s\r\n", subjectHeader)
	fmt.Fprint(&message, "MIME-Version: 1.0\r\n")
	if len(attachments) == 0 {
		fmt.Fprint(&message, "Content-Type: text/html; charset=UTF-8\r\n"+
			"Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := writeQuotedPrintableHTML(&message, body); err != nil {
			return smtpMessage{}, err
		}
	} else {
		mixed := multipart.NewWriter(&message)
		fmt.Fprintf(&message, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mixed.Boundary())

		htmlHeader := make(textproto.MIMEHeader)
		htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
		htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
		htmlPart, err := mixed.CreatePart(htmlHeader)
		if err != nil {
			return smtpMessage{}, fmt.Errorf("create email HTML part: %w", err)
		}
		if err := writeQuotedPrintableHTML(htmlPart, body); err != nil {
			return smtpMessage{}, err
		}

		for _, attachment := range attachments {
			filename := strings.TrimSpace(sanitizeEmailHeader(attachment.Filename))
			if filename == "" {
				return smtpMessage{}, errors.New("email attachment filename is required")
			}
			contentType := strings.TrimSpace(sanitizeEmailHeader(attachment.ContentType))
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			partHeader := make(textproto.MIMEHeader)
			partHeader.Set("Content-Type", mime.FormatMediaType(contentType, map[string]string{"name": filename}))
			partHeader.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
			partHeader.Set("Content-Transfer-Encoding", "base64")
			part, err := mixed.CreatePart(partHeader)
			if err != nil {
				return smtpMessage{}, fmt.Errorf("create email attachment part: %w", err)
			}
			if err := writeMIMEBase64(part, attachment.Data); err != nil {
				return smtpMessage{}, fmt.Errorf("encode email attachment: %w", err)
			}
		}
		if err := mixed.Close(); err != nil {
			return smtpMessage{}, fmt.Errorf("close multipart email: %w", err)
		}
	}

	return smtpMessage{
		envelopeFrom: fromAddress.Address,
		envelopeTo:   recipientAddress.Address,
		data:         message.Bytes(),
	}, nil
}

func writeQuotedPrintableHTML(dst io.Writer, body string) error {
	bodyWriter := quotedprintable.NewWriter(dst)
	if _, err := bodyWriter.Write([]byte(body)); err != nil {
		return fmt.Errorf("encode email body: %w", err)
	}
	if err := bodyWriter.Close(); err != nil {
		return fmt.Errorf("close email body encoder: %w", err)
	}
	return nil
}

func writeMIMEBase64(dst io.Writer, data []byte) error {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		if _, err := fmt.Fprintf(dst, "%s\r\n", encoded[:76]); err != nil {
			return err
		}
		encoded = encoded[76:]
	}
	if encoded != "" {
		if _, err := fmt.Fprintf(dst, "%s\r\n", encoded); err != nil {
			return err
		}
	}
	return nil
}

func parseSMTPAddress(value, field string) (*mail.Address, error) {
	if strings.ContainsAny(value, "\r\n") {
		return nil, fmt.Errorf("invalid SMTP %s address: contains a line break", field)
	}

	cleaned := strings.TrimSpace(value)
	address, err := mail.ParseAddress(cleaned)
	if err != nil || strings.TrimSpace(address.Address) == "" {
		if err == nil {
			err = fmt.Errorf("address is empty")
		}
		return nil, fmt.Errorf("invalid SMTP %s address: %w", field, err)
	}
	return address, nil
}

func generateEmailMessageID(fromAddress, smtpHost string) (string, error) {
	randomID := make([]byte, 16)
	if _, err := rand.Read(randomID); err != nil {
		return "", err
	}

	domain := strings.TrimSpace(sanitizeEmailHeader(smtpHost))
	if at := strings.LastIndexByte(fromAddress, '@'); at >= 0 && at < len(fromAddress)-1 {
		domain = fromAddress[at+1:]
	}
	domain = strings.Trim(domain, "[]<>")
	if domain == "" {
		domain = "localhost"
	}

	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(randomID), domain), nil
}
