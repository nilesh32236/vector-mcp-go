## 2026-04-04 - Static Regex Pre-compilation
**Learning:** Static regular expressions used in performance-critical code paths (like `parseRelationships` in `internal/indexer/chunker.go`) cause unnecessary recompilation overhead on every function call. This is a common performance bottleneck in Go.
**Action:** Always pre-compile static regular expressions into package-level variables using `regexp.MustCompile` to avoid repeated compilation overhead.

## 2026-04-06 - Remove strings.ToLower allocations from hot loops
**Learning:** Calling `strings.ToLower` inside iteration loops (like per-line file scanning or graph node traversal) causes significant memory allocations and CPU overhead due to repeated string copying. A benchmark showed that hoisting `strings.ToLower` outside of hot loops, or early-returning on substring matches, reduces time overhead by roughly 30-40%.
**Action:** Always hoist invariant string transformations (like lowercasing the search query) outside of hot loops. When iterating, prefer to pre-lower the entire text or only lower individual elements against a pre-lowered invariant.

## 2026-04-10 - O(N^2) Allocations in String Chunking
**Learning:** When optimizing string chunking or iteration loops that track line numbers, avoid recalculating line numbers from the beginning of the file (e.g., `strings.Count(string(runes[:i]), "\n")`) as it causes O(N^2) complexity and massive memory allocations.
**Action:** Instead, maintain a running sum by counting newlines only in the newly processed (non-overlapping) segment of the text.
