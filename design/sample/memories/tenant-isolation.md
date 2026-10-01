---
type: decision
summary: SearchByte isolates tenants with one Postgres schema each, not RLS
cluster: auth
rel:
  decided_for: [searchbyte]
  replaces:    [shared-schema-rollout]
  caused_by:   [staging-tenant-leak-2026-03]
---

Each tenant gets its own Postgres schema; the connection pool sets
`search_path` per request.

**Why not RLS:** the ORM connects as the table owner, which bypasses
row-level policies entirely ([[postgres-rls-orm-bypass]]). Rows leaked
across tenants in staging before anyone noticed.

**Cost:** ~40ms to provision a schema. Acceptable — tenant creation is
rare and already async.
