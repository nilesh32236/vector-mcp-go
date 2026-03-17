package indexer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

type Chunk struct {
	Content       string
	Symbols       []string
	Relationships []string
}

func CreateChunks(text string, filePath string) []Chunk {
	ext := filepath.Ext(filePath)
	relationships := parseRelationships(text, ext)
	var chunks []Chunk

	if ext == ".go" {
		chunks = astChunkGo(text)
	} else if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
		chunks = chunkJavaScriptTypeScript(text, filePath)
	} else {
		chunks = fastChunk(text)
	}

	// Inject Context Headers and attach relationships
	for i := range chunks {
		scope := "Global"
		if len(chunks[i].Symbols) > 0 {
			scope = chunks[i].Symbols[0]
		}
		
		header := fmt.Sprintf("// File: %s\n// Scope/Entity: %s\n\n", filePath, scope)
		chunks[i].Content = header + chunks[i].Content
		chunks[i].Relationships = relationships
	}
	return chunks
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
				chunks = append(chunks, Chunk{
					Content: text[start:end],
					Symbols: []string{symbolName},
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
						Content: text[start:end],
						Symbols: symbols,
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
			Content: strings.TrimSpace(content[start:end]),
			Symbols: []string{symbolName},
		})
	}

	return chunks
}

func parseRelationships(text string, ext string) []string {
	var relations []string
	if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
		importRegex := regexp.MustCompile(`import\s+.*\s+from\s+['"](.*)['"]`)
		matches := importRegex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				relations = append(relations, m[1])
			}
		}
	} else if ext == ".go" {
		importRegex := regexp.MustCompile(`"([^"]+)"`)
		start := strings.Index(text, "import (")
		if start != -1 {
			end := strings.Index(text[start:], ")")
			if end != -1 {
				block := text[start : start+end]
				matches := importRegex.FindAllStringSubmatch(block, -1)
				for _, m := range matches {
					relations = append(relations, m[1])
				}
			}
		} else {
			singleImportRegex := regexp.MustCompile(`import\s+"([^"]+)"`)
			matches := singleImportRegex.FindAllStringSubmatch(text, -1)
			for _, m := range matches {
				if len(m) > 1 {
					relations = append(relations, m[1])
				}
			}
		}
	}
	return relations
}

func semanticRegexChunk(text string) []Chunk {
	lines := strings.Split(text, "\n")
	var chunks []Chunk
	var currentChunk strings.Builder
	var currentSymbols []string

	exportRegex := regexp.MustCompile(`(?:export\s+)?(?:async\s+)?(?:function|class|interface|const|let|type|enum|def|func)\s+([a-zA-Z0-9_]+)`)

	for _, line := range lines {
		match := exportRegex.FindStringSubmatch(line)
		if (len(match) > 1 && currentChunk.Len() > 3000) || currentChunk.Len() > 8000 {
			chunks = append(chunks, Chunk{
				Content: strings.TrimSpace(currentChunk.String()),
				Symbols: append([]string{}, currentSymbols...),
			})
			currentChunk.Reset()
			currentSymbols = nil
		}

		if len(match) > 1 {
			currentSymbols = append(currentSymbols, match[1])
		}
		currentChunk.WriteString(line)
		currentChunk.WriteString("\n")
	}

	if currentChunk.Len() > 0 {
		chunks = append(chunks, Chunk{
			Content: strings.TrimSpace(currentChunk.String()),
			Symbols: currentSymbols,
		})
	}

	return chunks
}

func fastChunk(text string) []Chunk {
	var chunks []Chunk
	runes := []rune(text)
	for i := 0; i < len(runes); i += 3000 {
		end := i + 4000
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, Chunk{
			Content: string(runes[i:end]),
		})
		if end == len(runes) {
			break
		}
	}
	return chunks
}
