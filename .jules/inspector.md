## 2025-04-13 - Fat Tool Search Workspace Coverage
**Blindspot:** The unified 'search_workspace' Fat Tool (`handleSearchWorkspace`) and its action branches (vector, regex, graph, index_status) were completely missing test coverage, including panic scenarios around malicious input constraints and limits clamping.
**Coverage:** Added a table-driven test `TestHandleSearchWorkspace` covering all enum branches (vector, regex, graph, index_status) and verifying proper limits clamping using `util.ClampInt` to prevent out-of-bounds panics, along with multi-byte UTF-8 coverage for text search.
