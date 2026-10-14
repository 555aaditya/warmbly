package wmail

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/warmbly/warmbly/internal/pkg/mailhtml"
)

func TestGenerateSnippetPlainCollapsesAndDropsQuotes(t *testing.T) {
	body := "Hello,\n\nThis is the first paragraph.\n\n> quoted line\n\n--\nAna, Sunrise Labs"
	got := GenerateSnippet(body, "")
	want := "Hello, This is the first paragraph."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// Frozen normalization provides parity coverage for bounded preview extraction.
func referenceSnippet(bodyPlain, bodyHTML string) string {
	text := bodyPlain
	if strings.TrimSpace(text) == "" && bodyHTML != "" {
		text = bodyHTML
	}
	if mailhtml.LooksLikeHTML(text) {
		text = mailhtml.ToText(text)
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ">") {
			continue
		}
		if line == "--" {
			break
		}
		cleaned = append(cleaned, line)
	}
	text = strings.Join(strings.Fields(strings.Join(cleaned, " ")), " ")
	if utf8.RuneCountInString(text) > snippetMaxRunes {
		text = strings.TrimRight(string([]rune(text)[:snippetMaxRunes]), " .,;:-") + "…"
	}
	return text
}

func TestGenerateSnippetReferenceParity(t *testing.T) {
	for _, body := range []string{
		"", "\r\n\t ", "> quoted\n\t> another\nHello\r\n-- \r\nSignature",
		"--not a signature\nText\u2003next\u00a0word", "one\n\n two\tthree",
		strings.Repeat("é", 199) + "\nword", strings.Repeat("x", 200),
		strings.Repeat("x", 200) + "\t \n--\nignored",
		strings.Repeat("x", 199) + ":\n word", "bad\xff\xfe bytes",
		strings.Repeat("\xff", 201), strings.Repeat("word \n", 10000),
		strings.Repeat("> quoted\n", 1000) + "Visible",
		"<p>HTML &amp; entities</p><blockquote>Quoted</blockquote>",
	} {
		if got, want := GenerateSnippet(body, ""), referenceSnippet(body, ""); got != want {
			t.Fatalf("input prefix %.50q: got %q, want %q", body, got, want)
		}
	}
}

func FuzzGenerateSnippetParity(f *testing.F) {
	for _, body := range []string{"Hello\n> Quote\n--\nSignature", strings.Repeat("é", 201), "bad\xff bytes", "\u2003hello\nworld"} {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body string) {
		if got, want := GenerateSnippet(body, ""), referenceSnippet(body, ""); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

func BenchmarkGenerateSnippet(b *testing.B) {
	for _, tc := range []struct{ name, body string }{
		{"short", "Hello,\n\nThis is a short reply.\n> quoted\n--\nSignature"},
		{"large_plain", strings.Repeat("Message paragraph with UTF-8 café and details.\n", 10000)},
		{"quoted_history", "A new reply.\n" + strings.Repeat("> Long quoted history containing café and details.\n", 10000)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				GenerateSnippet(tc.body, "")
			}
		})
	}
}

func TestGenerateSnippetFromHTMLDecodesEntities(t *testing.T) {
	got := GenerateSnippet("", `<style>.a{color:red}</style><p>Terms &amp; conditions</p><p>caf&eacute; &#169;</p>`)
	if strings.Contains(got, "&amp;") || strings.Contains(got, "color:red") {
		t.Fatalf("unexpected snippet %q", got)
	}
	if got != "Terms & conditions café ©" {
		t.Fatalf("got %q", got)
	}
}

func TestGenerateSnippetTruncatesOnRuneBoundary(t *testing.T) {
	got := GenerateSnippet(strings.Repeat("é", 400), "")
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis, got %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("snippet sliced a rune in half: %q", got)
	}
}
