package embedding

import "strings"

// ModelAdapter provides a way to preprocess text before tokenization
// based on model-specific requirements (e.g., prefixing).
type ModelAdapter interface {
	Preprocess(text string, isQuery bool) string
}

// NomicAdapter implements the ModelAdapter interface for Nomic embedding models.
// It prepends 'search_document: ' for documents and 'search_query: ' for queries.
type NomicAdapter struct{}

func (a *NomicAdapter) Preprocess(text string, isQuery bool) string {
	if isQuery {
		if strings.HasPrefix(text, "search_query: ") {
			return text
		}
		return "search_query: " + text
	}
	if strings.HasPrefix(text, "search_document: ") {
		return text
	}
	return "search_document: " + text
}

// GetAdapter returns a ModelAdapter for the given model name, if one exists.
func GetAdapter(modelName string) ModelAdapter {
	if strings.Contains(strings.ToLower(modelName), "nomic") {
		return &NomicAdapter{}
	}
	return nil
}
