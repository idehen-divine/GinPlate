package mail

import (
	"regexp"
	"strings"
)

var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)

// StripTags derives a plain-text fallback from HTML by removing tags and
// collapsing whitespace. Mailables use it for the text part; write custom
// text when formatting matters.
func StripTags(html string) string {
	text := tagRe.ReplaceAllString(html, " ")
	return strings.Join(strings.Fields(text), " ")
}
