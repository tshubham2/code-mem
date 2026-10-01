package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tshubham2/code-mem/internal/memory"
)

func write(t *testing.T, dir, name, typ, cluster, summary, extra string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "memories"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "---\ntype: " + typ + "\nsummary: " + summary + "\ncluster: " + cluster + "\n" + extra
	if !strings.Contains(extra, "---") {
		src += "---\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "memories", name+".md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newSet builds a repo at root/proj with .git, and a global store.
func newSet(t *testing.T) (repo, global string, open func() *Set) {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	os.MkdirAll(filepath.Join(proj, ".git"), 0o755)
	os.MkdirAll(filepath.Join(proj, "sub", "dir"), 0o755)
	repo = filepath.Join(proj, ".mem")
	global = filepath.Join(root, "gmem")
	return repo, global, func() *Set {
		s, err := Open(Config{Cwd: filepath.Join(proj, "sub", "dir"), GlobalDir: global})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
}

func TestDiscoveryCreatesNothingUntilSave(t *testing.T) {
	repo, global, open := newSet(t)
	s := open()
	if len(s.Stores) != 2 || s.Primary().Dir != repo || s.Primary().Label != "proj" {
		t.Fatalf("stores: %s", s.Describe())
	}
	for _, d := range []string{repo, global} {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("%s created before any save", d)
		}
	}
	err := s.Primary().Write(&memory.Memory{Name: "a", Type: "bug", Summary: "x", Cluster: "c"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"memories/a.md", ".gitignore", "index.md", ".index/index.bin"} {
		if _, err := os.Stat(filepath.Join(repo, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	// Unix permissions only; Windows has no group/other bits.
	if fi, _ := os.Stat(filepath.Join(repo, "memories/a.md")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
}

func TestShadowAndCrossStoreLinks(t *testing.T) {
	repo, global, open := newSet(t)
	write(t, global, "go-errors", "convention", "style", "global error style", "rel:\n  relates_to: [team-db]\n")
	write(t, global, "team-db", "decision", "db", "global team db choice", "")
	write(t, repo, "team-db", "decision", "db", "repo overrides team db", "")
	write(t, repo, "svc", "service", "db", "svc uses the team db", "---\n\nsee [[go-errors]]\n")
	s := open()

	r := s.Ref("team-db")
	if r.Store == nil || r.Store.Global || r.Summary != "repo overrides team db" {
		t.Errorf("team-db resolved to %+v", r)
	}
	if r := s.Ref("go-errors"); r.Store == nil || !r.Store.Global {
		t.Errorf("go-errors should resolve globally")
	}

	hits := s.Search("team db", 10, "", "")
	for _, h := range hits {
		if h.Slug == "team-db" && h.Store.Global {
			t.Error("shadowed global memory returned by search")
		}
	}

	// go-errors (global) links to team-db; svc (repo) mentions go-errors.
	got := map[string]string{}
	for _, n := range s.Neighbors("go-errors") {
		got[n.Slug] = strings.Join(n.Out, ",") + "|" + strings.Join(n.In, ",")
	}
	if got["team-db"] != "relates_to|" || got["svc"] != "|mentions" {
		t.Errorf("neighbors = %v", got)
	}

	issues, err := s.Check()
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, is := range issues {
		kinds = append(kinds, is.Kind+":"+is.Name)
	}
	if strings.Join(kinds, " ") != "shadowed:team-db" {
		t.Errorf("issues = %v", kinds)
	}
}

func TestPicksUpExternalEdits(t *testing.T) {
	repo, _, open := newSet(t)
	write(t, repo, "a", "bug", "c", "original wording", "")
	s := open()
	if len(s.Search("revised", 5, "", "")) != 0 {
		t.Fatal("premature hit")
	}
	// A teammate's git pull, or a human editing in their editor.
	time.Sleep(10 * time.Millisecond)
	write(t, repo, "a", "bug", "c", "revised wording", "")
	write(t, repo, "b", "bug", "c", "brand new", "")
	if err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	if len(s.Search("revised", 5, "", "")) != 1 || s.Ref("b").Store == nil {
		t.Error("refresh missed external edits")
	}

	// Deleting the cache is always safe.
	os.RemoveAll(filepath.Join(repo, ".index"))
	s2 := open()
	if s2.Ref("b").Store == nil {
		t.Error("rebuild after cache deletion failed")
	}

	// Broken files are reported, not fatal.
	os.WriteFile(filepath.Join(repo, "memories", "broken.md"), []byte("no frontmatter"), 0o644)
	if err := s2.Refresh(); err != nil {
		t.Fatal(err)
	}
	issues, _ := s2.Check()
	if len(issues) != 1 || issues[0].Kind != "invalid" {
		t.Errorf("issues = %+v", issues)
	}
}

func TestRename(t *testing.T) {
	repo, global, open := newSet(t)
	write(t, global, "g", "preference", "style", "global pref", "rel:\n  relates_to: [old]\n")
	write(t, repo, "old", "decision", "db", "the decision", "---\n\nself [[old]]\n")
	write(t, repo, "x", "bug", "db", "x", "rel:\n  caused_by: [old, other]\n---\n\nsee [[old|the decision]]\n")
	s := open()

	n, err := s.Rename("old", "new-name")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed %d other files, want 1 (global store is below the repo)", n)
	}
	x, _ := os.ReadFile(filepath.Join(repo, "memories", "x.md"))
	if !strings.Contains(string(x), "caused_by: [new-name, other]") || !strings.Contains(string(x), "[[new-name|the decision]]") {
		t.Errorf("x.md not rewritten:\n%s", x)
	}
	self, _ := os.ReadFile(filepath.Join(repo, "memories", "new-name.md"))
	if !strings.Contains(string(self), "[[new-name]]") {
		t.Errorf("self link not rewritten:\n%s", self)
	}
	if s.Ref("new-name").Store == nil || s.Ref("old").Store != nil {
		t.Error("index not refreshed after rename")
	}
	g, _ := os.ReadFile(filepath.Join(global, "memories", "g.md"))
	if !strings.Contains(string(g), "[old]") {
		t.Error("global store should not be rewritten for a repo rename")
	}

	if _, err := s.Rename("x", "new-name"); err == nil {
		t.Error("rename onto existing slug should fail")
	}
	if _, err := s.Rename("missing", "y"); err == nil {
		t.Error("rename of missing slug should fail")
	}
}

func TestBootIndexCaps(t *testing.T) {
	repo, _, open := newSet(t)
	for i := range 5 {
		write(t, repo, "m"+string(rune('a'+i)), "bug", "big", "s", "")
	}
	write(t, repo, "solo", "bug", "small", "s", "")
	s := open()
	got := BootIndex("proj", s.Primary().Index())
	want := "# proj — 6 memories\n\n- **big** (5) — ma, mb, mc, +2 more\n- **small** (1) — solo\n"
	if !strings.HasPrefix(got, want) {
		t.Errorf("got:\n%s", got)
	}
}
