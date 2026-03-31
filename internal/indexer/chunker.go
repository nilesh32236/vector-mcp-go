package indexer

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/css"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/html"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

type Chunk struct {
	Content            string
	ContextualString   string
	Symbols            []string
	ParentSymbol       string
	Relationships      []string
	Type               string
	Calls              []string
	FunctionScore      float32
	StartLine          int
	EndLine            int
	Docstring          string
	StructuralMetadata map[string]string
}

type entityMatch struct {
	start int
	end   int
	chunk Chunk
}

func CreateChunks(text string, filePath string) []Chunk {
	ext := filepath.Ext(filePath)
	relationships := parseRelationships(text, ext)
	var chunks []Chunk

	if isTreeSitterSupported(ext) {
		chunks = treeSitterChunk(text, filePath)
	} else {
		chunks = fastChunk(text)
	}

	for i := range chunks {
		chunks[i].Relationships = relationships

		// Prepend parent context to content for better embedding
		if chunks[i].ParentSymbol != "" {
			scope := "Global"
			if len(chunks[i].Symbols) > 0 {
				scope = chunks[i].Symbols[0]
			}
			chunks[i].Content = fmt.Sprintf("// Context: %s -> %s\n%s", chunks[i].ParentSymbol, scope, chunks[i].Content)
		}

		docStr := "None"
		if chunks[i].Docstring != "" {
			docStr = chunks[i].Docstring
		}

		structStr := ""
		if len(chunks[i].StructuralMetadata) > 0 {
			var sb strings.Builder
			sb.WriteString(" [Structure: ")
			for k, v := range chunks[i].StructuralMetadata {
				sb.WriteString(fmt.Sprintf("%s=%s ", k, v))
			}
			sb.WriteString("]")
			structStr = sb.String()
		}

		scope := "Global"
		if len(chunks[i].Symbols) > 0 {
			scope = chunks[i].Symbols[0]
		}

		parentInfo := ""
		if chunks[i].ParentSymbol != "" {
			parentInfo = fmt.Sprintf(" Parent: %s.", chunks[i].ParentSymbol)
		}

		callsStr := "None"
		if len(chunks[i].Calls) > 0 {
			callsStr = strings.Join(chunks[i].Calls, ", ")
		}

		chunks[i].ContextualString = fmt.Sprintf("File: %s. Entity: %s.%s Type: %s. Docstring: %s. Calls: %s.%s Code:\n%s",
			filePath, scope, parentInfo, chunks[i].Type, docStr, callsStr, structStr, chunks[i].Content)
	}
	return chunks
}

func isTreeSitterSupported(ext string) bool {
	switch ext {
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".php", ".py", ".rs", ".html", ".css":
		return true
	}
	return false
}

