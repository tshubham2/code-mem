package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tshubham2/code-mem/internal/index"
	"github.com/tshubham2/code-mem/internal/memory"
	"github.com/tshubham2/code-mem/internal/render"
	"github.com/tshubham2/code-mem/internal/store"
)

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func (s *Server) tools() []map[string]any {
	clusters := strings.Join(s.Set.Clusters(), ", ")
	if clusters == "" {
		clusters = "(none yet)"
	}
	return []map[string]any{
		{
			"name":        "mem_search",
			"description": "Keyword (BM25) search over memory summaries and bodies. Each hit shows its summary and the memories it links to. Use exact technical terms; follow up with mem_get on a slug.",
			"inputSchema": obj(map[string]any{
				"query":   str("Keywords, e.g. \"RLS tenant owner\""),
				"type":    map[string]any{"type": "string", "enum": memory.Types, "description": "Only memories of this type"},
				"cluster": str("Only memories in this cluster"),
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "description": "Max hits (default 5)"},
			}, "query"),
		},
		{
			"name":        "mem_get",
			"description": "Read one memory in full, plus the summaries of linked memories (what caused it, what it replaced, what references it). One call usually gives the decision and its history.",
			"inputSchema": obj(map[string]any{
				"name": str("Memory slug, e.g. tenant-isolation"),
				"hops": map[string]any{"type": "integer", "minimum": 0, "maximum": render.MaxHops, "description": "Graph distance to include (default 1)"},
			}, "name"),
		},
		{
			"name": "mem_save",
			"description": "Create or replace a memory. One memory = one fact or decision. " +
				"summary is ONE line stating the claim, not the topic (good: \"Postgres RLS does not apply to the table owner unless FORCE is set\"; bad: \"Notes about RLS\"). " +
				"Reuse an existing cluster when one fits. Existing clusters: " + clusters + ".",
			"inputSchema": obj(map[string]any{
				"name":    str("Slug: lowercase-hyphenated, unique, descriptive. Becomes the filename."),
				"type":    map[string]any{"type": "string", "enum": memory.Types},
				"summary": str("One line, the claim itself"),
				"cluster": str("Group for the boot index, e.g. auth, deploy, conventions"),
				"body":    str("Markdown. Explain the why. Reference other memories inline as [[slug]]."),
				"rel": map[string]any{
					"type":                 "object",
					"description":          "Typed links in active voice, e.g. {\"caused_by\": [\"staging-leak\"], \"replaces\": [\"old-design\"]}. Targets may not exist yet.",
					"additionalProperties": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"store": map[string]any{"type": "string", "enum": []string{"repo", "global"}, "description": "repo (default) for project knowledge; global for cross-project preferences"},
			}, "name", "type", "summary", "cluster"),
		},
		{
			"name":        "mem_rename",
			"description": "Rename a memory and rewrite every link to it.",
			"inputSchema": obj(map[string]any{"from": str("Current slug"), "to": str("New slug")}, "from", "to"),
		},
		{
			"name":        "mem_check",
			"description": "Report invalid memory files, malformed links, shadowed memories, and links to memories not written yet.",
			"inputSchema": obj(map[string]any{}),
		},
	}
}

