package indexer

import (
	"regexp"
	"strings"
)

type Chunk struct {
	Content string
	Symbols []string
}

func CreateChunks(text string, ext string) []Chunk {
	if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
		return semanticChunk(text)
	}
	return fastChunk(text)
}

func semanticChunk(text string) []Chunk {
	lines := strings.Split(text, "\n")
	var chunks []Chunk
	var currentChunk strings.Builder
	var currentSymbols []string

	// Basic semantic regex for exports
	exportRegex := regexp.MustCompile(`export\s+(?:async\s+)?(?:function|class|interface|const|let|type|enum)\s+([a-zA-Z0-9_]+)`)

	for _, line := range lines {
		// BGE-M3 supports 8k context, we'll aim for ~4k char chunks
		match := exportRegex.FindStringSubmatch(line)
		if len(match) > 1 && currentChunk.Len() > 3000 {
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
	// Simple sliding window for non-code files
	for i := 0; i < len(text); i += 3500 {
		end := i + 4000
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, Chunk{
			Content: text[i:end],
			Symbols: nil,
		})
	}
	return chunks
}
