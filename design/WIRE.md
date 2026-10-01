# code-mem — what crosses the wire (draft)

The MCP tool *responses* are the product. The on-disk format is an
implementation detail; this is what an agent actually pays tokens for.

Format rule: no JSON in responses. JSON costs 30-40% more tokens than
equivalent plain text for the same content (quotes, braces, keys repeated
per record) and communicates nothing extra to a model.

## Boot: `index.md` in context

    # searchbyte — 47 memories

    - **auth** (12) — tenant isolation, JWT rotation, RLS rejection
    - **deploy** (9) — ECS sizing, blue/green cutover, migration gate
    - **known-bugs** (7) — NATS redelivery, S3 eventual consistency
    - **conventions** (11) — error wrapping, test layout, naming

~80 tokens, CONSTANT regardless of corpus size.

This is the fix for the main failure mode of every existing memory MCP:
the agent never queries it, because nothing in context suggests there is
anything to find. Clusters make existence visible for ~80 tokens.

> **Revised after benchmarking (2026-10).** The first version below sent
> summaries only. In end-to-end tests agents answered from those summaries
> and left out details that live in the body (dates, numbers, formats).
> Search hits now carry a ~500-byte body excerpt and direct neighbors in
> `mem_get` a ~400-byte excerpt, cut on a word boundary and marked
> `…(truncated; mem_get for the full note)`. The server instructions also
> tell the agent that summaries are partial. In end-to-end tests at 1,059
> memories this raised answer accuracy from 88% to 98% and cut tool calls
> per answer from 2.9 to 1.9.

## `mem_search("tenant isolation")`

    tenant-isolation · decision · auth
      SearchByte isolates tenants with one Postgres schema each, not RLS
      → caused_by staging-tenant-leak-2026-03 · replaces shared-schema-rollout

    postgres-rls-orm-bypass · bug · auth
      Postgres RLS policies do not apply to the table owner unless FORCE is set
      → blocks tenant-isolation

~70 tokens. Each hit carries its graph neighborhood, so the agent can see
what else is nearby without a second call.

## `mem_get("tenant-isolation", hops: 1)`

The call that earns the graph.

    ## tenant-isolation · decision · auth

    Each tenant gets its own Postgres schema; the connection pool sets
    search_path per request.

    Why not RLS: the ORM connects as the table owner, which bypasses
    row-level policies entirely. Rows leaked across tenants in staging.

    Cost: ~40ms to provision a schema. Acceptable.

    neighbors:
      caused_by → staging-tenant-leak-2026-03
         Billing job wrote 1.2k invoice rows to the wrong tenant, 2026-03
      replaces  → shared-schema-rollout
         Original design: one schema, tenant_id column on every table
      mentions  → postgres-rls-orm-bypass
         Postgres RLS does not apply to the table owner unless FORCE is set

**Full body of the target, summaries only of the neighbors.** One call
returns the decision, the incident that caused it, and the design it
replaced. The agent gets the *why* and the *history* without knowing to
ask for them.

That is the whole thesis: buy retrieval precision with graph structure
instead of buying recall with embeddings.

## Index plane (never sent to a model)

    graph.csr   offsets []uint32   // len = nodes+1
                targets []uint32   // len = edges
                etypes  []uint8    // parallel to targets

    one-hop:  targets[offsets[n] : offsets[n+1]]    // a reslice. zero alloc.

Plus: interned uint32 term ids -> delta+varint postings (BM25), roaring
bitmaps for cluster/type filters, one uint64 simhash per memory so
"agent is re-saving a fact it already saved" is an XOR and a popcount.

All mmap'd. This is where the Go choice actually pays off -- not startup
time, but zero-alloc traversal that Node and Python MCP servers cannot
reach.
