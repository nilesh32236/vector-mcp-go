// Package lexical provides a BM25 inverted index for fast lexical search,
// replacing the O(n) full-scan in db.Store.LexicalSearch.
package lexical

import (
	"math"
	"strings"
	"sync"
	"unicode"
)

const (
	bm25K1               = 1.2
	bm25B                = 0.75
	BM25Offset           = 0.5
	TokenExpansionFactor = 2
	DefaultFragments     = 4
)

// Posting holds a document ID and its term frequency.
type Posting struct {
	DocID string
	TF    int
}

// Index is a thread-safe BM25 inverted index.
type Index struct {
	mu       sync.RWMutex
	postings map[string][]Posting // term → postings list
	docLen   map[string]int       // docID → token count
	docCount int
	avgLen   float64
}

// NewIndex creates an empty BM25 index.
func NewIndex() *Index {
	return &Index{
		postings: make(map[string][]Posting),
		docLen:   make(map[string]int),
	}
}

// Add indexes a document. Call this whenever a record is inserted.
func (idx *Index) Add(docID, text string) {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return
	}

	// Count term frequencies
	tf := make(map[string]int, len(tokens))
	for _, t := range tokens {
		tf[t]++
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Remove old entry if re-indexing same doc
	idx.removeLocked(docID)

	idx.docLen[docID] = len(tokens)
	idx.docCount++

	// Recompute average length incrementally
	totalLen := idx.avgLen * float64(idx.docCount-1)
	idx.avgLen = (totalLen + float64(len(tokens))) / float64(idx.docCount)

	for term, count := range tf {
		idx.postings[term] = append(idx.postings[term], Posting{DocID: docID, TF: count})
	}
}

// Remove deletes a document from the index.
func (idx *Index) Remove(docID string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.removeLocked(docID)
}

func (idx *Index) removeLocked(docID string) {
	if _, exists := idx.docLen[docID]; !exists {
		return
	}
	oldLen := idx.docLen[docID]
	delete(idx.docLen, docID)
	idx.docCount--

	if idx.docCount > 0 {
		totalLen := idx.avgLen*float64(idx.docCount+1) - float64(oldLen)
		idx.avgLen = totalLen / float64(idx.docCount)
	} else {
		idx.avgLen = 0
	}

	for term, posts := range idx.postings {
		filtered := posts[:0]
		for _, p := range posts {
			if p.DocID != docID {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) == 0 {
			delete(idx.postings, term)
		} else {
			idx.postings[term] = filtered
		}
	}
}

// Result is a scored search result.
type Result struct {
	DocID string
	Score float64
}

// Search returns documents ranked by BM25 score for the given query.
func (idx *Index) Search(query string, topK int) []Result {
	if topK <= 0 {
		return nil
	}

	terms := tokenize(query)
	if len(terms) == 0 {
		return nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.docCount == 0 {
		return nil
	}

	scores := make(map[string]float64)
	N := float64(idx.docCount)
	avgdl := idx.avgLen

	for _, term := range terms {
		posts, ok := idx.postings[term]
		if !ok {
			continue
		}
		// IDF component
		df := float64(len(posts))
		idf := math.Log((N-df+BM25Offset)/(df+BM25Offset) + 1)

		for _, p := range posts {
			dl := float64(idx.docLen[p.DocID])
			tf := float64(p.TF)
			// BM25 TF component
			tfNorm := tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*dl/avgdl))
			scores[p.DocID] += idf * tfNorm
		}
	}

	// Collect and sort
	results := make([]Result, 0, len(scores))
	for id, score := range scores {
		results = append(results, Result{DocID: id, Score: score})
	}

	// Partial sort: only need topK
	if topK > len(results) {
		topK = len(results)
	}
	partialSort(results, topK)
	return results[:topK]
}

// Size returns the number of indexed documents.
func (idx *Index) Size() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.docCount
}

// tokenize lowercases and splits text into tokens, filtering punctuation.
func tokenize(text string) []string {
	rawTokens := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})

	tokens := make([]string, 0, len(rawTokens)*TokenExpansionFactor)
	for _, token := range rawTokens {
		if token == "" {
			continue
		}

		lowerToken := strings.ToLower(token)
		tokens = append(tokens, lowerToken)
		for _, fragment := range splitIdentifier(token) {
			lowerFragment := strings.ToLower(fragment)
			if lowerFragment != "" && lowerFragment != lowerToken {
				tokens = append(tokens, lowerFragment)
			}
		}
	}
	return tokens
}

// partialSort does a selection-based partial sort to get top-k without full sort.
func partialSort(results []Result, k int) {
	for i := 0; i < k; i++ {
		maxIdx := i
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[maxIdx].Score {
				maxIdx = j
			}
		}
		results[i], results[maxIdx] = results[maxIdx], results[i]
	}
}

func splitIdentifier(token string) []string {
	runes := []rune(token)
	if len(runes) == 0 {
		return nil
	}

	fragments := make([]string, 0, DefaultFragments)
	start := 0
	flush := func(end int) {
		if end <= start {
			return
		}
		fragment := strings.Trim(string(runes[start:end]), "_")
		if fragment != "" {
			fragments = append(fragments, fragment)
		}
	}

	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		curr := runes[i]

		if curr == '_' {
			flush(i)
			start = i + 1
			continue
		}

		if prev == '_' {
			start = i
			continue
		}

		if unicode.IsLower(prev) && unicode.IsUpper(curr) {
			flush(i)
			start = i
			continue
		}

		if unicode.IsDigit(prev) != unicode.IsDigit(curr) {
			flush(i)
			start = i
			continue
		}

		if unicode.IsUpper(prev) && unicode.IsUpper(curr) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
			flush(i)
			start = i
		}
	}

	flush(len(runes))
	return fragments
}
