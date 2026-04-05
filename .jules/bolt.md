## 2025-05-15 - Pre-compile Static Regular Expressions
**Learning:** Compiling regular expressions using `regexp.MustCompile` inside frequently called functions (such as `parseRelationships` handling every chunk, or `HybridSearch` running on each query) introduces measurable compilation overhead and slows down performance.
**Action:** Always pre-compile static regular expressions into package-level variables using `regexp.MustCompile` during initialization, rather than compiling them inline within functions.