func treeSitterChunk(content string, filePath string) []Chunk {
	ext := filepath.Ext(filePath)
	var lang *sitter.Language

	switch ext {
	case ".go":
		lang = golang.GetLanguage()
	case ".js", ".jsx":
		lang = javascript.GetLanguage()
	case ".ts":
		lang = typescript.GetLanguage()
	case ".tsx":
		lang = tsx.GetLanguage()
	case ".php":
		lang = php.GetLanguage()
	case ".py":
		lang = python.GetLanguage()
	case ".rs":
		lang = rust.GetLanguage()
	case ".html":
		lang = html.GetLanguage()
	case ".css":
		lang = css.GetLanguage()
	default:
		return fastChunk(content)
	}

	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(lang)
	tree := parser.Parse(nil, []byte(content))
	if tree == nil {
		return fastChunk(content)
	}
	defer tree.Close()

	root := tree.RootNode()

	var queries []string
	switch ext {
	case ".go":
		queries = []string{
			`(function_declaration name: (identifier) @name) @entity`,
			`(method_declaration name: (field_identifier) @name) @entity`,
			`(type_declaration (type_spec name: (type_identifier) @name type: (struct_type))) @entity`,
			`(type_declaration (type_spec name: (type_identifier) @name type: (interface_type))) @entity`,
		}
	case ".js", ".jsx", ".ts", ".tsx":
		queries = []string{
			`(export_statement declaration: (class_declaration name: (type_identifier) @name)) @entity`,
			`(class_declaration name: (type_identifier) @name) @entity`,
			`(export_statement declaration: (function_declaration name: (identifier) @name)) @entity`,
			`(function_declaration name: (identifier) @name) @entity`,
			`(export_statement (interface_declaration name: (type_identifier) @name)) @entity`,
			`(interface_declaration name: (type_identifier) @name) @entity`,
			`(method_definition name: (property_identifier) @name) @entity`,
			`(lexical_declaration (variable_declarator name: (identifier) @name value: [(arrow_function) (function)])) @entity`,
			`(export_statement declaration: (lexical_declaration (variable_declarator name: (identifier) @name value: [(arrow_function) (function)]))) @entity`,
		}
	case ".php":
		queries = []string{
			`(class_declaration name: (name) @name) @entity`,
			`(method_declaration name: (name) @name) @entity`,
			`(function_declaration name: (name) @name) @entity`,
			`(interface_declaration name: (name) @name) @entity`,
			`(function_call_expression function: (name) @name arguments: (arguments (argument (string) @hook_name) (argument [(anonymous_function_creation_expression) (arrow_function)]))) @entity`,
		}
	case ".py":
		queries = []string{
			`(class_definition name: (identifier) @name) @entity`,
			`(function_definition name: (identifier) @name) @entity`,
		}
	case ".rs":
		queries = []string{
			`(struct_item name: (type_identifier) @name) @entity`,
			`(enum_item name: (type_identifier) @name) @entity`,
			`(function_item name: (identifier) @name) @entity`,
			`(impl_item type: (type_identifier) @name) @entity`,
			`(trait_item name: (type_identifier) @name) @entity`,
		}
	case ".html":
		queries = []string{
			`(element (start_tag (tag_name) @name)) @entity`,
			`(element (self_closing_tag (tag_name) @name)) @entity`,
			`(element (start_tag (tag_name) @name (attribute (attribute_name) @attr_name (quoted_attribute_value) @attr_val))) @entity`,
		}
	case ".css":
		queries = []string{
			`(rule_set (selectors (class_selector (class_name) @name))) @entity`,
			`(rule_set (selectors (id_selector (id_name) @name))) @entity`,
			`(rule_set (selectors (tag_name) @name)) @entity`,
			`(at_rule name: (at_keyword) @name) @entity`,
		}
	}

	var matches []entityMatch
	seen := make(map[string]bool)

	for _, qStr := range queries {
		func(q string) {
			query, err := sitter.NewQuery([]byte(q), lang)
			if err != nil {
				return
			}
			defer query.Close()

			qc := sitter.NewQueryCursor()
			defer qc.Close()

			qc.Exec(query, root)

			for {
				match, ok := qc.NextMatch()
				if !ok {
					break
				}

				var entityNode *sitter.Node
				var nameNode *sitter.Node

				for _, capture := range match.Captures {
					captureName := query.CaptureNameForId(capture.Index)
					if captureName == "entity" {
						entityNode = capture.Node
					} else if captureName == "name" || captureName == "hook_name" {
						nameNode = capture.Node
					}
				}

				if entityNode != nil {
					start := int(entityNode.StartByte())
					end := int(entityNode.EndByte())
					key := fmt.Sprintf("%d-%d", start, end)

					if !seen[key] {
						seen[key] = true

						symbolName := "Unknown"
						if nameNode != nil {
							symbolName = nameNode.Content([]byte(content))
						}

						parentSymbol := ""
						// Find parent symbol (e.g., Class or Struct)
						if entityNode.Type() == "method_definition" {
							// TS/JS
							p := entityNode.Parent()
							for p != nil {
								// In TS/JS, method is in class_body, parent of class_body is class_declaration
								if p.Type() == "class_declaration" || p.Type() == "class_definition" {
									// Find the name of the class
									for i := 0; i < int(p.ChildCount()); i++ {
										c := p.Child(i)
										if c.Type() == "type_identifier" || c.Type() == "identifier" {
											parentSymbol = c.Content([]byte(content))
											break
										}
									}
									break
								}
								p = p.Parent()
							}
						} else if entityNode.Type() == "method_declaration" {
							// Go
							recv := entityNode.ChildByFieldName("receiver")
							if recv != nil {
								// Look for type_identifier inside receiver
								var findType func(*sitter.Node) string
								findType = func(n *sitter.Node) string {
									if n.Type() == "type_identifier" {
										return n.Content([]byte(content))
									}
									for i := 0; i < int(n.ChildCount()); i++ {
										if t := findType(n.Child(i)); t != "" {
											return t
										}
									}
									return ""
								}
								parentSymbol = findType(recv)
							}
						}

						chunkType := entityNode.Type()
						calls := extractCallsGeneric(entityNode, content)
						score := calculateScoreGeneric(entityNode, calls)
						docstring := associateDocstrings(entityNode, content)
						structMeta := extractStructuralMetadata(entityNode, content)

						matches = append(matches, entityMatch{
							start: start,
							end:   end,
							chunk: Chunk{
								Content:            string(content[start:end]),
								Symbols:            []string{symbolName},
								ParentSymbol:       parentSymbol,
								Type:               chunkType,
								Calls:              calls,
								FunctionScore:      score,
								Docstring:          docstring,
								StructuralMetadata: structMeta,
								StartLine:          int(entityNode.StartPoint().Row) + 1,
								EndLine:            int(entityNode.EndPoint().Row) + 1,
							},
						})
					}
				}
			}
		}(qStr)
	}

	// Identify top-level entities for gap filling
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].start != matches[j].start {
			return matches[i].start < matches[j].start
		}
		return matches[i].end > matches[j].end
	})

	var topLevel []entityMatch
	for _, m := range matches {
		isContained := false
		for _, tl := range topLevel {
			if m.start >= tl.start && m.end <= tl.end {
				isContained = true
				break
			}
		}
		if !isContained {
			topLevel = append(topLevel, m)
		}
	}

	// Calculate gaps and fill with Unknown chunks
	var allChunks []Chunk
	lastEnd := 0
	contentBytes := []byte(content)

	for _, tl := range topLevel {
		// Add gap before this top-level entity
		if tl.start > lastEnd {
			gapContent := string(contentBytes[lastEnd:tl.start])
			if strings.TrimSpace(gapContent) != "" {
				gapChunks := splitIfNeeded(Chunk{
					Content:   gapContent,
					Type:      "Unknown",
					StartLine: countLines(string(contentBytes[:lastEnd])) + 1,
					EndLine:   countLines(string(contentBytes[:tl.start])),
				})
				allChunks = append(allChunks, gapChunks...)
			}
		}
		lastEnd = tl.end
	}

	// Add final gap
	if lastEnd < len(contentBytes) {
		gapContent := string(contentBytes[lastEnd:])
		if strings.TrimSpace(gapContent) != "" {
			gapChunks := splitIfNeeded(Chunk{
				Content:   gapContent,
				Type:      "Unknown",
				StartLine: countLines(string(contentBytes[:lastEnd])) + 1,
				EndLine:   countLines(string(contentBytes)),
			})
			allChunks = append(allChunks, gapChunks...)
		}
	}

	// Add all semantic matches, splitting large ones
	for _, m := range matches {
		allChunks = append(allChunks, splitIfNeeded(m.chunk)...)
	}

	if len(allChunks) == 0 {
		return fastChunk(content)
	}

	return allChunks
}

