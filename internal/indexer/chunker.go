package indexer

import (
	"regexp"
	"strings"
)

type Chunk struct {
	Content       string
	Symbols       []string
	Relationships []string
}

func CreateChunks(text string, ext string) []Chunk {
	// Fallback to regex-based chunking due to dependency conflicts with tree-sitter grammars in Go 1.21+
	return semanticRegexChunk(text)
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
