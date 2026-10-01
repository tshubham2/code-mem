# code-mem — file format

Status: **v1 implemented** (see `README.md`). See `sample/` for real files.

## Two planes

    ┌─ CONTENT PLANE ──────────────────────┐  text, because text is what
    │  memories/*.md                        │  crosses the wire to a model.
    │  frontmatter + prose + [[links]]      │  portable, diffable.
    │  SOURCE OF TRUTH                      │  optimized for TOKENS.
    └───────────────────────────────────────┘
                     │ derived, regenerable
                     ▼
    ┌─ INDEX PLANE ────────────────────────┐  pure math. never sent to a
    │  .index/*.dat                         │  model. delete anytime.
    │  term IDs, postings, CSR graph        │  optimized for SPEED + SIZE.
    │  A CACHE, NOT A DATABASE              │
    └───────────────────────────────────────┘

Deleting `.index/` must always be safe. That is what makes a file-based
store viable: the only thing that can corrupt is a cache, and `git pull`
bringing in a teammate's memories is just a reindex.

## Directory layout

    .mem/
      memories/                 flat. slugs are globally unique.
        tenant-isolation.md
        postgres-rls-orm-bypass.md
      index.md                  generated boot index, committed
      .gitignore                ".index/"
      .index/                   gitignored, regenerable
        index.bin               node table + CSR (out and in) + BM25 postings,
                                one mmap'd file, stamped with a fingerprint
                                of every memory file's name/size/mtime

Memories are **flat, not foldered by cluster**: `[[links]]` resolve
without paths, a memory can belong to several clusters, and there is
never a "which folder does this go in" decision.

## A memory file

    ---
    type: decision
    summary: SearchByte isolates tenants with one Postgres schema each, not RLS
    cluster: auth
    rel:
      decided_for: [searchbyte]
      replaces:    [shared-schema-rollout]
      caused_by:   [staging-tenant-leak-2026-03]
    ---

    Prose body. Links to other memories inline: [[postgres-rls-orm-bypass]].

### Fields

| field     | required | notes |
|-----------|----------|-------|
| (id)      | —        | the **filename** is the id. no field. |
| `type`    | yes      | decision / bug / convention / pattern / service / person / preference |
| `summary` | yes      | ONE line. the claim, not the topic. |
| `cluster` | yes      | groups memories in the boot index |
| `rel`     | no       | typed outbound edges, active voice |
| (dates)   | —        | from `git log`, else mtime. never a field. |

### Why no `id` or timestamp fields

Anything an agent writes, an agent can desync. The filename cannot
disagree with itself, and git already records who changed what, when,
with better fidelity than a `updated:` line an agent forgets to bump.

### Why frontmatter keys are spelled out, not golfed

Frontmatter is parsed by Go, not read by a model. Only `summary` reaches
the context window (via the index). So there is no token argument for
`t:`/`s:`/`c:` — spend the clarity, keep the tokens for the wire.

### `summary` is the load-bearing field

It is the only line that enters the index, so it is what retrieval
matches against and what an agent reads before deciding whether to pull
the body. Enforce: one line, states a claim.

    good: Postgres RLS does not apply to the table owner unless FORCE is set
    bad:  Notes about row-level security

## Edges

Two kinds, one graph:

- **`rel:` frontmatter** — structural, typed, queryable. Active voice
  (`caused_by`, `replaces`, `depends_on`, `decided_for`).
- **`[[wikilinks]]` in prose** — associative, free to write mid-sentence.
  Parsed into edges of type `mentions`.

Dangling links are **legal and meaningful** — `[[not-yet-written]]` marks
something worth capturing later. `mem_check` reports them; nothing
forbids them.

## Resolved questions

1. **Rename + backlink rewrite.** `mem_rename` rewrites every `rel:`
   target and `[[link]]` that points at the old slug, byte-for-byte
   otherwise. It rewrites the store that owns the memory and every store
   that takes precedence over it; stores below cannot see it.
2. **`cluster`: agent-assigned.** `mem_save`'s tool description lists the
   existing clusters, and saving into a new one says so, so the agent
   reuses the taxonomy instead of inventing near-duplicates.
3. **Scope: repo + global, merged at query time.** The repo store is the
   nearest `.mem/` above the working directory (else `<git root>/.mem`,
   created on first save); the global store is `$CODE_MEM_GLOBAL` or
   `~/.mem`. A repo slug shadows the same global slug, and links resolve
   across stores by slug. Each store keeps its own index.
