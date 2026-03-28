package indexer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

type Chunk struct {
	Content       string
	Symbols       []string
	Relationships []string
	Type          string   // "function", "class", etc.
	Calls         []string // Function calls made inside this block
	FunctionScore float32
	StartLine     int
	EndLine       int
}

func CreateChunks(text string, filePath string) []Chunk {
	ext := filepath.Ext(filePath)
	relationships := parseRelationships(text, ext)
	var chunks []Chunk

	if ext == ".go" {
		chunks = astChunkGo(text)
	} else if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
		chunks = treeSitterChunkJS(text, filePath)
	} else {
		chunks = fastChunk(text)
	}

	// Inject Context Headers and attach relationships
	for i := range chunks {
		scope := "Global"
		if len(chunks[i].Symbols) > 0 {
			scope = chunks[i].Symbols[0]
		}

		callsStr := "None"
		if len(chunks[i].Calls) > 0 {
			callsStr = strings.Join(chunks[i].Calls, ", ")
		}

		header := fmt.Sprintf("// File: %s\n// Entity: %s\n// Type: %s\n// Calls: %s\n// Score: %.2f\n// Functionality: \n\n",
			filePath, scope, chunks[i].Type, callsStr, chunks[i].FunctionScore)
		chunks[i].Content = header + chunks[i].Content
		chunks[i].Relationships = relationships
	}
	return chunks
}

