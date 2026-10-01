// Package memory reads and writes memory files: YAML frontmatter, a prose
// body, and [[wikilinks]]. The filename (minus .md) is the memory's id.
package memory

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Types are the allowed values of the `type` field.
var Types = []string{"decision", "bug", "convention", "pattern", "service", "person", "preference"}

// MaxSummary bounds the one-line summary. It is the only line that enters
// the index, so it must stay short.
const MaxSummary = 200

// Mentions is the edge type given to [[wikilinks]] found in the body.
const Mentions = "mentions"

var (
	slugRE    = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	relTypeRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	linkRE    = regexp.MustCompile(`\[\[([^\[\]|]+)(\|[^\[\]]*)?\]\]`)
)

// ValidSlug reports whether s can be a memory id: lowercase, digits, hyphens.
func ValidSlug(s string) bool { return len(s) <= 120 && slugRE.MatchString(s) }

// ValidType reports whether t is one of Types.
func ValidType(t string) bool {
	for _, x := range Types {
		if x == t {
			return true
		}
	}
	return false
}

// Rel is one typed outbound edge group from the `rel:` frontmatter block.
type Rel struct {
	Type    string
	Targets []string
}

// Memory is one parsed memory file.
type Memory struct {
	Name    string
	Type    string
	Summary string
	Cluster string
	Rels    []Rel // in file order
	Body    string
}

// Edge is a typed outbound edge, from either `rel:` or a body wikilink.
type Edge struct {
	Type   string
	Target string
}

// Edges returns rel edges in file order, then body mentions in order of
// first appearance. Exact duplicates and self-links are dropped.
func (m *Memory) Edges() []Edge {
	seen := map[Edge]bool{}
	var out []Edge
	add := func(e Edge) {
		if e.Target == m.Name || seen[e] {
			return
		}
		seen[e] = true
		out = append(out, e)
	}
	for _, r := range m.Rels {
		for _, t := range r.Targets {
			add(Edge{r.Type, t})
		}
	}
	for _, l := range Links(m.Body) {
		if ValidSlug(l) {
			add(Edge{Mentions, l})
		}
	}
	return out
}

// Links returns every [[target]] in text, in order, trimmed. Invalid slugs
// are returned too so callers can report them. Code is skipped: inside
// fences or `spans`, [[ is syntax (bash tests, nested arrays), not a link.
func Links(text string) []string {
	var out []string
	for _, m := range linkRE.FindAllStringSubmatch(stripCode(text), -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

var inlineCodeRE = regexp.MustCompile("`[^`\n]*`")

// stripCode blanks fenced code blocks and inline code spans.
func stripCode(text string) string {
	lines := strings.Split(text, "\n")
	fence := ""
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		switch {
		case fence != "":
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			lines[i] = ""
		case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
			fence = t[:3]
			lines[i] = ""
		default:
			lines[i] = inlineCodeRE.ReplaceAllString(ln, "")
		}
	}
	return strings.Join(lines, "\n")
}

type frontmatter struct {
	Type    string  `yaml:"type"`
	Summary string  `yaml:"summary"`
	Cluster string  `yaml:"cluster"`
	Rel     relList `yaml:"rel"`
}

type relList []Rel

func (r *relList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: rel must be a mapping of edge type to slugs", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		var targets []string
		switch v.Kind {
		case yaml.ScalarNode:
			if v.Value != "" {
				targets = []string{v.Value}
			}
		case yaml.SequenceNode:
			if err := v.Decode(&targets); err != nil {
				return fmt.Errorf("line %d: rel.%s: %w", v.Line, k.Value, err)
			}
		default:
			return fmt.Errorf("line %d: rel.%s must be a slug or a list of slugs", v.Line, k.Value)
		}
		*r = append(*r, Rel{Type: k.Value, Targets: targets})
	}
	return nil
}

