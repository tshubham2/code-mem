package render

import (
	"strings"
	"testing"
)

func TestExcerpt(t *testing.T) {
	if got := excerpt("short\n\nbody   text", 100); got != "short body text" {
		t.Errorf("flatten: %q", got)
	}
	long := strings.Repeat("word ", 50)
	got := excerpt(long, 42)
	if !strings.HasSuffix(got, "…(truncated; mem_get for the full note)") {
		t.Errorf("missing marker: %q", got)
	}
	if body := strings.TrimSuffix(got, " …(truncated; mem_get for the full note)"); len(body) > 42 || strings.HasSuffix(body, " ") || strings.Contains(body, "wor ") {
		t.Errorf("bad cut: %q", body)
	}
	// One giant word cannot be cut on a space; cut at the budget instead.
	if got := excerpt(strings.Repeat("x", 100), 10); !strings.HasPrefix(got, "xxxxxxxxxx …") {
		t.Errorf("no-space cut: %q", got)
	}
}
