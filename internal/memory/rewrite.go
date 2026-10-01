package memory

import (
	"regexp"
	"strings"
)

// RewriteLinks replaces every reference to slug old with new: targets in
// the `rel:` block and [[old]] / [[old|text]] in the body. Everything else,
// including formatting and alignment, is left byte-for-byte alone. The
// second return reports whether anything changed.
func RewriteLinks(data []byte, old, new string) ([]byte, bool) {
	s := string(data)
	fmEnd := frontmatterEnd(s)

	q := regexp.QuoteMeta(old)
	slugRef := regexp.MustCompile(`(^|[^a-z0-9_-])` + q + `($|[^a-z0-9_-])`)
	wiki := regexp.MustCompile(`\[\[\s*` + q + `\s*(\|[^\[\]]*)?\]\]`)

	head, body := s[:fmEnd], s[fmEnd:]
	head = rewriteRelBlock(head, slugRef, new)
	body = wiki.ReplaceAllString(body, "[["+new+"$1]]")

	out := head + body
	return []byte(out), out != s
}

// frontmatterEnd returns the byte offset just past the closing --- line,
// or 0 when the file has no frontmatter.
func frontmatterEnd(s string) int {
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return 0
	}
	i := strings.Index(s[3:], "\n---")
	if i < 0 {
		return 0
	}
	end := 3 + i + 4
	if nl := strings.IndexByte(s[end:], '\n'); nl >= 0 {
		return end + nl + 1
	}
	return len(s)
}

// rewriteRelBlock rewrites slug references in the values (never the keys)
// of lines belonging to the top-level `rel:` mapping.
func rewriteRelBlock(head string, ref *regexp.Regexp, new string) string {
	lines := strings.SplitAfter(head, "\n")
	inRel := false
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		indented := strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t")
		switch {
		case !indented && strings.HasPrefix(trimmed, "rel:"):
			inRel = true
			continue
		case !indented:
			inRel = false
			continue
		case !inRel:
			continue
		}
		cut := strings.IndexByte(ln, ':')
		if strings.HasPrefix(trimmed, "-") {
			cut = strings.IndexByte(ln, '-')
		}
		if cut < 0 {
			continue
		}
		val := ref.ReplaceAllString(ln[cut+1:], "${1}"+new+"${2}")
		// A second pass catches adjacent matches like [a, a] whose shared
		// separator was consumed by the first.
		val = ref.ReplaceAllString(val, "${1}"+new+"${2}")
		lines[i] = ln[:cut+1] + val
	}
	return strings.Join(lines, "")
}
