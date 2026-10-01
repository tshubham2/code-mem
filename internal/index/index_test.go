package index

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tshubham2/code-mem/internal/memory"
)

func loadSample(t *testing.T) []*memory.Memory {
	t.Helper()
	dir := "../../design/sample/memories"
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []*memory.Memory
	for _, e := range ents {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		m, err := memory.Parse(strings.TrimSuffix(e.Name(), ".md"), data)
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		out = append(out, m)
	}
	return out
}

func openSample(t *testing.T) *Index {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.bin")
	if err := os.WriteFile(path, Build(loadSample(t), 42), 0o644); err != nil {
		t.Fatal(err)
	}
	x, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

func names(x *Index, e Edges) []string {
	var out []string
	for i := range e.Len() {
		id, typ := e.At(i)
		out = append(out, typ+" "+x.Name(id))
	}
	return out
}

func TestGraph(t *testing.T) {
	x := openSample(t)
	if x.Fingerprint() != 42 || x.Real() != 4 {
		t.Fatalf("fingerprint %d real %d", x.Fingerprint(), x.Real())
	}
	// searchbyte, shared-schema-rollout, plus applies_to target of error-wrapping
	if x.Len() != 6 {
		t.Errorf("len = %d", x.Len())
	}
	id, ok := x.Lookup("tenant-isolation")
	if !ok || x.Phantom(id) || x.Cluster(id) != "auth" || x.Type(id) != "decision" {
		t.Fatalf("lookup tenant-isolation: %d %v", id, ok)
	}
	want := []string{
		"decided_for searchbyte",
		"replaces shared-schema-rollout",
		"caused_by staging-tenant-leak-2026-03",
		"mentions postgres-rls-orm-bypass",
	}
	if got := names(x, x.Out(id)); !reflect.DeepEqual(got, want) {
		t.Errorf("out = %v", got)
	}
	if got := names(x, x.In(id)); !reflect.DeepEqual(got, []string{"blocks postgres-rls-orm-bypass"}) {
		t.Errorf("in = %v", got)
	}
	ph, ok := x.Lookup("shared-schema-rollout")
	if !ok || !x.Phantom(ph) || x.Summary(ph) != "" {
		t.Errorf("phantom lookup: %d %v", ph, ok)
	}
	if _, ok := x.Lookup("nope"); ok {
		t.Error("found nonexistent")
	}
}

func TestSearch(t *testing.T) {
	x := openSample(t)
	top := func(q string) string {
		h := x.Search(q, 3, nil)
		if len(h) == 0 {
			return ""
		}
		return x.Name(h[0].ID)
	}
	cases := map[string]string{
		"RLS owner FORCE":  "postgres-rls-orm-bypass",
		"tenant isolation": "tenant-isolation",
		"wrap errors":      "error-wrapping",
		"billing invoices": "staging-tenant-leak-2026-03",
		"policies":         "postgres-rls-orm-bypass", // plural folding
		"search_path":      "tenant-isolation",
	}
	for q, want := range cases {
		if got := top(q); got != want {
			t.Errorf("search %q: top = %q, want %q", q, got, want)
		}
	}
	if h := x.Search("kubernetes", 5, nil); len(h) != 0 {
		t.Errorf("unrelated query hit %v", h)
	}
	auth := func(id uint32) bool { return x.Cluster(id) == "auth" }
	for _, h := range x.Search("errors tenant", 10, auth) {
		if x.Cluster(h.ID) != "auth" {
			t.Errorf("filter leaked %s", x.Name(h.ID))
		}
	}
}

func TestRejectsGarbage(t *testing.T) {
	if _, err := Decode([]byte("nope")); err == nil {
		t.Error("decoded garbage")
	}
	b := Build(nil, 1)
	b[hVersion] = 99
	if _, err := Decode(b); err != ErrStale {
		t.Errorf("err = %v, want ErrStale", err)
	}
}

func TestEmpty(t *testing.T) {
	x, err := Decode(Build(nil, 7))
	if err != nil {
		t.Fatal(err)
	}
	if x.Len() != 0 || x.Search("x", 5, nil) != nil {
		t.Error("empty index not empty")
	}
}

func TestTokenize(t *testing.T) {
	got := Tokenize("The ORM's search_path policies, FORCE-d!")
	want := []string{"orm", "search_path", "search", "path", "policy", "force"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}
