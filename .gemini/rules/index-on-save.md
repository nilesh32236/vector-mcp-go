# Index-on-Save Strategy

Every time a file is modified using `replace` or `write_file`, you **MUST** immediately call the `index_file` tool for that specific path.

## Why
This ensures the Vector DB (project brain) is always synchronized with the current state of the codebase, allowing for accurate context retrieval.

## Triggers
- After a successful `replace` call.
- After a successful `write_file` call.
- After any shell command that is known to modify files (e.g., `go fmt`, `npm run lint --fix`).

## Action
Call `index_file` with the `filePath` parameter set to the relative path of the modified file.