func associateDocstrings(node *sitter.Node, content string) string {
	contentBytes := []byte(content)
	parent := node.Parent()
	if parent == nil {
		return ""
	}

	var comments []string

	// Check siblings before this node
	for i := 0; i < int(parent.ChildCount()); i++ {
		child := parent.Child(i)
		if child.StartByte() >= node.StartByte() {
			break
		}
		if child.Type() == "comment" || child.Type() == "line_comment" || child.Type() == "block_comment" {
			// Only take comments that are visually close (last child before us)
			// Simple heuristic: if there's more than 1 line between comment and node, skip
			if int(node.StartPoint().Row-child.EndPoint().Row) <= 1 {
				comments = append(comments, child.Content(contentBytes))
			} else {
				comments = nil // Reset if we find a "stale" comment
			}
		} else if strings.TrimSpace(child.Content(contentBytes)) != "" {
			comments = nil // Reset if we find unrelated code between comments and node
		}
	}

	return strings.Join(comments, "\n")
}

func extractStructuralMetadata(node *sitter.Node, content string) map[string]string {
	meta := make(map[string]string)
	contentBytes := []byte(content)

	var traverse func(n *sitter.Node)
	traverse = func(n *sitter.Node) {
		if n == nil {
			return
		}

		t := n.Type()

		// Go Struct fields (can be in struct_type or directly in type_spec if simplified)
		if t == "field_declaration" {
			nameNode := n.ChildByFieldName("name")
			typeNode := n.ChildByFieldName("type")

			// Fallback for name
			if nameNode == nil {
				for i := 0; i < int(n.ChildCount()); i++ {
					child := n.Child(i)
					if child.Type() == "field_identifier" || child.Type() == "identifier" {
						nameNode = child
						break
					}
				}
			}

			// Fallback for type (usually the last child that is a type or identifier)
			if typeNode == nil && nameNode != nil {
				for i := int(n.ChildCount()) - 1; i >= 0; i-- {
					child := n.Child(i)
					if child != nameNode && (strings.Contains(child.Type(), "type") || child.Type() == "identifier") {
						typeNode = child
						break
					}
				}
			}

			if nameNode != nil && typeNode != nil {
				meta["field:"+nameNode.Content(contentBytes)] = typeNode.Content(contentBytes)
			}
		}

		// Go Interface methods
		if t == "method_spec" || t == "method_elem" {
			nameNode := n.ChildByFieldName("name")
			if nameNode == nil {
				for i := 0; i < int(n.ChildCount()); i++ {
					child := n.Child(i)
					if child.Type() == "field_identifier" || child.Type() == "identifier" {
						nameNode = child
						break
					}
				}
			}
			if nameNode != nil {
				meta["method:"+nameNode.Content(contentBytes)] = "defined"
			}
		}
		// TS/JS Class properties
		if t == "public_field_definition" || t == "property_definition" {
			nameNode := n.ChildByFieldName("property")
			if nameNode == nil {
				nameNode = n.ChildByFieldName("name")
			}
			if nameNode != nil {
				meta["prop:"+nameNode.Content(contentBytes)] = "defined"
			}
		}

		for i := 0; i < int(n.ChildCount()); i++ {
			traverse(n.Child(i))
		}
	}
	traverse(node)
	return meta
}

