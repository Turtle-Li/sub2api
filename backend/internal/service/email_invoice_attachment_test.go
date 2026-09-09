package service

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInvoicePDFEmailMIMERoundTrip(t *testing.T) {
	config := &SMTPConfig{Host: "smtp.example.test", From: "sender@example.test", FromName: "Sub2"}
	pdf := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{0, 1, 2, 253, 254, 255}, 70)...)
	body := "<p>发票已开具 = PDF attached</p>"
	message, err := buildSMTPMessageWithAttachments(config, "recipient@example.test", "发票", body, []EmailAttachment{{Filename: "电子发票\r\nBcc: unwanted@example.test.pdf", ContentType: "application/pdf", Data: pdf}})
	require.NoError(t, err)
	parsed, err := mail.ReadMessage(bytes.NewReader(message.data))
	require.NoError(t, err)
	require.Empty(t, parsed.Header.Get("Bcc"))
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/mixed", mediaType)
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	html, err := reader.NextPart() // NextPart transparently decodes quoted-printable.
	require.NoError(t, err)
	htmlBytes, err := io.ReadAll(html)
	require.NoError(t, err)
	require.Equal(t, body, string(htmlBytes))
	attachment, err := reader.NextPart()
	require.NoError(t, err)
	require.Empty(t, attachment.Header.Get("Bcc"))
	require.Equal(t, "电子发票Bcc: unwanted@example.test.pdf", attachment.FileName())
	mimeType, _, err := mime.ParseMediaType(attachment.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "application/pdf", mimeType)
	encoded, err := io.ReadAll(attachment)
	require.NoError(t, err)
	for _, line := range strings.Split(strings.TrimSpace(string(encoded)), "\r\n") {
		require.LessOrEqual(t, len(line), 76)
	}
	decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(encoded)))
	require.NoError(t, err)
	require.Equal(t, pdf, decoded)
	_, err = reader.NextPart()
	require.ErrorIs(t, err, io.EOF)
}
