package fsstore

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

var fmDelim = []byte("---\n")

// SplitFrontmatter splits a markdown document into raw YAML frontmatter and
// body. A document without frontmatter yields nil meta and the whole input as
// body.
func SplitFrontmatter(doc []byte) (meta, body []byte, err error) {
	if !bytes.HasPrefix(doc, fmDelim) {
		return nil, doc, nil
	}
	rest := doc[len(fmDelim):]
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return nil, nil, fmt.Errorf("frontmatter не закрыт разделителем ---")
	}
	meta = rest[:end+1] // keep the trailing newline for YAML
	body = rest[end+1:]
	// Skip the closing delimiter line ("---" plus its newline, if present).
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = nil
	}
	return meta, body, nil
}

// ParseFrontmatter unmarshals the document's frontmatter into out and returns
// the markdown body.
func ParseFrontmatter(doc []byte, out any) (body []byte, err error) {
	meta, body, err := SplitFrontmatter(doc)
	if err != nil {
		return nil, err
	}
	if meta != nil {
		if err := yaml.Unmarshal(meta, out); err != nil {
			return nil, fmt.Errorf("frontmatter: %w", err)
		}
	}
	return body, nil
}

// RenderFrontmatter serialises meta and body back into a markdown document.
func RenderFrontmatter(meta any, body []byte) ([]byte, error) {
	y, err := yaml.Marshal(meta)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(fmDelim)
	b.Write(y)
	b.WriteString("---\n")
	b.Write(body)
	return b.Bytes(), nil
}
