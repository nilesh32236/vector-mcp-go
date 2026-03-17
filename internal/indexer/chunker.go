package indexer

import (
	"regexp"
	"strings"
)

type Chunk struct {
	Content      string
	Symbols      []string
	Relationships []string
}

func CreateChunks(text string, ext string) []Chunk {
	relationships := parseRelationships(text, ext)
	var chunks []Chunk
	if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
		chunks = semanticChunk(text)
	} else {
		chunks = fastChunk(text)
	}

	// Attach relationships to each chunk
	for i := range chunks {
		chunks[i].Relationships = relationships
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
		// Multi-line and single-line Go imports
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
			// Single line import "path"
			singleImportRegex := regexp.MustCompile(`import\s+"([^"]+)"`)
			matches := singleImportRegex.FindAllStringSubmatch(text, -1)
			for _, m := range matches {
				if len(m) > 1 {
					relations = append(relations, m[1])
				}
			}
		}
	} else if ext == ".prisma" {
		// Prisma model relations
		relationRegex := regexp.MustCompile(`@relation\(fields:\s*\[([^\]]+)\]`)
		matches := relationRegex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				relations = append(relations, m[1])
			}
		}
	} else if ext == ".css" {
		importRegex := regexp.MustCompile(`@import\s+['"](.*)['"]`)
		matches := importRegex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) > 1 {
				relations = append(relations, m[1])
			}
		}
	}
	return relations
}

func semanticChunk(text string) []Chunk {
	lines := strings.Split(text, "\n")
	var chunks []Chunk
	var currentChunk strings.Builder
	var currentSymbols []string

	// Expanded semantic regex for multiple languages
	// TS/JS: export function/class/const
	// Go: func (r *Recv) Name() or func Name()
	// Python: def name() or class Name:
	exportRegex := regexp.MustCompile(`(?:export\s+)?(?:async\s+)?(?:function|class|interface|const|let|type|enum|def|func)\s+([a-zA-Z0-9_]+)`)

	for _, line := range lines {
		// BGE-M3 supports 8k context, we'll aim for ~4k char chunks
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
	// Simple sliding window for non-code files, using runes for UTF-8 safety
	for i := 0; i < len(runes); i += 3500 {
		end := i + 4000
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, Chunk{
			Content: string(runes[i:end]),
			Symbols: nil,
		})
	}
	return chunks
}
