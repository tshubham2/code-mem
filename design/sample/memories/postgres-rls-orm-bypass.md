---
type: bug
summary: Postgres RLS policies do not apply to the table owner unless FORCE is set
cluster: auth
rel:
  blocks: [tenant-isolation]
---

`CREATE POLICY` alone is not enough. The owning role bypasses all
policies until you run:

```sql
ALTER TABLE invoices FORCE ROW LEVEL SECURITY;
```

Our ORM and migration tool both connect as the owner, so every policy
was inert in production while passing in tests (tests ran as a
restricted role).

**Detection is hard:** there is no error. Queries just return more rows
than they should.