func (s *Server) call(name string, raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := s.Set.Refresh(); err != nil {
		return "", err
	}
	switch name {
	case "mem_search":
		var a struct {
			Query   string `json:"query"`
			Type    string `json:"type"`
			Cluster string `json:"cluster"`
			Limit   int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		if a.Limit <= 0 {
			a.Limit = 5
		}
		return Search(s.Set, a.Query, a.Type, a.Cluster, min(a.Limit, 20)), nil
	case "mem_get":
		a := struct {
			Name string `json:"name"`
			Hops *int   `json:"hops"`
		}{}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		hops := 1
		if a.Hops != nil {
			hops = *a.Hops
		}
		return render.Get(s.Set, strings.TrimSpace(a.Name), hops)
	case "mem_save":
		var a SaveArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		return Save(s.Set, a)
	case "mem_rename":
		var a struct{ From, To string }
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		n, err := s.Set.Rename(a.From, a.To)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("renamed %s → %s; rewrote links in %d other %s\n", a.From, a.To, n, plural(n, "memory", "memories")), nil
	case "mem_check":
		issues, err := s.Set.Check()
		if err != nil {
			return "", err
		}
		return render.Check(issues), nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// Search is mem_search, shared with the CLI.
func Search(set *store.Set, query, typ, cluster string, limit int) string {
	return render.Search(set, query, set.Search(query, limit, typ, cluster))
}

// SaveArgs are mem_save's arguments.
type SaveArgs struct {
	Name    string              `json:"name"`
	Type    string              `json:"type"`
	Summary string              `json:"summary"`
	Cluster string              `json:"cluster"`
	Body    string              `json:"body"`
	Rel     map[string][]string `json:"rel"`
	Store   string              `json:"store"`
}

// Save is mem_save: it validates, writes, and reports anything the agent
// should know about what it just saved.
func Save(set *store.Set, a SaveArgs) (string, error) {
	st := set.Primary()
	if a.Store == "global" {
		g, err := set.Store("global")
		if err != nil {
			return "", errors.New("no global store configured")
		}
		st = g
	}
	m := &memory.Memory{
		Name:    strings.TrimSpace(a.Name),
		Type:    strings.TrimSpace(a.Type),
		Summary: strings.TrimSpace(a.Summary),
		Cluster: strings.TrimSpace(a.Cluster),
		Rels:    memory.NormalizeRels(a.Rel),
		Body:    a.Body,
	}
	if err := m.Validate(); err != nil {
		return "", err
	}

	known := map[string]bool{}
	for _, c := range set.Clusters() {
		known[c] = true
	}
	existed := st.Has(m.Name)
	similar := findSimilar(set, m)
	_, shadows := globalTwin(set, st, m.Name)

	if err := st.Write(m); err != nil {
		return "", err
	}
	if err := set.Refresh(); err != nil {
		return "", err
	}

	var b strings.Builder
	verb := "saved"
	if existed {
		verb = "updated"
	}
	fmt.Fprintf(&b, "%s %s in %s\n", verb, m.Name, st.Label)
	if !known[m.Cluster] && len(known) > 0 {
		var cs []string
		for c := range known {
			cs = append(cs, c)
		}
		sort.Strings(cs)
		fmt.Fprintf(&b, "new cluster %q (existing: %s). If one of those fits, re-save with it.\n", m.Cluster, strings.Join(cs, ", "))
	}
	if shadows {
		fmt.Fprintf(&b, "note: this hides the global memory %q in this repo\n", m.Name)
	}
	var missing []string
	for _, e := range m.Edges() {
		if set.Ref(e.Target).Store == nil {
			missing = append(missing, e.Target)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, "links to memories not written yet: %s\n", strings.Join(missing, ", "))
	}
	if len(similar) > 0 {
		b.WriteString("similar existing memories (if one states the same fact, update it instead and delete this):\n")
		for _, r := range similar {
			fmt.Fprintf(&b, "  %s — %s\n", r.Slug, r.Summary)
		}
	}
	return b.String(), nil
}

// globalTwin reports whether saving name into st would shadow a memory in
// a lower-precedence store.
func globalTwin(set *store.Set, st *store.Store, name string) (*store.Store, bool) {
	below := false
	for _, x := range set.Stores {
		if x == st {
			below = true
			continue
		}
		if below && x.Has(name) {
			return x, true
		}
	}
	return nil, false
}

// findSimilar finds memories whose summary shares most of its terms with
// m's: a cheap guard against an agent re-saving a fact under a new slug.
func findSimilar(set *store.Set, m *memory.Memory) []store.Ref {
	mine := termSet(m.Summary)
	if len(mine) == 0 {
		return nil
	}
	var out []store.Ref
	for _, h := range set.Search(m.Summary, 3, "", "") {
		if h.Slug == m.Name {
			continue
		}
		theirs := termSet(h.Summary)
		inter := 0
		for t := range mine {
			if theirs[t] {
				inter++
			}
		}
		union := len(mine) + len(theirs) - inter
		if union > 0 && float64(inter)/float64(union) >= 0.5 {
			out = append(out, h.Ref)
		}
	}
	return out
}

func termSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range index.Tokenize(s) {
		out[t] = true
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
