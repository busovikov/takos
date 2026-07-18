package core

import "strings"

// translit maps Cyrillic to a Latin slug alphabet.
var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

const maxSlugLen = 40

// Slugify turns a task title into a folder-name slug: transliterated,
// lowercase, hyphen-separated, bounded length.
func Slugify(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if t, ok := translit[r]; ok {
				b.WriteString(t)
			} else {
				b.WriteByte('-')
			}
		}
	}
	slug := b.String()
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if len(slug) > maxSlugLen {
		slug = strings.Trim(slug[:maxSlugLen], "-")
	}
	if slug == "" {
		slug = "task"
	}
	return slug
}
