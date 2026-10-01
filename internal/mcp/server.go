// Package mcp serves the memory tools over the Model Context Protocol:
// newline-delimited JSON-RPC 2.0 on stdin/stdout. Only the handful of
// methods a tools-only server needs are implemented.
package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/tshubham2/code-mem/internal/render"
	"github.com/tshubham2/code-mem/internal/store"
)

// Version is reported to clients in serverInfo. Release builds set it with
// -ldflags "-X github.com/tshubham2/code-mem/internal/mcp.Version=...".
var Version = "dev"

const fallbackProtocol = "2025-06-18"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server answers MCP requests against a store set.
type Server struct {
	Set *store.Set
}

// Serve reads requests from r and writes responses to w until r closes.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			enc.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			continue
		}
		result, rerr := s.handle(req)
		if len(req.ID) == 0 {
			continue // notification: never answered
		}
		resp := response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *Server) handle(req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = fallbackProtocol
		}
		// Only tools are used, whose shape is stable across protocol
		// revisions, so the client's version is accepted as is.
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "code-mem", "version": Version},
			"instructions":    s.instructions(),
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		s.Set.Refresh()
		return map[string]any{"tools": s.tools()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		text, err := s.call(p.Name, p.Arguments)
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		return toolResult(text, false), nil
	}
	if strings.HasPrefix(req.Method, "notifications/") {
		return nil, nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func (s *Server) instructions() string {
	s.Set.Refresh()
	return `code-mem is this project's long-term memory: decisions and why they were made, bug root causes, conventions. The index below shows what exists.

Before answering a question about why something is the way it is, a past incident, or how things are done here, check the index; if a cluster looks relevant, mem_search it or mem_get a listed slug. mem_get returns the memory plus what it links to (what caused it, what it replaced), so one call usually answers the "why".

Summaries and excerpts are partial. Before stating a specific detail (a number, date, name, format or step) that you have not seen in full text, mem_get that memory.

When you learn something durable (a decision and its reason, a bug's root cause, a non-obvious constraint, a stated preference), save it with mem_save. Link it to related memories with rel and [[slug]] references. Do not save what the code or git history already records.

` + render.Boot(s.Set)
}
