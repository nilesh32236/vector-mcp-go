---
name: vector-context
description: Use Vector MCP Go for deep codebase analysis, semantic search, and project context management.
---

# Vector Context Skill

This skill allows AI agents to leverage the `vector-mcp-go` server for high-performance semantic search and project context retrieval.

## 📋 Capabilities

- **Deep Context Retrieval**: Get comprehensive context for a specific file, including its symbols and dependencies.
- **Semantic Search**: Find relevant code chunks across the project using natural language queries.
- **Monorepo Support**: Dynamically switch project roots and resolve internal dependencies.
- **Duplication Analysis**: Identify redundant logic across the codebase.
- **Architectural Knowledge**: Store and retrieve project-wide rules and decisions.

## 🛠️ Usage Patterns

### 1. Initial Project Scoping
When starting work on a new project, always check the index status and trigger a full index if necessary:
1. `index_status()`: Check if the project is indexed.
2. `trigger_project_index(project_path)`: Start indexing if missing or outdated.
3. `get_codebase_skeleton()`: Understand the high-level structure.

### 2. Implementation Context
Before modifying a file, retrieve its full semantic context:
1. `get_related_context(filePath)`: Get chunked code, imports, and resolved local dependencies.
2. `search_codebase(query)`: Search for specific functions, components, or concepts across the codebase.
3. This provides much more depth than just reading the file content directly.

### 3. Refactoring and Optimization
Use the duplication tool to find consolidation opportunities:
1. `find_duplicate_code(target_path)`: Discover similar logic in other modules.

### 4. Knowledge Sharing
Maintain technical debt or architectural records:
1. `store_context(text)`: Record a decision (e.g., "Use Tailwind for all new components").
2. This information persists and can be retrieved via `search_codebase` later.

## 💡 Best Practices

- **Absolute Paths**: Always use absolute paths for `project_path` and `filePath`.
- **Top-K Tuning**: Use semantic search with a reasonable `topK` (default is 5) to balance breadth and token usage.
- **Dynamic Updates**: If you make major structural changes, call `trigger_project_index` to ensure your search results remain accurate.
- **Context Window Awareness**: When using `search_codebase`, always pay attention to the `max_tokens` limit. If your search results are truncated, fallback to using `filesystem_grep` to narrow down the exact file or line numbers.
