package telegram

import (
	"regexp"
	"strings"
)

var mentionRe = regexp.MustCompile(`(?:^|\s)@([a-zA-Z0-9_]+)`)

func ParseMentions(text string) []string {
	matches := mentionRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	result := make([]string, len(matches))
	for i, m := range matches {
		result[i] = m[1]
	}
	return result
}

func StripMention(text, name string) string {
	stripped := strings.Replace(text, "@"+name, "", 1)
	return strings.TrimSpace(stripped)
}
