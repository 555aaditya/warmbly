package arf

import (
	"bytes"
	"io"
	"strings"

	"github.com/emersion/go-message"
)

// reportParts carry the Feedback-Type and the reported Message-ID; the human notice carries neither.
var reportParts = map[string]bool{
	"message/feedback-report": true,
	"message/rfc822":          true,
	"message/rfc822-headers":  true,
	"text/rfc822-headers":     true,
	"message/global":          true,
	"message/global-headers":  true,
}

// ParseMIME parses a whole raw message by its report parts, for a provider that
// syncs only the rendered body (Microsoft Graph).
func ParseMIME(raw []byte) Report {
	ent, err := message.Read(bytes.NewReader(raw))
	if ent == nil || err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return Report{}
	}
	var parts []string
	_ = ent.Walk(func(_ []int, part *message.Entity, _ error) error {
		if part == nil {
			return nil
		}
		mediaType, _, _ := part.Header.ContentType()
		if !reportParts[strings.ToLower(mediaType)] {
			return nil
		}
		body, _ := io.ReadAll(part.Body)
		parts = append(parts, string(body))
		return nil
	})
	return Parse(strings.Join(parts, "\n"))
}
