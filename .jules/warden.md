## $(date +%Y-%m-%d) - Missing Error Context in LSP Client
**Anti-Pattern:** Returning bare errors (`return err`) or using `%v` instead of `%w` when wrapping errors, especially in LSP communication where request context (`method`, `id`) is crucial for debugging.
**Standard:** Always wrap errors using `fmt.Errorf("... %w", err)`. For LSP communication, specifically include context in the format: `fmt.Errorf("... (method=%s id=%v): %w", method, id, err)`.
