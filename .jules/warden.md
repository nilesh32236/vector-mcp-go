## 2026-04-11 - Enforce Idiomatic Error Handling
**Anti-Pattern:** Returning bare errors (e.g. `return err`) without adding context. This makes it difficult to trace the origin of an error when it bubbles up to the top level.
**Standard:** Always wrap errors with `fmt.Errorf("failed to do X: %w", err)` to preserve the error chain and provide clear context about where and why the error occurred.