func treeSitterChunkJS(content string, filePath string) []Chunk {
	ext := filepath.Ext(filePath)
	var lang *gotreesitter.Language
	switch ext {
	case ".ts":
		lang = grammars.TypescriptLanguage()
	case ".tsx":
		lang = grammars.TsxLanguage()
	default:
		lang = grammars.JavascriptLanguage()
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse([]byte(content))
	if err != nil {
		return chunkJavaScriptTypeScript(content, filePath)
	}

	root := tree.RootNode()

	// Queries for functions, classes, interfaces, etc.
	queryStrings := []string{
		`(export_statement declaration: (class_declaration name: (type_identifier) @name)) @entity`,
		`(class_declaration name: (type_identifier) @name) @entity`,
		`(export_statement declaration: (function_declaration name: (identifier) @name)) @entity`,
		`(function_declaration name: (identifier) @name) @entity`,
		`(export_statement (interface_declaration (type_identifier) @name)) @entity`,
		`(interface_declaration (type_identifier) @name) @entity`,
		`(method_definition (property_identifier) @name) @entity`,
		`(variable_declarator (identifier) @name value: [(arrow_function) (function_expression)]) @entity`,
		`(lexical_declaration (variable_declarator (identifier) @name value: [(arrow_function) (function_expression)])) @entity`,
		`(export_statement (lexical_declaration (variable_declarator (identifier) @name value: [(arrow_function) (function_expression)]))) @entity`,
		`(export_statement (export_default_declaration value: (function_declaration name: (identifier)? @name)) @entity) @entity`,
		`(export_statement (export_default_declaration value: [(arrow_function) (function_expression) (identifier) @name])) @entity`,
		`(export_statement (export_default_declaration value: (call_expression) @name)) @entity`,
		`(call_expression) @entity`,
		`(expression_statement) @entity`,
	}

	var rawChunks []Chunk
	type chunkPos struct {
		start, end uint32
	}
	var rawPositions []chunkPos

	for _, qs := range queryStrings {
		query, err := gotreesitter.NewQuery(qs, lang)
		if err != nil {
			continue
		}

		cursor := query.Exec(root, lang, []byte(content))
		for {
			match, ok := cursor.NextMatch()
			if !ok {
				break
			}

			for _, capture := range match.Captures {
				if capture.Name == "entity" {
					entityNode := capture.Node
					var name string
					for _, c := range match.Captures {
						if c.Name == "name" && (c.Node.StartByte() >= entityNode.StartByte() && c.Node.EndByte() <= entityNode.EndByte()) {
							name = c.Node.Text([]byte(content))
							break
						}
					}

					var entityType string
					nodeToAnalyze := entityNode

					// Improved node drilling for export statements
					if entityNode.Type(lang) == "export_statement" {
						for _, child := range entityNode.Children() {
							t := child.Type(lang)
							if strings.HasSuffix(t, "_declaration") || t == "lexical_declaration" || t == "method_definition" || t == "variable_declarator" || t == "export_default_declaration" {
								nodeToAnalyze = child
								break
							}
						}
					}

					if nodeToAnalyze.Type(lang) == "export_default_declaration" {
						entityType = "export_default"
						if name == "" {
							// Use filename as name for anonymous defaults (common in Next.js pages)
							base := filepath.Base(filePath)
							name = strings.TrimSuffix(base, filepath.Ext(base))
							if name == "page" || name == "layout" || name == "route" {
								// For Next.js, include parent dir to differentiate
								parent := filepath.Base(filepath.Dir(filePath))
								name = parent + "_" + name
							}
						}
					}

					switch nodeToAnalyze.Type(lang) {
					case "function_declaration":
						entityType = "function"
					case "class_declaration":
						entityType = "class"
					case "method_definition":
						entityType = "method"
					case "interface_declaration":
						entityType = "interface"
					case "type_alias_declaration":
						entityType = "type"
					case "enum_declaration":
						entityType = "enum"
					case "variable_declarator", "lexical_declaration":
						entityType = "variable"
						// Check if it's an arrow function component
						if strings.Contains(entityNode.Text([]byte(content)), "=>") {
							entityType = "component"
						}
					case "expression_statement":
						entityType = "call"
					default:
						if entityType == "" {
							entityType = "entity"
						}
					}

					// Detect React Components (Function name starting with Uppercase)
					if (entityType == "function" || entityType == "export_default") && len(name) > 0 {
						if name[0] >= 'A' && name[0] <= 'Z' {
							entityType = "component"
						}
					}
					chunkContent := entityNode.Text([]byte(content))

					// Capture preceding comments/JSDoc
					var docString string
					prev := entityNode.PrevSibling()
					for prev != nil && (prev.Type(lang) == "comment" || (prev.Type(lang) == " " || strings.TrimSpace(prev.Text([]byte(content))) == "")) {
						if prev.Type(lang) == "comment" {
							docString = prev.Text([]byte(content)) + "\n" + docString
						}
						prev = prev.PrevSibling()
					}

					if docString != "" {
						chunkContent = docString + chunkContent
					}

					calls := extractCalls(entityNode, content, lang)

					// Filter out own name from calls to avoid false usage positives
					var filteredCalls []string
					for _, c := range calls {
						if c != name {
							filteredCalls = append(filteredCalls, c)
						}
					}

					score := calculateScore(chunkContent, entityNode, filteredCalls, content, lang)

					startLine := int(entityNode.StartPoint().Row) + 1
					endLine := int(entityNode.EndPoint().Row) + 1

					// If we added docstring, adjust start line
					if docString != "" {
						startLine -= strings.Count(docString, "\n")
						if startLine < 1 {
							startLine = 1
						}
					}

					rawChunks = append(rawChunks, Chunk{
						Content:       strings.TrimSpace(chunkContent),
						Symbols:       []string{name},
						Type:          entityType,
						Calls:         filteredCalls,
						FunctionScore: score,
						StartLine:     startLine,
						EndLine:       endLine,
					})
					rawPositions = append(rawPositions, chunkPos{entityNode.StartByte(), entityNode.EndByte()})
				}
			}
		}
	}

	// Filter out redundant overlaps (e.g. export_statement containing class_declaration)
	// But keep methods inside classes as they are distinct semantic units.
	var chunks []Chunk
	for i, c1 := range rawChunks {
		isRedundant := false
		for j, c2 := range rawChunks {
			if i == j {
				continue
			}
			// If c1 is exactly the same as c2 (from different match branches)
			if rawPositions[i].start == rawPositions[j].start && rawPositions[i].end == rawPositions[j].end {
				if i > j { // Keep the first one
					isRedundant = true
					break
				}
				continue
			}

			// If c1 is inside c2
			if rawPositions[i].start >= rawPositions[j].start && rawPositions[i].end <= rawPositions[j].end {
				// Redundant if c2 is just an export wrapper for c1
				if c2.Type == "export" && c1.Type != "method" && c1.Type != "function" && c1.Type != "class" {
					isRedundant = true
					break
				}
				// Redundant if they are same type and c2 is larger
				if c1.Type == c2.Type && (rawPositions[i].end-rawPositions[i].start) < (rawPositions[j].end-rawPositions[j].start) {
					isRedundant = true
					break
				}
			}
		}
		if !isRedundant {
			chunks = append(chunks, c1)
		}
	}

	if len(chunks) == 0 {
		return chunkJavaScriptTypeScript(content, filePath)
	}

	return chunks
}

func extractCalls(node *gotreesitter.Node, content string, lang *gotreesitter.Language) []string {
	callQueryString := `
		(identifier) @call_name
		(property_identifier) @call_name
	`
	query, err := gotreesitter.NewQuery(callQueryString, lang)
	if err != nil {
		return nil
	}

	matches := query.ExecuteNode(node, lang, []byte(content))

	uniqueCalls := make(map[string]bool)
	for _, match := range matches {
		for _, capture := range match.Captures {
			if capture.Name == "call_name" {
				callName := capture.Node.Text([]byte(content))
				uniqueCalls[callName] = true
			}
		}
	}

	// Filter out the symbols of the chunk itself from its calls
	// (Simplified: we'll just return all found identifiers for now to see if it works)
	var calls []string
	for c := range uniqueCalls {
		calls = append(calls, c)
	}
	sort.Strings(calls)
	return calls
}

func calculateScore(chunkContent string, node *gotreesitter.Node, calls []string, fullContent string, lang *gotreesitter.Language) float32 {
	score := float32(1.0)

	// Length penalty/bonus
	lines := strings.Count(chunkContent, "\n") + 1
	if lines < 3 {
		score -= 0.3 // Boilerplate penalty
	} else if lines > 10 {
		score += 0.2
	}

	// Structural weight based on calls
	score += float32(len(calls)) * 0.1

	// Export boost
	parent := node.Parent()
	if parent != nil && (parent.Type(lang) == "export_statement" || strings.Contains(fullContent[max(0, int(node.StartByte())-20):node.StartByte()], "export")) {
		score += 1.0
	}

	return score
}

func astChunkGo(text string) []Chunk {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", text, parser.ParseComments)
	if err != nil {
		return semanticRegexChunk(text)
	}

	var chunks []Chunk

	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			start := fset.Position(x.Pos()).Offset
			if x.Doc != nil {
				start = fset.Position(x.Doc.Pos()).Offset
			}
			end := fset.Position(x.End()).Offset

			symbolName := x.Name.Name
			if x.Recv != nil && len(x.Recv.List) > 0 {
				recvType := ""
				switch t := x.Recv.List[0].Type.(type) {
				case *ast.Ident:
					recvType = t.Name
				case *ast.StarExpr:
					if id, ok := t.X.(*ast.Ident); ok {
						recvType = id.Name
					}
				}
				if recvType != "" {
					symbolName = fmt.Sprintf("%s.%s", recvType, symbolName)
				}
			}

			if start >= 0 && end <= len(text) && start < end {
				calls := extractCallsGo(x)
				chunks = append(chunks, Chunk{
					Content:   text[start:end],
					Symbols:   []string{symbolName},
					Type:      "function",
					Calls:     calls,
					StartLine: fset.Position(x.Pos()).Line,
					EndLine:   fset.Position(x.End()).Line,
				})
			}
			return false

		case *ast.GenDecl:
			if x.Tok == token.TYPE {
				start := fset.Position(x.Pos()).Offset
				if x.Doc != nil {
					start = fset.Position(x.Doc.Pos()).Offset
				}
				end := fset.Position(x.End()).Offset

				var symbols []string
				for _, spec := range x.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						symbols = append(symbols, ts.Name.Name)
					}
				}

				if start >= 0 && end <= len(text) && start < end {
					chunks = append(chunks, Chunk{
						Content:   text[start:end],
						Symbols:   symbols,
						Type:      "type",
						StartLine: fset.Position(x.Pos()).Line,
						EndLine:   fset.Position(x.End()).Line,
					})
				}
				return false
			}
		}
		return true
	})

	if len(chunks) == 0 {
		return semanticRegexChunk(text)
	}

	return chunks
}

