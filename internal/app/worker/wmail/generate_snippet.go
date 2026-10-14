package wmail

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/pkg/mailhtml"
)

// snippetMaxRunes bounds the one-line preview shown in conversation lists. It
// is a preview only: the full body lives in object storage and is what the
// thread reader renders.
const snippetMaxRunes = 200

// GenerateSnippet builds the list preview for a message: quoted history and
// signature dropped, whitespace collapsed to a single line, cut to a rune
// boundary so a multi-byte character is never sliced in half.
func GenerateSnippet(bodyPlain, bodyHTML string) string {
	text := bodyPlain
	if strings.TrimSpace(text) == "" && bodyHTML != "" {
		text = bodyHTML
	}
	if mailhtml.LooksLikeHTML(text) {
		// Entity-decoding matters here: the stripped output of an HTML body is
		// escaped text, so "&amp;" would otherwise be shown verbatim. Senders
		// that put markup in their text/plain part get the same treatment.
		text = mailhtml.ToText(text)
	}

	// Line filtering has to run before whitespace collapsing, or there are no
	// lines left to filter.
	var snippet strings.Builder
	snippet.Grow(min(len(text), snippetMaxRunes))
	runes := 0
	space := false
	truncate := func() string {
		// Match []rune's invalid UTF-8 replacement only when truncating.
		return strings.TrimRight(string([]rune(snippet.String())), " .,;:-") + "…"
	}
	for text != "" {
		var line string
		line, text, _ = strings.Cut(text, "\n")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ">") {
			continue // quoted reply history
		}
		if line == "--" {
			break // RFC 3676 signature delimiter (trailing space already trimmed)
		}
		for i := 0; i < len(line); {
			r, size := utf8.DecodeRuneInString(line[i:])
			if unicode.IsSpace(r) {
				space = runes > 0
				i += size
				continue
			}
			if space {
				if runes == snippetMaxRunes {
					return truncate()
				}
				snippet.WriteByte(' ')
				runes++
				space = false
			}
			if runes == snippetMaxRunes {
				return truncate()
			}
			snippet.WriteString(line[i : i+size])
			i += size
			runes++
		}
		space = runes > 0
	}
	return snippet.String()
}

// SearchText renders the message body down to the bounded plain text Postgres
// indexes for search.
func SearchText(bodyPlain, bodyHTML string) string {
	return mailhtml.SearchText(bodyPlain, bodyHTML, config.MaxSearchBodyText)
}
