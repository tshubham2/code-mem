---
type: bug
summary: Billing job wrote 1.2k invoice rows to the wrong tenant in staging, 2026-03
cluster: auth
rel:
  caused_by: [postgres-rls-orm-bypass]
---

The nightly billing job bulk-inserted invoices without setting
`app.tenant_id`. Because the job ran as the table owner, RLS did not
filter the read that built the batch.

Caught by a support ticket, not by monitoring. Blast radius was staging
only — but the same code path was three days from production.

**Follow-up:** alert on cross-tenant row counts, not just error rates.