func extractCallsGo(node ast.Node) []string {
	uniqueCalls := make(map[string]bool)
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			// Handle simple function calls
			if id, ok := x.Fun.(*ast.Ident); ok {
				uniqueCalls[id.Name] = true
			}
			// Handle method calls (e.g., s.handleSomething)
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				uniqueCalls[sel.Sel.Name] = true
				// Also capture the full qualified name if it matches an exported pattern
				if id, ok := sel.X.(*ast.Ident); ok {
					uniqueCalls[fmt.Sprintf("%s.%s", id.Name, sel.Sel.Name)] = true
				}
			}
		case *ast.Ident:
			// Capture identifiers being passed as arguments or assigned
			uniqueCalls[x.Name] = true
		case *ast.SelectorExpr:
			// Capture selected identifiers (e.g., server.handleSomething)
			uniqueCalls[x.Sel.Name] = true
			if id, ok := x.X.(*ast.Ident); ok {
				uniqueCalls[fmt.Sprintf("%s.%s", id.Name, x.Sel.Name)] = true
			}
		}
		return true
	})

	var calls []string
	for c := range uniqueCalls {
		calls = append(calls, c)
	}
	sort.Strings(calls)
	return calls
}

func chunkJavaScriptTypeScript(content string, filePath string) []Chunk {
	// Pattern to match functions, classes, and arrow functions
	semanticPattern := regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?(?:function\s+([a-zA-Z0-9_]+)|class\s+([a-zA-Z0-9_]+)|(?:const|let|var)\s+([a-zA-Z0-9_]+)\s*=\s*(?:async\s*)?\(.*?\)\s*=>)`)

	indices := semanticPattern.FindAllStringSubmatchIndex(content, -1)
	if len(indices) == 0 {
		return fastChunk(content)
	}

	var chunks []Chunk
	for i := 0; i < len(indices); i++ {
		start := indices[i][0]
		end := len(content)
		if i+1 < len(indices) {
			end = indices[i+1][0]
		}

		// Extract symbol name from submatches
		symbolName := ""
		match := semanticPattern.FindStringSubmatch(content[indices[i][0]:indices[i][1]])
		for j := 1; j < len(match); j++ {
			if match[j] != "" {
				symbolName = match[j]
				break
			}
		}

		chunks = append(chunks, Chunk{
			Content:   strings.TrimSpace(content[start:end]),
			Symbols:   []string{symbolName},
			StartLine: strings.Count(content[:start], "\n") + 1,
			EndLine:   strings.Count(content[:end], "\n") + 1,
		})
	}

	return chunks
}

func parseRelationships(text string, ext string) []string {
	var relations []string
	if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
		// Match 'from "..."' or 'import "..."' or 'require("...")'
		importRegex := regexp.MustCompile(`(?:import|from|require)\s*\(?\s*['"]([^'"]+)['"]`)
		matches := importRegex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				relations = append(relations, m[1])
			}
		}

		// Also extract named imports: import { A, B } from ...
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
		// Improved Go import parsing
		// Match single line: import "..." or import alias "..."
		singleImportRegex := regexp.MustCompile(`import\s+(?:[a-zA-Z0-9_.]+\s+)?["']([^"']+)["']`)
		matches := singleImportRegex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				relations = append(relations, m[1])
			}
		}

		// Match import blocks: import (...)
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
	}

	// De-duplicate relationships
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

func semanticRegexChunk(text string) []Chunk {
	lines := strings.Split(text, "\n")
	var chunks []Chunk
	var currentChunk strings.Builder
	var currentSymbols []string
	startLine := 1

	exportRegex := regexp.MustCompile(`(?:export\s+)?(?:async\s+)?(?:function|class|interface|const|let|type|enum|def|func)\s+([a-zA-Z0-9_]+)`)

	for i, line := range lines {
		match := exportRegex.FindStringSubmatch(line)
		if (len(match) > 1 && currentChunk.Len() > 3000) || currentChunk.Len() > 8000 {
			chunks = append(chunks, Chunk{
				Content:   strings.TrimSpace(currentChunk.String()),
				Symbols:   append([]string{}, currentSymbols...),
				StartLine: startLine,
				EndLine:   i,
			})
			currentChunk.Reset()
			currentSymbols = nil
			startLine = i + 1
		}

		if len(match) > 1 {
			currentSymbols = append(currentSymbols, match[1])
		}
		currentChunk.WriteString(line)
		currentChunk.WriteString("\n")
	}

	if currentChunk.Len() > 0 {
		chunks = append(chunks, Chunk{
			Content:   strings.TrimSpace(currentChunk.String()),
			Symbols:   currentSymbols,
			StartLine: startLine,
			EndLine:   len(lines),
		})
	}

	return chunks
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
			Content:   content,
			StartLine: startLine,
			EndLine:   endLine,
		})

		if end == len(runes) {
			break
		}

		// Advance by (chunkSize - overlap)
		i += (chunkSize - overlap)
	}
	return chunks
}
