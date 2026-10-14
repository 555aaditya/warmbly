package tasks

import (
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

func BenchmarkRenderTemplateCached(b *testing.B) {
	contact := models.Contact{FirstName: "Alex", CustomFields: map[string]string{"job title": "Founder"}}
	context := TemplateContext{Sender: TemplateSender{Name: "Ana"}}
	for _, tc := range []struct{ name, source string }{
		{"subject", `Hello {{.FirstName}} from {{.Sender.Name}}`},
		{"body", strings.Repeat(`<p>Hi {{.FirstName}}, your role is {{.job title}}. {{if .Sender.Name}}From {{.Sender.Name}}{{end}}</p>`, 20)},
		{"fallback", `{{if .Missing}}Hello {{.FirstName}} in {{.job title}}`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			RenderTemplateWith(tc.source, contact, context)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				RenderTemplateWith(tc.source, contact, context)
			}
		})
	}
}
