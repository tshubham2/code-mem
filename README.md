# code-mem

[![CI](https://github.com/tshubham2/code-mem/actions/workflows/ci.yml/badge.svg)](https://github.com/tshubham2/code-mem/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tshubham2/code-mem.svg)](https://pkg.go.dev/github.com/tshubham2/code-mem)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Persistent, linked memory for AI coding agents, stored as plain markdown in your repository.**

code-mem is an [MCP](https://modelcontextprotocol.io) server that gives a
coding agent a long-term memory of your project: why decisions were made,
what caused past bugs, and how things are done here. Memories are small
markdown files that link to each other. Asking about one decision also
returns the incident behind it and the design it replaced, in a single call.

```
you    › should we switch tenant isolation to row-level security?

agent  › (sees an "auth" cluster in its memory index, calls mem_get)

         We moved away from RLS on purpose. Our ORM connects as the table
         owner, which bypasses RLS policies unless FORCE is set, and that
         caused a cross-tenant leak in staging in March 2026. We now use one
         schema per tenant instead…
```

## Features

- **Plain files, reviewed like code.** Each memory is a markdown file in
  `.mem/memories/`. It can be diffed, reviewed in a pull request, and
  shared with `git pull`.
- **Answers include the "why".** Memories link to one another with typed
  relations (`caused_by`, `replaces`, `depends_on`, …) and inline
  `[[links]]`. A lookup returns the memory plus its linked neighbors.
- **The agent can see what it knows.** A compact index of topics is sent
  to the agent at the start of every session, so it can tell when a
  question has a recorded answer.
- **Fast, zero-setup search.** Keyword ranking (BM25) over a single
  memory-mapped index file. No embedding model, no database, no
  background process.
- **Self-maintaining.** Edits made in an editor, by `git pull`, or by
  another agent are picked up automatically. The index is a disposable
  cache: deleting it is always safe.
- **Per-project and personal memory.** Each repository has its own store,
  and a global store holds preferences that apply everywhere.
- **Small footprint.** One static Go binary with a single dependency.
  Responses are compact plain text, not JSON, to save context tokens.

## Installation

Requires [Go](https://go.dev/dl/) 1.22 or newer.

```sh
go install github.com/tshubham2/code-mem/cmd/code-mem@latest
```

The binary is installed to `$(go env GOPATH)/bin`, usually `~/go/bin`.

To build from source instead:

```sh
git clone https://github.com/tshubham2/code-mem.git
cd code-mem
go build -o bin/code-mem ./cmd/code-mem
```

## Quick start

### 1. Register the server with your agent

code-mem works with any MCP-compatible client. Most clients accept a JSON
configuration like this:

```json
{
  "mcpServers": {
    "code-mem": {
      "command": "/absolute/path/to/code-mem",
      "args": ["serve"]
    }
  }
}
```

Use the absolute path to the binary (for example
`/Users/you/go/bin/code-mem`). The client should start the server in your
project directory, which is the default for most coding agents.

### 2. Add agent instructions (recommended)

The server gives the agent basic usage guidance on its own. Adding a few
lines to your agent's instructions file (`AGENTS.md`, or your client's
equivalent) makes it consistent about checking memory before answering
and saving durable knowledge as it works.

<details>
<summary>Suggested instructions</summary>

```markdown
## Memory (code-mem)

You have long-term project memory through the code-mem tools
(mem_search, mem_get, mem_save, mem_rename, mem_check). Its table of
contents is in your context at the start of every session.

### Look before you answer
- Before answering why something is built the way it is, how things are
  done in this project, or whether a problem has been seen before, check
  the table of contents. If a topic looks related, mem_get the listed
  note, or mem_search with specific technical words.
- Before changing something a memory describes, read that memory first.

### Save as you go. Don't ask permission.
Call mem_save when you learn:
- a decision and the reason behind it
- the root cause of a bug, and the fix
- a non-obvious constraint (infrastructure, auth, rate limits, production quirks)
- how two components actually talk to each other
- a convention the code doesn't make obvious
- my stated preferences about tools, style or workflow (use store: "global")

Do NOT save: anything readable from the code or git history, task
status, or details that only matter to the current conversation.

### How to write a memory
- One fact or decision per memory.
- summary: one line stating the claim itself. Write "Postgres RLS doesn't
  apply to the table owner unless FORCE is set", not "Notes about RLS".
- body: explain the why. Mention related memories as [[their-name]].
- rel: link causes and history in active voice: caused_by, replaces,
  depends_on, decided_for.
- Reuse an existing cluster when one fits.
- If mem_save reports a similar existing memory, update that one instead
  and delete the new file from .mem/memories/.
- When something is superseded, update the old memory rather than leaving
  two that disagree.
```

</details>

### 3. Use it

Talk to your agent normally. Ask it to remember things ("remember that we
don't use Redis because the hosting plan doesn't include it"). With the
instructions above it will also save important findings on its own. In
later sessions it finds and uses them when they're relevant.

In a git repository the store is created at `<repo>/.mem` on the first
save. Outside git, create it yourself with `mkdir .mem`.

## Tools

| Tool | Description |
|------|-------------|
| `mem_search` | Keyword search across memory names, summaries and bodies. Each hit includes its linked memories. Optional `type` and `cluster` filters. |
| `mem_get` | A memory's full text, plus the summaries of memories linked to it, up to `hops` away (default 1, max 2). |
| `mem_save` | Create or update a memory. Validates the format and reports new clusters, links to memories that don't exist yet, and likely duplicates. |
| `mem_rename` | Rename a memory and rewrite every link that points to it. |
| `mem_check` | Report invalid files, malformed links, shadowed memories and links to memories not yet written. |

Example `mem_get` response:

```
## tenant-isolation · decision · auth

Each tenant gets its own Postgres schema; the connection pool sets
`search_path` per request.

**Why not RLS:** the ORM connects as the table owner, which bypasses
row-level policies entirely ([[postgres-rls-orm-bypass]]). Rows leaked
across tenants in staging before anyone noticed.

neighbors:
  replaces →  shared-schema-rollout  (not written yet)
  caused_by →  staging-tenant-leak-2026-03
      Billing job wrote 1.2k invoice rows to the wrong tenant in staging, 2026-03
  mentions →, ← blocks  postgres-rls-orm-bypass
      Postgres RLS policies do not apply to the table owner unless FORCE is set
```

## Memory format

Each memory is a markdown file whose name (minus `.md`) is its identifier:

```markdown
---
type: decision
summary: SearchByte isolates tenants with one Postgres schema each, not RLS
cluster: auth
rel:
  replaces:  [shared-schema-rollout]
  caused_by: [staging-tenant-leak-2026-03]
---

Each tenant gets its own Postgres schema; the connection pool sets
`search_path` per request.

**Why not RLS:** the ORM connects as the table owner, which bypasses
row-level policies entirely ([[postgres-rls-orm-bypass]]).
```

| Field | Required | Description |
|-------|----------|-------------|
| `type` | yes | `decision`, `bug`, `convention`, `pattern`, `service`, `person` or `preference` |
| `summary` | yes | One line stating the claim. This is what search ranks highest and what the agent reads first. |
| `cluster` | yes | Topic used to group memories in the index |
| `rel` | no | Typed links to other memories, written in active voice |

Links can point to memories that don't exist yet. They work as markers
for knowledge worth capturing, and `mem_check` lists them. See
[`design/sample/`](design/sample) for a complete example set.

## Storage

```
.mem/
├── memories/      one markdown file per memory (commit this)
├── index.md       generated topic index (commit this)
└── .index/        search index cache (git-ignored, safe to delete)
```

| Store | Location | Purpose |
|-------|----------|---------|
| Repository | Nearest `.mem/` above the working directory, otherwise `<git root>/.mem` | Project knowledge, shared through git |
| Global | `$CODE_MEM_GLOBAL`, otherwise `~/.mem` | Personal preferences that apply across projects |

Both stores are searched together. A repository memory takes precedence
over a global memory with the same name, and links resolve across stores.

## Command-line interface

The binary also works as a CLI over the same stores, which is useful for
browsing memory without an agent and for CI.

```sh
code-mem search tenant isolation -n 3    # keyword search
code-mem get tenant-isolation -hops 2    # read a memory and its neighbors
code-mem boot                            # print the topic index
code-mem check                           # validate; exits non-zero on errors
code-mem rename old-name new-name        # rename and rewrite links
code-mem index                           # force a full index rebuild
code-mem serve                           # run the MCP server on stdio
```

| Flag / variable | Description |
|-----------------|-------------|
| `-dir`, `CODE_MEM_DIR` | Repository store location |
| `-global`, `CODE_MEM_GLOBAL` | Global store location (default `~/.mem`) |
| `-no-global` | Ignore the global store |

Running `code-mem check` in CI catches malformed memories before they're
merged.

## How it works

1. **Startup.** The server memory-maps a single index file containing the
   memory table, the link graph (in both directions) and the search index.
   Nothing is parsed, so startup time doesn't depend on store size.
2. **Freshness.** Before each tool call, the server compares the name,
   size and modification time of every memory file against a fingerprint
   stored in the index, and rebuilds the index if anything changed. This
   costs about 1.6 ms for 1,000 memories.
3. **Retrieval.** BM25 ranks memories by keyword, weighting summaries
   above bodies. Results come back with their graph neighborhood, so
   related context arrives without a second query.

Why keyword search and links instead of embeddings: embeddings find text
that *sounds* similar, while links record what an author said is related,
such as a decision and the incident that caused it. For "why" questions,
the links are what matter. The full rationale and benchmarks are in
[`design/WHY.md`](design/WHY.md).

Design documents:

- [`design/WHY.md`](design/WHY.md): rationale and measurements
- [`design/WIRE.md`](design/WIRE.md): response formats sent to the agent
- [`design/FORMAT.md`](design/FORMAT.md): file format and layout

## Development

```sh
go test ./...                              # run tests
go test -race ./...                        # with the race detector
go test -bench . ./internal/store/         # freshness-check benchmark
go vet ./...
```

```
cmd/code-mem/       CLI and server entry point
internal/memory/    memory file parsing, validation and link rewriting
internal/index/     binary index: build, memory-mapped reader, BM25
internal/store/     store discovery, freshness, repo + global merge
internal/render/    plain-text response formatting
internal/mcp/       MCP server and tool handlers
```

## Contributing

Issues and pull requests are welcome. For larger changes, please open an
issue first to discuss the approach. Make sure `go vet ./...` and
`go test -race ./...` pass before submitting.

## License

[MIT](LICENSE) © Shubham Tiwari
