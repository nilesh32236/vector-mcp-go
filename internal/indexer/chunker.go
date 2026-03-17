package indexer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
)

type Chunk struct {
	Content       string
	Symbols       []string
	Relationships []string
}

func CreateChunks(text string, ext string) []Chunk {
	relationships := parseRelationships(text, ext)
	var chunks []Chunk

	if ext == ".go" {
		chunks = astChunkGo(text)
	} else if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
		chunks = semanticRegexChunk(text)
	} else {
		chunks = fastChunk(text)
	}

	// Attach relationships to each chunk
	for i := range chunks {
		chunks[i].Relationships = relationships
	}
	return chunks
}

func astChunkGo(text string) []Chunk {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", text, parser.ParseComments)
	if err != nil {
		// Fallback to regex if parsing fails (e.g. incomplete code)
		return semanticRegexChunk(text)
	}

	var chunks []Chunk

	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			// Extract Functions and Methods
			start := fset.Position(x.Pos()).Offset
			if x.Doc != nil {
				start = fset.Position(x.Doc.Pos()).Offset
			}
			end := fset.Position(x.End()).Offset
			
			symbolName := x.Name.Name
			if x.Recv != nil && len(x.Recv.List) > 0 {
				// Include receiver type for methods
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
			return false // Don't recurse into function bodies

		case *ast.GenDecl:
			// Extract Types (Structs, Interfaces, etc.)
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

	// If AST didn't find anything (e.g. only global variables), fallback to regex
	if len(chunks) == 0 {
		return semanticRegexChunk(text)
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
