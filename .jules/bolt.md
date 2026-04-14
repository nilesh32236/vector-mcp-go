## 2026-04-04 - Static Regex Pre-compilation
**Learning:** Static regular expressions used in performance-critical code paths (like `parseRelationships` in `internal/indexer/chunker.go`) cause unnecessary recompilation overhead on every function call. This is a common performance bottleneck in Go.
**Action:** Always pre-compile static regular expressions into package-level variables using `regexp.MustCompile` to avoid repeated compilation overhead.

## 2026-04-06 - Remove strings.ToLower allocations from hot loops
**Learning:** Calling `strings.ToLower` inside iteration loops (like per-line file scanning or graph node traversal) causes significant memory allocations and CPU overhead due to repeated string copying. A benchmark showed that hoisting `strings.ToLower` outside of hot loops, or early-returning on substring matches, reduces time overhead by roughly 30-40%.
**Action:** Always hoist invariant string transformations (like lowercasing the search query) outside of hot loops. When iterating, prefer to pre-lower the entire text or only lower individual elements against a pre-lowered invariant.

## 2024-05-18 - [Chunking O(N^2) Complexity]
**Learning:** In string chunking loops that advance via runes and track line numbers (e.g. `splitIfNeeded` and `fastChunk`), recalculating newlines from the very beginning of the file in each iteration (`strings.Count(string(runes[:i]), "\n")`) results in massive O(N^2) memory allocation and processing overhead as the string gets longer.
**Action:** Always maintain a running accumulator counter outside the loop (like `currentLine`) and calculate newlines only over the newly processed, non-overlapping `step` segment (`runes[i:i+step]`) to achieve O(N) performance.