func countLines(s string) int {
	return strings.Count(s, "\n")
}

func splitIfNeeded(c Chunk) []Chunk {
	runes := []rune(c.Content)
	maxRunes := 3000
	overlap := 500

	if len(runes) <= maxRunes {
		return []Chunk{c}
	}

	var chunks []Chunk
	for i := 0; i < len(runes); {
		end := i + maxRunes
		if end > len(runes) {
			end = len(runes)
		}

		subContent := string(runes[i:end])

		// Approximate lines
		linesInSub := strings.Count(subContent, "\n")

		newChunk := c
		newChunk.Content = subContent
		newChunk.EndLine = newChunk.StartLine + linesInSub
		// Adjust start line for subsequent chunks
		if i > 0 {
			linesBefore := strings.Count(string(runes[:i]), "\n")
			newChunk.StartLine = c.StartLine + linesBefore
		}

		chunks = append(chunks, newChunk)

		if end == len(runes) {
			break
		}
		i += (maxRunes - overlap)
	}
	return chunks
}

func extractCallsGeneric(node *sitter.Node, content string) []string {
	uniqueCalls := make(map[string]bool)
	contentBytes := []byte(content)
	var traverse func(n *sitter.Node)
	traverse = func(n *sitter.Node) {
		if n == nil {
			return
		}
		t := n.Type()
		if t == "call_expression" || t == "function_call_expression" {
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				ct := child.Type()
				if ct == "identifier" || ct == "property_identifier" || ct == "name" {
					uniqueCalls[child.Content(contentBytes)] = true
				} else if ct == "selector_expression" || ct == "member_expression" {
					if child.ChildCount() > 0 {
						lastChild := child.Child(int(child.ChildCount()) - 1)
						if lastChild.Type() == "field_identifier" || lastChild.Type() == "property_identifier" {
							uniqueCalls[lastChild.Content([]byte(content))] = true
						}
					}
				}
			}
		}

		for i := 0; i < int(n.ChildCount()); i++ {
			traverse(n.Child(i))
		}
	}
	traverse(node)

	var calls []string
	for c := range uniqueCalls {
		calls = append(calls, c)
	}
	sort.Strings(calls)
	return calls
}