// Parse parses a memory file. name is the filename without .md.
func Parse(name string, data []byte) (*Memory, error) {
	fm, body, err := split(data)
	if err != nil {
		return nil, err
	}
	var f frontmatter
	if err := yaml.Unmarshal(fm, &f); err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	m := &Memory{
		Name:    name,
		Type:    strings.TrimSpace(f.Type),
		Summary: strings.TrimSpace(f.Summary),
		Cluster: strings.TrimSpace(f.Cluster),
		Rels:    f.Rel,
		Body:    strings.TrimSpace(body),
	}
	return m, m.Validate()
}

// Body returns the body of a memory file without parsing its frontmatter.
func Body(data []byte) string {
	_, body, err := split(data)
	if err != nil {
		return strings.TrimSpace(string(data))
	}
	return strings.TrimSpace(body)
}

func split(data []byte) (fm []byte, body string, err error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, "", errors.New("missing frontmatter: file must start with ---")
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", errors.New("unterminated frontmatter: no closing ---")
	}
	fm = []byte(rest[:end])
	rest = rest[end+4:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		rest = ""
	}
	return fm, rest, nil
}

// Validate checks the fields FORMAT.md requires.
func (m *Memory) Validate() error {
	var errs []string
	if !ValidSlug(m.Name) {
		errs = append(errs, fmt.Sprintf("invalid slug %q (use lowercase letters, digits, hyphens)", m.Name))
	}
	if !ValidType(m.Type) {
		errs = append(errs, fmt.Sprintf("type %q must be one of %s", m.Type, strings.Join(Types, ", ")))
	}
	switch {
	case m.Summary == "":
		errs = append(errs, "summary is required")
	case strings.ContainsAny(m.Summary, "\n\r"):
		errs = append(errs, "summary must be one line")
	case len(m.Summary) > MaxSummary:
		errs = append(errs, fmt.Sprintf("summary is %d chars, max %d", len(m.Summary), MaxSummary))
	}
	if !ValidSlug(m.Cluster) {
		errs = append(errs, fmt.Sprintf("cluster %q must be a slug", m.Cluster))
	}
	for _, r := range m.Rels {
		if !relTypeRE.MatchString(r.Type) {
			errs = append(errs, fmt.Sprintf("rel type %q must be snake_case", r.Type))
		}
		for _, t := range r.Targets {
			if !ValidSlug(t) {
				errs = append(errs, fmt.Sprintf("rel.%s target %q is not a slug", r.Type, t))
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Format renders m as a memory file.
func (m *Memory) Format() []byte {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "type: %s\n", m.Type)
	fmt.Fprintf(&b, "summary: %s\n", scalar(m.Summary))
	fmt.Fprintf(&b, "cluster: %s\n", m.Cluster)
	if len(m.Rels) > 0 {
		b.WriteString("rel:\n")
		w := 0
		for _, r := range m.Rels {
			w = max(w, len(r.Type))
		}
		for _, r := range m.Rels {
			fmt.Fprintf(&b, "  %s:%s [%s]\n", r.Type, strings.Repeat(" ", w-len(r.Type)+1), strings.Join(r.Targets, ", "))
		}
	}
	b.WriteString("---\n")
	if m.Body != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(m.Body))
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// scalar quotes s only when plain YAML would misread it.
func scalar(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%q", s)
	}
	return strings.TrimRight(string(out), "\n")
}

// NormalizeRels merges duplicate edge types, drops empty groups and
// duplicate targets, and orders groups by type for a stable file.
func NormalizeRels(in map[string][]string) []Rel {
	var out []Rel
	for t, targets := range in {
		seen := map[string]bool{}
		var ts []string
		for _, x := range targets {
			x = strings.TrimSpace(x)
			if x != "" && !seen[x] {
				seen[x] = true
				ts = append(ts, x)
			}
		}
		if len(ts) > 0 {
			out = append(out, Rel{Type: t, Targets: ts})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}
