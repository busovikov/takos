package registry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	if len(r.Workspaces) != 0 {
		t.Errorf("empty registry expected, got %+v", r)
	}
	// No implicit workspace: an empty registry lists nothing.
	if entries := r.Entries(); len(entries) != 0 {
		t.Errorf("implicit entries: %+v", entries)
	}
}

func TestAddRemove(t *testing.T) {
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

	// Round-trip through the file.
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Workspaces) != 2 {
		t.Errorf("after round-trip: %+v", r2)
	}

	if err := r2.Remove("second"); err != nil {
		t.Fatal(err)
	}
	if len(r2.Workspaces) != 1 || r2.Workspaces[0].Name != e1.Name {
		t.Errorf("after remove: %+v", r2.Workspaces)
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
	// Empty outside every workspace: nothing is guessed, the caller picks.
	if got, err := Resolve(""); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("Resolve('') = %q, %v", got, err)
	}
	// $TAKOS_WORKSPACE still wins — resolved to its .takos, same as any
	// other project path.
	t.Setenv("TAKOS_WORKSPACE", "/x/y")
	if got, _ := Resolve(""); got != "/x/y/.takos" {
		t.Errorf("env override: %q", got)
	}
	t.Setenv("TAKOS_WORKSPACE", "")

	// No registry and nothing around the cwd → the same explanatory error,
	// never a silent workspace under the home directory.
	os.Remove(Path())
	if got, err := Resolve(""); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("no-registry fallback: %q, %v", got, err)
	}
}

func TestEntriesMostRecentlyUsedFirst(t *testing.T) {
	setHome(t)
	dir1, dir2 := t.TempDir(), t.TempDir()
	r := &Registry{}
	if _, err := r.Add("one", dir1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add("two", dir2); err != nil {
		t.Fatal(err)
	}
	r.Workspaces[0].LastUsedAt = time.Now().Add(-time.Hour)
	r.Workspaces[1].LastUsedAt = time.Now().Add(-2 * time.Hour)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	if got := r.Entries()[0].Name; got != "one" {
		t.Errorf("head of the list: %q, want one", got)
	}
	// Working in "two" moves it to the head for the next picker.
	Touch(dir2)
	r2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := r2.Entries()[0].Name; got != "two" {
		t.Errorf("after Touch: %q, want two", got)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