func calculateScoreGeneric(node *sitter.Node, calls []string) float32 {
	score := float32(1.0)

	lines := int(node.EndPoint().Row - node.StartPoint().Row + 1)
	if lines < 3 {
		score -= 0.3
	} else if lines > 10 {
		score += 0.2
	}

	score += float32(len(calls)) * 0.1
	return score
}

func parseRelationships(text string, ext string) []string {
	var relations []string
	if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
		importRegex := regexp.MustCompile(`(?:import|from|require)\s*\(?\s*['"]([^'"]+)['"]`)
		matches := importRegex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				relations = append(relations, m[1])
			}
		}

		namedImportRegex := regexp.MustCompile(`import\s*{([^}]+)}`)
		namedMatches := namedImportRegex.FindAllStringSubmatch(text, -1)
		for _, m := range namedMatches {
			if len(m) > 1 {
				names := strings.Split(m[1], ",")
				for _, name := range names {
					relations = append(relations, strings.TrimSpace(strings.Split(name, " as ")[0]))
				}
			}
		}
	} else if ext == ".go" {
		singleImportRegex := regexp.MustCompile(`import\s+(?:[a-zA-Z0-9_.]+\s+)?["']([^"']+)["']`)
		matches := singleImportRegex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				relations = append(relations, m[1])
			}
		}

		blockRegex := regexp.MustCompile(`import\s+\(([\s\S]*?)\)`)
		blocks := blockRegex.FindAllStringSubmatch(text, -1)
		for _, b := range blocks {
			if len(b) > 1 {
				inner := b[1]
				innerRegex := regexp.MustCompile(`["']([^"']+)["']`)
				innerMatches := innerRegex.FindAllStringSubmatch(inner, -1)
				for _, im := range innerMatches {
					if len(im) > 1 {
						relations = append(relations, im[1])
					}
				}
			}
		}
	} else if ext == ".php" {
		reqRegex := regexp.MustCompile(`(?:require|require_once|include|include_once)\s*\(?\s*['"]([^'"]+)['"]`)
		matches := reqRegex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				relations = append(relations, m[1])
			}
		}

		useRegex := regexp.MustCompile(`use\s+([^;]+);`)
		useMatches := useRegex.FindAllStringSubmatch(text, -1)
		for _, m := range useMatches {
			if len(m) > 1 {
				parts := strings.Split(m[1], ",")
				for _, p := range parts {
					relations = append(relations, strings.TrimSpace(strings.Split(p, " as ")[0]))
				}
			}
		}
	} else if ext == ".html" {
		srcRegex := regexp.MustCompile(`(?:src|href)\s*=\s*['"]([^'"]+)['"]`)
		matches := srcRegex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				relations = append(relations, m[1])
			}
		}
	} else if ext == ".css" {
		importRegex := regexp.MustCompile(`@import\s+(?:url\()?['"]([^'"]+)['"]\)?`)
		matches := importRegex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				relations = append(relations, m[1])
			}
		}
	}

	unique := make(map[string]bool)
	var result []string
	for _, r := range relations {
		if !unique[r] {
			unique[r] = true
			result = append(result, r)
		}
	}
	return result
}

func fastChunk(text string) []Chunk {
	var chunks []Chunk
	runes := []rune(text)
	chunkSize := 3000
	overlap := 500

	if len(runes) == 0 {
		return nil
	}

	for i := 0; i < len(runes); {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}

		content := string(runes[i:end])
		startLine := strings.Count(string(runes[:i]), "\n") + 1
		endLine := startLine + strings.Count(content, "\n")

		chunks = append(chunks, Chunk{
			Content:          content,
			ContextualString: content,
			StartLine:        startLine,
			EndLine:          endLine,
		})

		if end == len(runes) {
			break
		}

		i += (chunkSize - overlap)
	}
	return chunks
}
