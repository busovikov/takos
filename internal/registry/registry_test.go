package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/busovikov/takos/internal/core"
)

func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("TAKOS_HOME", home)
	t.Setenv("TAKOS_WORKSPACE", "")
	return home
}

func TestLoadMissingIsEmpty(t *testing.T) {
	setHome(t)
	r, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Workspaces) != 0 || r.Default != "" {
		t.Errorf("empty registry expected, got %+v", r)
	}
	// The implicit default workspace keeps the pre-registry behavior.
	entries := r.Entries()
	if len(entries) != 1 || entries[0].Name != "default" {
		t.Errorf("implicit entries: %+v", entries)
	}
}

func TestAddRemoveUse(t *testing.T) {
	setHome(t)
	dir1, dir2 := t.TempDir(), t.TempDir()

	r := &Registry{}
	e1, err := r.Add("", dir1)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Name == "" || !filepath.IsAbs(e1.Root) {
		t.Errorf("entry: %+v", e1)
	}
	if r.Default != e1.Name {
		t.Errorf("first add must become the default, got %q", r.Default)
	}

	if _, err := r.Add("second", dir2); err != nil {
		t.Fatal(err)
	}
	// Duplicates: by name and by resolved path.
	if _, err := r.Add("second", t.TempDir()); err == nil {
		t.Error("duplicate name accepted")
	}
	if _, err := r.Add("other", dir1); err == nil {
		t.Error("duplicate root accepted")
	}
	// Bad names.
	if _, err := r.Add("Bad_Name", t.TempDir()); err == nil {
		t.Error("invalid name accepted")
	}

	if err := r.Use("second"); err != nil {
		t.Fatal(err)
	}
	if err := r.Use("ghost"); err == nil {
		t.Error("use of an unknown workspace accepted")
	}

	// Round-trip through the file.
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if r2.Default != "second" || len(r2.Workspaces) != 2 {
		t.Errorf("after round-trip: %+v", r2)
	}

	// Remove moves the default to the first remaining entry.
	if err := r2.Remove("second"); err != nil {
		t.Fatal(err)
	}
	if r2.Default != e1.Name {
		t.Errorf("default after remove: %q", r2.Default)
	}
}

func TestResolve(t *testing.T) {
	setHome(t)
	dir := t.TempDir()

	r := &Registry{}
	if _, err := r.Add("work", dir); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	// By registry name.
	if got, err := Resolve("work"); err != nil || got != mustEval(t, dir) {
		t.Errorf("Resolve(work) = %q, %v", got, err)
	}
	// A path stays a path even if it could be a name.
	if got, err := Resolve("./" + filepath.Base(dir)); err != nil || filepath.IsAbs(got) == false {
		t.Errorf("Resolve(path) = %q, %v", got, err)
	}
	// Unknown name.
	if _, err := Resolve("ghost"); err == nil {
		t.Error("unknown name resolved")
	}
	// Empty → registry default.
	if got, err := Resolve(""); err != nil || got != mustEval(t, dir) {
		t.Errorf("Resolve('') = %q, %v", got, err)
	}
	// $TAKOS_WORKSPACE beats the registry default.
	t.Setenv("TAKOS_WORKSPACE", "/x/y")
	if got, _ := Resolve(""); got != "/x/y" {
		t.Errorf("env override: %q", got)
	}
	t.Setenv("TAKOS_WORKSPACE", "")

	// No registry at all → the classic default root.
	os.Remove(Path())
	if got, err := Resolve(""); err != nil || got != core.DefaultRoot() {
		t.Errorf("no-registry fallback: %q, %v", got, err)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
