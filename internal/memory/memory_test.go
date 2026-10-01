package memory

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseSample(t *testing.T) {
	data, err := os.ReadFile("../../design/sample/memories/tenant-isolation.md")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse("tenant-isolation", data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != "decision" || m.Cluster != "auth" {
		t.Errorf("type/cluster = %q/%q", m.Type, m.Cluster)
	}
	if m.Summary != "SearchByte isolates tenants with one Postgres schema each, not RLS" {
		t.Errorf("summary = %q", m.Summary)
	}
	want := []Edge{
		{"decided_for", "searchbyte"},
		{"replaces", "shared-schema-rollout"},
		{"caused_by", "staging-tenant-leak-2026-03"},
		{"mentions", "postgres-rls-orm-bypass"},
	}
	if got := m.Edges(); !reflect.DeepEqual(got, want) {
		t.Errorf("edges =\n%v\nwant\n%v", got, want)
	}
	if !strings.HasPrefix(m.Body, "Each tenant gets") {
		t.Errorf("body starts %q", m.Body[:20])
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"no frontmatter": "hello",
		"unterminated":   "---\ntype: bug\n",
		"bad type":       "---\ntype: idea\nsummary: x\ncluster: a\n---\n",
		"no summary":     "---\ntype: bug\ncluster: a\n---\n",
		"bad cluster":    "---\ntype: bug\nsummary: x\ncluster: Auth Stuff\n---\n",
		"bad rel target": "---\ntype: bug\nsummary: x\ncluster: a\nrel:\n  blocks: [Not A Slug]\n---\n",
	}
	for name, src := range cases {
		if _, err := Parse("x", []byte(src)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestRelForms(t *testing.T) {
	src := "---\ntype: bug\nsummary: x\ncluster: a\nrel:\n  blocks: one\n  causes:\n    - two\n    - three\n---\nsee [[four]] and [[ five | alias ]] and [[Bad Link]]\n"
	m, err := Parse("x", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []Edge{{"blocks", "one"}, {"causes", "two"}, {"causes", "three"}, {"mentions", "four"}, {"mentions", "five"}}
	if got := m.Edges(); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %v", got)
	}
}

func TestFormatRoundTrip(t *testing.T) {
	m := &Memory{
		Name: "x", Type: "pattern", Cluster: "deploy",
		Summary: "Use: colons and #hashes safely",
		Rels:    []Rel{{"depends_on", []string{"a", "b"}}, {"replaces", []string{"c"}}},
		Body:    "Body with [[a]].",
	}
	got, err := Parse("x", m.Format())
	if err != nil {
		t.Fatalf("%v\n%s", err, m.Format())
	}
	if !reflect.DeepEqual(got, m) {
		t.Errorf("round trip:\n%#v\n%#v", got, m)
	}
}

func TestRewriteLinks(t *testing.T) {
	src := `---
type: decision
summary: foo is not foo-bar
cluster: foo
rel:
  caused_by:   [foo, foo-bar,foo]
  foo_ish:
    - foo
---

See [[foo]], [[ foo | the foo ]], [[foo-bar]] and plain foo.
`
	got, changed := RewriteLinks([]byte(src), "foo", "baz")
	if !changed {
		t.Fatal("expected change")
	}
	want := `---
type: decision
summary: foo is not foo-bar
cluster: foo
rel:
  caused_by:   [baz, foo-bar,baz]
  foo_ish:
    - baz
---

See [[baz]], [[baz| the foo ]], [[foo-bar]] and plain foo.
`
	if string(got) != want {
		t.Errorf("got:\n%s", got)
	}
	if _, changed := RewriteLinks([]byte(want), "nope", "x"); changed {
		t.Error("unexpected change")
	}
}

func TestLinksSkipCode(t *testing.T) {
	src := "real [[a]] and `[[...]]` inline\n```bash\nif [[ -f x ]]; then echo [[b]]; fi\n```\n~~~\n[[c]]\n~~~\nafter [[d|alias]]\n"
	if got := Links(src); !reflect.DeepEqual(got, []string{"a", "d"}) {
		t.Errorf("links = %v", got)
	}
}
