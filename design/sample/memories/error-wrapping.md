---
type: convention
summary: Wrap errors with %w and a verb phrase naming the failed operation
cluster: conventions
rel:
  applies_to: [searchbyte]
---

```go
if err != nil {
    return fmt.Errorf("provision tenant schema: %w", err)
}
```

Verb phrase, lowercase, no trailing punctuation, no "failed to" (the
word `error` already carries that). Always `%w`, never `%v` — callers
use `errors.Is` / `errors.As`.
