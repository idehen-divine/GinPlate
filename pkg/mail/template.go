package mail

import (
	"embed"
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)

// RenderFS executes name+".html" from a mailable package's embedded
// templates (each mail directory embeds its own *.html beside its mailable).
func RenderFS(fsys embed.FS, name string, data any) (string, error) {
	tmpl, err := template.ParseFS(fsys, name+".html")
	if err != nil {
		return "", fmt.Errorf("mail: template %q: %w", name, err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("mail: template %q: %w", name, err)
	}
	return sb.String(), nil
}

// StripTags derives a plain-text fallback from HTML by removing tags and
// collapsing whitespace. Mailables use it for the text part; write custom
// text when formatting matters.
func StripTags(html string) string {
	text := tagRe.ReplaceAllString(html, " ")
	return strings.Join(strings.Fields(text), " ")
}
