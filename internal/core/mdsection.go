package core

import (
	"regexp"
	"strings"
)

// The markdown body of task.md is structured into "## <Name>" sections.

func sectionRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?ms)^## ` + regexp.QuoteMeta(name) + `\s*$\n?(.*?)(^## |\z)`)
}

// GetSection returns the trimmed body of section "## name", or "".
func GetSection(body, name string) string {
	m := sectionRe(name).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// SetSection replaces the body of section "## name", appending the section if
// it doesn't exist yet.
func SetSection(body, name, content string) string {
	block := "## " + name + "\n" + strings.TrimSpace(content) + "\n"
	re := sectionRe(name)
	if re.MatchString(body) {
		return re.ReplaceAllString(body, block+"$2")
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if body != "" {
		body += "\n"
	}
	return body + block
}
