## 2026-04-04 - Static Regex Pre-compilation
**Learning:** Static regular expressions used in performance-critical code paths (like `parseRelationships` in `internal/indexer/chunker.go`) cause unnecessary recompilation overhead on every function call. This is a common performance bottleneck in Go.
**Action:** Always pre-compile static regular expressions into package-level variables using `regexp.MustCompile` to avoid repeated compilation overhead.
