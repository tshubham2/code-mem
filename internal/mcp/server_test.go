package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tshubham2/code-mem/internal/store"
)

type session struct {
	t   *testing.T
	srv *Server
	id  int
}

func newSession(t *testing.T) (*session, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "searchbyte", ".mem")
	src := "../../design/sample/memories"
	os.MkdirAll(filepath.Join(repo, "memories"), 0o755)
	ents, _ := os.ReadDir(src)
	for _, e := range ents {
		data, _ := os.ReadFile(filepath.Join(src, e.Name()))
		os.WriteFile(filepath.Join(repo, "memories", e.Name()), data, 0o644)
	}
	set, err := store.Open(store.Config{RepoDir: repo, GlobalDir: filepath.Join(root, "gmem")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(set.Close)
	return &session{t: t, srv: &Server{Set: set}}, repo
}

// rpc sends one request line and decodes the single response.
func (s *session) rpc(method string, params any) map[string]any {
	s.t.Helper()
	s.id++
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": s.id, "method": method, "params": params})
	var out bytes.Buffer
	if err := s.srv.Serve(bytes.NewReader(append(line, '\n')), &out); err != nil {
		s.t.Fatal(err)
	}
	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		s.t.Fatalf("bad response %q: %v", out.String(), err)
	}
	if resp["id"].(float64) != float64(s.id) {
		s.t.Fatalf("id mismatch: %v", resp)
	}
	return resp
}

// tool calls a tool and returns its text and isError flag.
func (s *session) tool(name string, args any) (string, bool) {
	s.t.Helper()
	r := s.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	res := r["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	return text, res["isError"].(bool)
}

func TestHandshake(t *testing.T) {
	s, _ := newSession(t)
	r := s.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}})
	res := r["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" {
		t.Errorf("version %v", res["protocolVersion"])
	}
	ins := res["instructions"].(string)
	if !strings.Contains(ins, "# searchbyte — 4 memories") || !strings.Contains(ins, "**auth** (3)") {
		t.Errorf("instructions missing boot index:\n%s", ins)
	}

	tools := s.rpc("tools/list", nil)["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, x := range tools {
		tool := x.(map[string]any)
		names = append(names, tool["name"].(string))
		if tool["name"] == "mem_save" && !strings.Contains(tool["description"].(string), "auth, conventions") {
			t.Error("mem_save description should list existing clusters")
		}
	}
	if strings.Join(names, ",") != "mem_search,mem_get,mem_save,mem_rename,mem_check" {
		t.Errorf("tools = %v", names)
	}

	if r := s.rpc("nope", nil); r["error"] == nil {
		t.Error("unknown method should error")
	}
}

func TestNotificationsGetNoReply(t *testing.T) {
	s, _ := newSession(t)
	var out bytes.Buffer
	in := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + "garbage\n"
	s.srv.Serve(strings.NewReader(in), &out)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "-32700") {
		t.Errorf("got %q", out.String())
	}
}

func TestToolsFlow(t *testing.T) {
	s, repo := newSession(t)

	text, isErr := s.tool("mem_search", map[string]any{"query": "RLS owner FORCE"})
	if isErr || !strings.HasPrefix(text, "postgres-rls-orm-bypass · bug · auth\n") {
		t.Errorf("search:\n%s", text)
	}
	// Hits carry body text, not just the summary line.
	if !strings.Contains(text, "ALTER TABLE invoices FORCE ROW LEVEL SECURITY;") {
		t.Errorf("search hit missing body excerpt:\n%s", text)
	}

	text, _ = s.tool("mem_get", map[string]any{"name": "tenant-isolation"})
	for _, want := range []string{
		"## tenant-isolation · decision · auth",
		"caused_by →  staging-tenant-leak-2026-03",
		"Billing job wrote 1.2k invoice rows",
		"replaces →  shared-schema-rollout  (not written yet)",
		"mentions →, ← blocks  postgres-rls-orm-bypass",
		// Direct neighbors carry an excerpt of their body.
		"The nightly billing job bulk-inserted invoices without setting",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("get missing %q:\n%s", want, text)
		}
	}
	text, _ = s.tool("mem_get", map[string]any{"name": "tenant-isolation", "hops": 0})
	if strings.Contains(text, "neighbors:") {
		t.Error("hops 0 should omit neighbors")
	}
	text, isErr = s.tool("mem_get", map[string]any{"name": "tenant-isolaton"})
	if !isErr || !strings.Contains(text, "did you mean") {
		t.Errorf("typo get: %s", text)
	}

	// Save fills a dangling link, in a new cluster.
	text, isErr = s.tool("mem_save", map[string]any{
		"name": "shared-schema-rollout", "type": "decision", "cluster": "architecture",
		"summary": "Original design: one schema, tenant_id column on every table",
		"rel":     map[string]any{"replaced_by": []string{"tenant-isolation"}},
		"body":    "Abandoned after [[postgres-rls-orm-bypass]] surfaced.",
	})
	if isErr {
		t.Fatal(text)
	}
	for _, want := range []string{"saved shared-schema-rollout in searchbyte", `new cluster "architecture" (existing: auth, conventions)`} {
		if !strings.Contains(text, want) {
			t.Errorf("save missing %q:\n%s", want, text)
		}
	}
	text, _ = s.tool("mem_get", map[string]any{"name": "tenant-isolation"})
	if !strings.Contains(text, "replaces →, ← replaced_by  shared-schema-rollout\n      Original design") {
		t.Errorf("phantom not resolved after save:\n%s", text)
	}

	// Near-duplicate save is flagged.
	text, _ = s.tool("mem_save", map[string]any{
		"name": "rls-force", "type": "bug", "cluster": "auth",
		"summary": "RLS policies do not apply to table owner unless FORCE set",
	})
	if !strings.Contains(text, "similar existing memories") || !strings.Contains(text, "postgres-rls-orm-bypass") {
		t.Errorf("duplicate not flagged:\n%s", text)
	}

	// Validation errors come back as tool errors, not protocol errors.
	text, isErr = s.tool("mem_save", map[string]any{"name": "Bad Name", "type": "idea", "summary": "a\nb", "cluster": "x"})
	if !isErr || !strings.Contains(text, "invalid slug") || !strings.Contains(text, "one line") {
		t.Errorf("validation: %v %s", isErr, text)
	}

	text, isErr = s.tool("mem_rename", map[string]any{"from": "postgres-rls-orm-bypass", "to": "rls-owner-bypass"})
	if isErr || !strings.Contains(text, "rewrote links in 3 other memories") {
		t.Errorf("rename: %s", text)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "memories", "tenant-isolation.md"))
	if !strings.Contains(string(data), "[[rls-owner-bypass]]") {
		t.Error("rename did not rewrite tenant-isolation")
	}

	text, _ = s.tool("mem_check", map[string]any{})
	if !strings.Contains(text, "searchbyte/tenant-isolation: decided_for → searchbyte") || strings.Contains(text, "invalid") {
		t.Errorf("check:\n%s", text)
	}

	boot, _ := os.ReadFile(filepath.Join(repo, "index.md"))
	if !strings.Contains(string(boot), "# searchbyte — 6 memories") {
		t.Errorf("index.md not regenerated:\n%s", boot)
	}
}
