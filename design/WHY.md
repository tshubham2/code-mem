# code-mem — why this shape (diagrams)

All numbers below were MEASURED on one machine (M-series mac, Go 1.23.5),
100,000 memories / 600,000 edges. Reproduce: see bench notes at bottom.

────────────────────────────────────────────────────────────────────────
1. WHAT AN AGENT ACTUALLY GOES THROUGH
────────────────────────────────────────────────────────────────────────

QUESTION: "why aren't we using RLS for tenant isolation?"

  ┌─ today: flat AGENTS.md ────────────────────────────────────────┐
  │                                                                │
  │  the whole file is in context, every turn, forever             │
  │  ███████████████████████████████  ~10,000 tok  EVERY TURN      │
  │                                                                │
  │  answer present only if a human remembered to write it there   │
  │  grows until someone prunes it. never prunes itself.           │
  └────────────────────────────────────────────────────────────────┘

  ┌─ today: vector/entity memory MCP ──────────────────────────────┐
  │                                                                │
  │  0. agent must GUESS a memory exists ......... usually SKIPS   │
  │     ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^  the real failure mode    │
  │  1. search("RLS")            -> 5 loose chunks ...... ~800 tok │
  │  2. chunks have no "why"     -> search again ........ ~800 tok │
  │  3. still no history         -> answers from priors            │
  │                                          total ~1,600 tok,     │
  │                                          2 round trips,        │
  │                                          incomplete answer     │
  └────────────────────────────────────────────────────────────────┘

  ┌─ code-mem ─────────────────────────────────────────────────────┐
  │                                                                │
  │  0. index.md sits in context .......................... 80 tok │
  │     "auth (12) - tenant isolation, JWT rotation, RLS"          │
  │     ^ the agent can SEE that an answer exists                  │
  │  1. mem_get("tenant-isolation", hops:1) ............. ~180 tok │
  │     -> the decision + the bug that caused it                   │
  │        + the incident + the design it replaced                 │
  │                                                                │
  │                              total ~260 tok, ONE round trip,   │
  │                              complete answer with causality    │
  └────────────────────────────────────────────────────────────────┘

The win is not speed. It is that step 0 exists at all, and that step 1
returns the *why* without the agent knowing to ask for it.

────────────────────────────────────────────────────────────────────────
2. WHY A GRAPH BEATS SIMILARITY (precision vs recall)
────────────────────────────────────────────────────────────────────────

  embedding search                      graph expansion
  "things that SOUND alike"             "things a human SAID are linked"

      query "RLS"                            query "RLS"
           |                                      |
           v                                      v
   ┌──────────────┐ 0.81               ┌────────────────────┐
   │ chunk: auth  │                    │  tenant-isolation  │  BM25 hit
   ├──────────────┤ 0.79               └─────────┬──────────┘
   │ chunk: perms │                              │ ONE reslice
   ├──────────────┤ 0.77               ┌─────────┼──────────┐
   │ chunk: roles │                    v         v          v
   └──────────────┘                caused_by  replaces   mentions
                                       |         |          |
   3 chunks that share                 v         v          v
   VOCABULARY with the             the real  the design  the exact
   query. none explain              incident  we dropped  gotcha
   why the decision was
   made.                           causality, authored deliberately

Similarity finds neighbours in WORD space. The graph finds neighbours in
DECISION space. Only one of them answers "why".

────────────────────────────────────────────────────────────────────────
3. STARTUP COST  (MCP servers respawn on every editor launch / subagent)
────────────────────────────────────────────────────────────────────────

  100k memories, 600k edges.   MEASURED, best of 2 runs:

  Python  json.load    ██████████████████████████████  58.2 ms   108 MB
  Node    JSON.parse   ████████████████                31.7 ms   131 MB
  Go      mmap         ▏                                0.13 ms     8 MB
                       ^
                       one syscall. nothing is parsed.

           ~250x faster than Node      ~16x less memory
           ~450x faster than Python

  on-disk:   graph.json  9.5 MB        graph.csr  2.7 MB   (3.5x smaller)

  And mmap is LAZY: the kernel maps the address range but reads nothing
  until a page is touched. Traverse 3 nodes of a 40 MB graph and you fault
  in ~8 KB. "Loading" a huge graph costs the same as a tiny one.

  MAP_SHARED bonus: two editors running agents at once map the SAME
  physical pages. One copy in RAM no matter how many agents attach.

────────────────────────────────────────────────────────────────────────
4. WHY THE LAYOUT IS FAST  (measured 1.0 ns per one-hop traversal)
────────────────────────────────────────────────────────────────────────

  map[string][]string                   CSR
  ───────────────────                   ───

  "tenant-isolation"                    offsets[5]=3  offsets[6]=7
        | hash + bucket probe                 |
        v                                     v
   []string header                      targets[3:7]
        |                                ┌───┬───┬───┬───┐
        ├──ptr──> "postgres-rls..."      │ 1 │ 2 │ 3 │ 4 │  16 bytes
        ├──ptr──> "searchbyte"           └───┴───┴───┴───┘
        ├──ptr──> "shared-schema..."      all inside ONE 64-byte
        └──ptr──> "staging-tenant..."     cache line

   4 heap objects, scattered             0 allocations
   4 likely cache misses                 1 memory access
   GC traces every pointer,              GC skips it entirely
   every cycle, forever                  ([]uint32 has no pointers)

  Measured: 1,000,000 one-hop traversals, 0 heap allocations, ~1 ns/op.

────────────────────────────────────────────────────────────────────────
HONEST CAVEATS
────────────────────────────────────────────────────────────────────────

- At a few THOUSAND memories a plain Go map is completely fine. The
  traversal speed in (4) is not the reason to do this.
- The reason that holds at ANY size is (3): a short-lived, constantly
  respawned process wants zero-parse startup.
- (1) and (2) are the actual product. (3) and (4) are why Go instead of
  TypeScript, not why the design is right.
- Numbers in (3) compare parsing JSON to not parsing anything. A Node
  server using a binary format would close much of that gap - it just
  would not be idiomatic, and none of the existing ones do it.

Bench (one-off harness, not part of this repo): 100k-node synthetic graph,
json.load vs JSON.parse vs syscall.Mmap + unsafe.Slice. The store's own
freshness benchmark is `go test -bench . ./internal/store/`.
