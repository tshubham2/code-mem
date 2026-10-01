// Package render produces the text that crosses the wire to a model. No
// JSON: plain lines carry the same content in fewer tokens (see WIRE.md).
package render

import (
	"fmt"
	"strings"

	"github.com/tshubham2/code-mem/internal/store"
)

const (
	searchEdges = 4  // neighbors listed under each search hit
	maxNodes    = 25 // neighbors listed by Get, across all hops
	MaxHops     = 2

	// Excerpt budgets, in bytes of body text. A summary alone is not enough
	// to answer from: agents given only summaries stopped early and left out
	// the dates, numbers and formats that live in the body.
	searchExcerpt   = 500
	neighborExcerpt = 400
)

// excerpt flattens body to a single line and cuts it at about n bytes on a
// word boundary. The marker tells the agent the note continues.
func excerpt(body string, n int) string {
	flat := strings.Join(strings.Fields(body), " ")
	if len(flat) <= n {
		return flat
	}
	cut := strings.LastIndexByte(flat[:n], ' ')
	if cut < n/2 {
		cut = n
	}
	return flat[:cut] + " …(truncated; mem_get for the full note)"
}

// bodyExcerpt reads a memory's body for display, or "" if it cannot.
func bodyExcerpt(set *store.Set, r store.Ref, n int) string {
	if r.Store == nil {
		return ""
	}
	body, err := set.ReadBody(r)
	if err != nil || body == "" {
		return ""
	}
	return excerpt(body, n)
}

// header renders "slug · type · cluster", tagging memories from the
// global store so the agent knows they are not repo-specific.
func header(r store.Ref) string {
	h := fmt.Sprintf("%s · %s · %s", r.Slug, r.Type, r.Cluster)
	if r.Store != nil && r.Store.Global {
		h += " · global"
	}
	return h
}

// Search renders search hits, each with its immediate graph neighborhood.
func Search(set *store.Set, query string, hits []store.Hit) string {
	if len(hits) == 0 {
		return fmt.Sprintf("no matches for %q. clusters: %s\n", query, strings.Join(set.Clusters(), ", "))
	}
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(header(h.Ref) + "\n")
		b.WriteString("  " + h.Summary + "\n")
		if ex := bodyExcerpt(set, h.Ref, searchExcerpt); ex != "" {
			b.WriteString("  " + ex + "\n")
		}
		ns := set.Neighbors(h.Slug)
		var parts []string
		for _, n := range ns {
			for _, t := range n.Out {
				parts = append(parts, t+" "+n.Slug)
			}
			for _, t := range n.In {
				parts = append(parts, "← "+t+" "+n.Slug)
			}
		}
		if len(parts) > searchEdges {
			parts = append(parts[:searchEdges], fmt.Sprintf("+%d more", len(parts)-searchEdges))
		}
		if len(parts) > 0 {
			b.WriteString("  → " + strings.Join(parts, " · ") + "\n")
		}
	}
	return b.String()
}

// Get renders the full body of slug, an excerpt of each direct neighbor's
// body, and the summaries of anything further out.
func Get(set *store.Set, slug string, hops int) (string, error) {
	r := set.Ref(slug)
	if r.Store == nil {
		return "", notFound(set, slug)
	}
	body, err := set.ReadBody(r)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("## " + header(r) + "\n\n")
	if body != "" {
		b.WriteString(body + "\n")
	}
	hops = max(0, min(hops, MaxHops))
	if hops == 0 {
		return b.String(), nil
	}

	var nb strings.Builder // neighbors, rendered apart to know if there are any
	seen := map[string]bool{slug: true}
	budget := maxNodes
	var walk func(at string, depth int, indent string) bool
	walk = func(at string, depth int, indent string) bool {
		// Claim this level before descending, so a direct neighbor is never
		// shown nested under another one.
		var fresh []store.Neighbor
		for _, n := range set.Neighbors(at) {
			if !seen[n.Slug] {
				seen[n.Slug] = true
				fresh = append(fresh, n)
			}
		}
		for _, n := range fresh {
			if budget == 0 {
				nb.WriteString(indent + "… (more neighbors; mem_get one of the above to continue)\n")
				return false
			}
			budget--
			line := indent + relation(n) + "  " + n.Slug
			switch {
			case n.Store == nil:
				line += "  (not written yet)"
			case n.Store.Global && !r.Store.Global:
				line += "  · global"
			}
			nb.WriteString(line + "\n")
			if n.Summary != "" {
				nb.WriteString(indent + "    " + n.Summary + "\n")
			}
			if depth == 1 {
				if ex := bodyExcerpt(set, n.Ref, neighborExcerpt); ex != "" {
					nb.WriteString(indent + "    " + ex + "\n")
				}
			}
			if depth < hops && n.Store != nil {
				if !walk(n.Slug, depth+1, indent+"    ") {
					return false
				}
			}
		}
		return true
	}
	walk(slug, 1, "  ")
	if nb.Len() > 0 {
		b.WriteString("\nneighbors:\n")
		b.WriteString(nb.String())
	}
	return b.String(), nil
}

// relation renders the edge types joining a neighbor: "caused_by →" for
// outbound, "← blocks" for inbound.
func relation(n store.Neighbor) string {
	var parts []string
	for _, t := range n.Out {
		parts = append(parts, t+" →")
	}
	for _, t := range n.In {
		parts = append(parts, "← "+t)
	}
	return strings.Join(parts, ", ")
}

func notFound(set *store.Set, slug string) error {
	msg := fmt.Sprintf("no memory %q", slug)
	if hits := set.Search(strings.ReplaceAll(slug, "-", " "), 3, "", ""); len(hits) > 0 {
		var names []string
		for _, h := range hits {
			names = append(names, h.Slug)
		}
		msg += ". did you mean: " + strings.Join(names, ", ")
	}
	return fmt.Errorf("%s", msg)
}

// Check renders mem_check issues grouped by kind.
func Check(issues []store.Issue) string {
	if len(issues) == 0 {
		return "ok: no issues\n"
	}
	kinds := []struct{ kind, title string }{
		{"invalid", "invalid (not indexed until fixed)"},
		{"bad-link", "malformed links"},
		{"shadowed", "shadowed by a repo memory"},
		{"dangling", "dangling links (legal: marks something worth writing)"},
	}
	var b strings.Builder
	for _, k := range kinds {
		var lines []string
		for _, is := range issues {
			if is.Kind == k.kind {
				lines = append(lines, fmt.Sprintf("  %s/%s: %s", is.Store.Label, is.Name, is.Note))
			}
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "%s (%d):\n%s\n", k.title, len(lines), strings.Join(lines, "\n"))
		}
	}
	return b.String()
}

// Boot renders every store's boot index, repo first.
func Boot(set *store.Set) string {
	var parts []string
	for _, st := range set.Stores {
		parts = append(parts, strings.TrimSuffix(
			store.BootIndex(st.Label, st.Index()),
			"\n<!-- generated by code-mem; do not edit by hand -->\n"))
	}
	return strings.Join(parts, "\n")
}
