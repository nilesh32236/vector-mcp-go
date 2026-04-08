## 2026-04-07 - Path Traversal Vulnerability in MCP Handlers
**Vulnerability:** Found unvalidated absolute path resolution in `handleGetCodebaseSkeleton` (via `filepath.Join(s.cfg.ProjectRoot, targetPath)` and `filepath.IsAbs`) which allowed directory traversal outside the project bounds.
**Learning:** File paths supplied by users or via external requests must always be subjected to explicit validation.
**Prevention:** Use `s.pathValidator.ValidatePath` (from `internal/security/pathguard`) for all user-provided file paths in the MCP handler layer instead of relying solely on `filepath` functions.
