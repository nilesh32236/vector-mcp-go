## 2026-04-07 - Path Traversal Vulnerability in MCP Handlers
**Vulnerability:** Found unvalidated absolute path resolution in `handleGetCodebaseSkeleton` (via `filepath.Join(s.cfg.ProjectRoot, targetPath)` and `filepath.IsAbs`) which allowed directory traversal outside the project bounds.
**Learning:** File paths supplied by users or via external requests must always be subjected to explicit validation.
**Prevention:** Use `s.pathValidator.ValidatePath` (from `internal/security/pathguard`) for all user-provided file paths in the MCP handler layer instead of relying solely on `filepath` functions.

## 2026-04-08 - Path Validator Not Updated on Project Root Change
**Vulnerability:** The `handleSetProjectRoot` tool allowed updating `s.cfg.ProjectRoot` without re-initializing `s.pathValidator`.
**Learning:** Security components (like path validators) that rely on configuration state (like `ProjectRoot`) must be updated synchronously when that state changes, otherwise validation bypass or false positives may occur.
**Prevention:** Always re-initialize dependent security components when configuration state changes.

## 2025-04-18 - [CRITICAL] Path Traversal in MCP Handlers Bypassing pathValidator
**Vulnerability:** The `handleVerifyPatchIntegrity` MCP handler in `internal/mcp/handlers_safety.go` failed to validate the user-provided `path` using `s.validatePath(path)` before passing it to `s.safety.VerifyPatchIntegrity()`.
**Learning:** `handleVerifyPatchIntegrity` is an internal sub-handler that could be called with unvalidated input (e.g. from `handleModifyWorkspace` -> `verify_patch`), which allowed reading/interacting with files outside the allowed project workspace.
**Prevention:** Always validate file paths derived from user input using `s.validatePath(path)` as early as possible in every individual MCP handler to ensure comprehensive path validation and security within boundaries.
