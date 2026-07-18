package fsstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")

	if err := WriteFileAtomic(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2" {
		t.Errorf("content = %q, want v2", got)
	}

	// Никаких временных файлов после успешной записи.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("остался временный файл %s", e.Name())
		}
	}
}

func TestTryLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.lock")
	l1, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); err == nil {
		t.Fatal("второй TryLock должен упасть, пока лок держится")
	}
	if err := l1.Unlock(); err != nil {
		t.Fatal(err)
	}
	l2, err := TryLock(path)
	if err != nil {
		t.Fatalf("после Unlock лок должен браться: %v", err)
	}
	l2.Unlock()
}

func TestFrontmatterRoundtrip(t *testing.T) {
	type meta struct {
		ID       string   `yaml:"id"`
		Priority string   `yaml:"priority"`
		Labels   []string `yaml:"labels,omitempty"`
	}
	in := meta{ID: "T-0042", Priority: "p2", Labels: []string{"auth", "tests"}}
	body := []byte("## Objective\nОписание задачи.\n")

	doc, err := RenderFrontmatter(in, body)
	if err != nil {
		t.Fatal(err)
	}
	var out meta
	gotBody, err := ParseFrontmatter(doc, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.Priority != in.Priority || len(out.Labels) != 2 {
		t.Errorf("meta roundtrip: got %+v", out)
	}
	if string(gotBody) != string(body) {
		t.Errorf("body roundtrip: got %q, want %q", gotBody, body)
	}
}

func TestFrontmatterAbsent(t *testing.T) {
	var out struct{}
	body, err := ParseFrontmatter([]byte("просто текст\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "просто текст\n" {
		t.Errorf("body = %q", body)
	}
}

func TestFrontmatterUnclosed(t *testing.T) {
	var out struct{}
	if _, err := ParseFrontmatter([]byte("---\nid: x\n"), &out); err == nil {
		t.Fatal("незакрытый frontmatter должен быть ошибкой")
	}
}
