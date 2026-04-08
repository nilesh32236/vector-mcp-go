// Package db provides data persistence and graph representation for code entities.
package db

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/db/lexical"
	"github.com/nilesh32236/vector-mcp-go/internal/observability/metrics"
	"github.com/nilesh32236/vector-mcp-go/internal/observability/tracing"
	"github.com/philippgille/chromem-go"
)

// Store manages the vector and lexical search database.
const (
	FetchMultiplier = 3
	ExpandedFetchMultiplier = 2
	WaitGroupCount = 2
	SecondsPerDay = 24 * 60 * 60
	RecencyBoostFactor = 0.5
	RecencyDecayDays = 14.0
	MaxParsedCacheSize = 10000
)

type Store struct {
	db          *chromem.DB
	collection  *chromem.Collection
	Dimension   int
	parsedCache map[string][]string
	cacheMu     sync.RWMutex
	bm25        *lexical.Index // BM25 inverted index for O(log n) lexical search
}

// Record represents a single entry in the database.
type Record struct {
	ID         string            `json:"id"`
	Content    string            `json:"content"`
	Embedding  []float32         `json:"embedding"`
	Metadata   map[string]string `json:"metadata"`
	Similarity float32           `json:"similarity,omitempty"`
}

// Connect establishes a connection to the persistent database and ensures the collection exists.
func Connect(ctx context.Context, dbPath string, collectionName string, dimension int) (*Store, error) {
	db, err := chromem.NewPersistentDB(dbPath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create persistent DB: %w", err)
	}

	col := db.GetCollection(collectionName, nil)
	if col == nil {
		col, err = db.CreateCollection(collectionName, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create collection: %w", err)
		}
	}

	s := &Store{db: db, collection: col, Dimension: dimension, parsedCache: make(map[string][]string), bm25: lexical.NewIndex()}

	// Probe for dimension mismatch if the collection already has data.
	if col.Count() > 0 {
		probe := make([]float32, dimension)
		probe[0] = 1.0 // non-zero so it's a valid normalized-ish vector
		_, err := col.QueryEmbedding(ctx, probe, 1, nil, nil)
		if err != nil && strings.Contains(err.Error(), "vectors must have the same length") {
			return nil, fmt.Errorf(
				"dimension mismatch detected: you have switched embedding models. "+
					"Please delete your existing vector database at %q and restart", dbPath)
		}
	}

	if err := s.rebuildLexicalIndex(ctx); err != nil {
		return nil, fmt.Errorf("failed to bootstrap lexical index: %w", err)
	}

	return s, nil
}

func (s *Store) rebuildLexicalIndex(ctx context.Context) error {
	count := s.collection.Count()
	if count == 0 {
		return nil
	}

	dummyEmb := make([]float32, s.Dimension)
	results, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, nil, nil)
	if err != nil {
		return err
	}

	index := lexical.NewIndex()
	for _, doc := range results {
		index.Add(doc.ID, s.lexicalDocumentText(doc.Content, doc.Metadata))
	}
	s.bm25 = index
	return nil
}

func (s *Store) deleteByFilter(ctx context.Context, filter map[string]string) error {
	count := s.collection.Count()
	if count == 0 {
		return nil
	}

	dummyEmb := make([]float32, s.Dimension)
	results, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, filter, nil)
	if err != nil {
		return err
	}

	for _, doc := range results {
		s.bm25.Remove(doc.ID)
	}

	return s.collection.Delete(ctx, filter, nil)
}

func (s *Store) lexicalDocumentText(content string, metadata map[string]string) string {
	var builder strings.Builder
	appendField := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(value)
	}

	appendField(content)
	appendField(metadata["name"])
	appendField(metadata["path"])

	for _, key := range []string{"symbols", "calls"} {
		if raw := metadata[key]; raw != "" {
			if parsed := s.parseStringArray(raw); len(parsed) > 0 {
				for _, value := range parsed {
					appendField(value)
				}
				continue
			}
			appendField(raw)
		}
	}

	return builder.String()
}

// Insert adds a batch of records to the database and updates the lexical index.
func (s *Store) Insert(ctx context.Context, records []Record) error {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.insert")
	defer span.End()

	var docs []chromem.Document
	for _, r := range records {
		docs = append(docs, chromem.Document{
			ID:        r.ID,
			Content:   r.Content,
			Embedding: r.Embedding,
			Metadata:  r.Metadata,
		})
		s.bm25.Add(r.ID, s.lexicalDocumentText(r.Content, r.Metadata))
	}

	return s.collection.AddDocuments(ctx, docs, runtime.NumCPU())
}

// Search performs a vector similarity search.
func (s *Store) Search(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]Record, error) {
	if topK <= 0 {
		return nil, nil
	}

	records, _, err := s.SearchWithScore(ctx, queryEmbedding, topK, projectIDs, category)
	return records, err
}

// LexicalSearch performs a keyword-based search using the BM25 index.
func (s *Store) LexicalSearch(ctx context.Context, query string, topK int, projectIDs []string, category string) ([]Record, error) {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.lexical_search")
	defer span.End()

	if topK <= 0 {
		return nil, nil
	}

	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}

	fetchK := count
	if topK <= count/3 {
		fetchK = topK * FetchMultiplier
	}

	// Fast BM25 path: fetch extras up front so project/category filtering does not
	// starve the final result set.
	bm25Results := s.bm25.Search(query, fetchK)
	if len(bm25Results) == 0 {
		return nil, nil
	}

	// Build a set of matching IDs for fast lookup
	idSet := make(map[string]float64, len(bm25Results))
	for _, r := range bm25Results {
		idSet[r.DocID] = r.Score
	}

	// Fetch the actual records from chromem by ID via a dummy embedding query
	// We use a targeted fetch: query with a filter that matches our IDs.
	// Since chromem doesn't support IN queries, we fetch all and filter by ID.
	dummyEmb := make([]float32, s.Dimension)
	var where map[string]string
	if category != "" {
		where = map[string]string{"category": category}
	}

	// For project filtering, iterate per project
	var allResults []chromem.Result
	if len(projectIDs) == 0 {
		res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, where, nil)
		if err != nil {
			return nil, err
		}
		allResults = res
	} else {
		for _, pid := range projectIDs {
			w := map[string]string{"project_id": pid}
			if category != "" {
				w["category"] = category
			}
			res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, w, nil)
			if err != nil {
				return nil, err
			}
			allResults = append(allResults, res...)
		}
	}

	type scored struct {
		rec   Record
		score float64
	}
	var matches []scored
	for _, doc := range allResults {
		if score, ok := idSet[doc.ID]; ok {
			matches = append(matches, scored{
				rec: Record{
					ID:       doc.ID,
					Content:  doc.Content,
					Metadata: doc.Metadata,
				},
				score: score,
			})
		}
	}

	// Sort by BM25 score descending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	if len(matches) > topK {
		matches = matches[:topK]
	}

	records := make([]Record, len(matches))
	for i, m := range matches {
		records[i] = m.rec
	}
	return records, nil
}

// HybridSearch combines vector and lexical search results using Reciprocal Rank Fusion (RRF).
func (s *Store) HybridSearch(ctx context.Context, query string, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]Record, error) {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.hybrid_search")
	defer span.End()

	if topK <= 0 {
		return nil, nil
	}

	searchTimer := metrics.NewTimer(metrics.SearchDuration)
	defer searchTimer.ObserveDuration()

	var (
		vectorResults  []Record
		lexicalResults []Record
		vectorErr      error
		lexicalErr     error
	)
	expandedTopK := topK
	if topK <= int(^uint(0)>>1)/2 {
		expandedTopK = topK * ExpandedFetchMultiplier
	}

	// 1. & 2. Concurrent Vector and Lexical Search (Fetch more for better RRF)
	var wg sync.WaitGroup
	wg.Add(WaitGroupCount)

	go func() {
		defer wg.Done()
		vectorResults, vectorErr = s.Search(ctx, queryEmbedding, expandedTopK, projectIDs, category)
	}()

	go func() {
		defer wg.Done()
		lexicalResults, lexicalErr = s.LexicalSearch(ctx, query, expandedTopK, projectIDs, category)
	}()

	wg.Wait()

	if vectorErr != nil {
		return nil, fmt.Errorf("vector search failed: %w", vectorErr)
	}
	if lexicalErr != nil {
		return nil, fmt.Errorf("lexical search failed: %w", lexicalErr)
	}

	// 3. Reciprocal Rank Fusion (RRF) with Dynamic Weighting
	k := 60.0
	lexicalWeight := 1.0
	vectorWeight := 1.0

	// Heuristic: If query contains code-like identifiers, boost lexical matches
	hasIdentifier := hasCodeLikeIdentifiers(query)
	if hasIdentifier {
		lexicalWeight = 1.5 // 50% boost for lexical when symbols are involved
	}

	scores := make(map[string]float64)
	recordMap := make(map[string]Record)

	for i, r := range vectorResults {
		scores[r.ID] += vectorWeight * (1.0 / (k + float64(i+1)))
		recordMap[r.ID] = r
	}

	for i, r := range lexicalResults {
		scores[r.ID] += lexicalWeight * (1.0 / (k + float64(i+1)))
		recordMap[r.ID] = r
	}

	// 4. Boost & Sort
	type ScoredRecord struct {
		Record Record
		Score  float64
	}
	var ranked []ScoredRecord
	for id, score := range scores {
		r := recordMap[id]

		// Boost by FunctionScore
		boost := 1.0
		if fsStr, ok := r.Metadata["function_score"]; ok {
			if fs, err := strconv.ParseFloat(fsStr, 32); err == nil {
				boost = float64(fs)
			}
		}

		// Apply recency boost for document category
		if cat, ok := r.Metadata["category"]; ok && cat == "document" {
			if updatedAtStr, ok := r.Metadata["updated_at"]; ok {
				if updatedAt, err := strconv.ParseInt(updatedAtStr, 10, 64); err == nil {
					now := time.Now().Unix()
					ageDays := float64(now-updatedAt) / SecondsPerDay
					if ageDays < 0 {
						ageDays = 0
					}
					// Half-life of 14 days, max boost of 1.5x
					// Formula: 1.0 + 0.5 * 2^(-age/14)
					recencyBoost := 1.0 + RecencyBoostFactor*math.Pow(2, -ageDays/RecencyDecayDays)
					boost *= recencyBoost
				}
			}
		}

		// Apply priority boost
		if pStr, ok := r.Metadata["priority"]; ok {
			if p, err := strconv.ParseFloat(pStr, 32); err == nil {
				boost *= p
			}
		}

		ranked = append(ranked, ScoredRecord{
			Record: r,
			Score:  score * boost,
		})
	}

	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Score > ranked[j].Score
	})

	// 5. Select topK
	var finalResults []Record
	for i := 0; i < len(ranked) && i < topK; i++ {
		finalResults = append(finalResults, ranked[i].Record)
	}

	return finalResults, nil
}

// SearchWithScore performs a semantic search using vector embeddings and returns records along with their cosine similarity scores.
func (s *Store) SearchWithScore(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]Record, []float32, error) {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.vector_search")
	defer span.End()

	if topK <= 0 {
		return nil, nil, nil
	}

	count := s.collection.Count()
	if count == 0 {
		return nil, nil, nil
	}

	if topK > count {
		topK = count
	}

	var allResults []chromem.Result
	if len(projectIDs) == 0 {
		var where map[string]string
		if category != "" {
			where = map[string]string{"category": category}
		}
		res, err := s.collection.QueryEmbedding(ctx, queryEmbedding, topK, where, nil)
		if err != nil {
			return nil, nil, err
		}
		allResults = res
	} else {
		// Perform individual queries for each project to avoid truncation issues
		for _, pid := range projectIDs {
			where := make(map[string]string)
			where["project_id"] = pid
			if category != "" {
				where["category"] = category
			}
			res, err := s.collection.QueryEmbedding(ctx, queryEmbedding, topK, where, nil)
			if err != nil {
				return nil, nil, err
			}
			allResults = append(allResults, res...)
		}
		// Sort by similarity descending
		sort.Slice(allResults, func(i, j int) bool {
			return allResults[i].Similarity > allResults[j].Similarity
		})
		// Slicing topK
		if len(allResults) > topK {
			allResults = allResults[:topK]
		}
	}

	var records []Record
	var scores []float32
	for _, doc := range allResults {
		boost := float32(1.0)
		if pStr, ok := doc.Metadata["priority"]; ok {
			if p, err := strconv.ParseFloat(pStr, 32); err == nil {
				boost = float32(p)
			}
		}

		records = append(records, Record{
			ID:         doc.ID,
			Content:    doc.Content,
			Embedding:  doc.Embedding,
			Metadata:   doc.Metadata,
			Similarity: doc.Similarity * boost,
		})
		scores = append(scores, doc.Similarity*boost)
	}

	// Re-sort if boosts changed the orders
	sort.Slice(records, func(i, j int) bool {
		return records[i].Similarity > records[j].Similarity
	})

	return records, scores, nil
}

// DeleteByPath removes all records associated with a specific file path.
func (s *Store) DeleteByPath(ctx context.Context, path string, projectID string) error {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.delete_by_path")
	defer span.End()

	return s.deleteByFilter(ctx, map[string]string{"path": path, "project_id": projectID})
}

// DeleteByPrefix deletes all records where the metadata 'path' starts with the given prefix.
// This is critical for handling directory removals/renames correctly.
func (s *Store) DeleteByPrefix(ctx context.Context, prefix string, projectID string) error {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.delete_by_prefix")
	defer span.End()

	count := s.collection.Count()
	if count == 0 {
		return nil
	}

	dummyEmb := make([]float32, s.Dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, map[string]string{"project_id": projectID}, nil)
	if err != nil {
		return err
	}

	for _, doc := range res {
		path := doc.Metadata["path"]
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			s.bm25.Remove(doc.ID)
			if err := s.collection.Delete(ctx, map[string]string{"path": path, "project_id": projectID}, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// ClearProject removes all records associated with a specific project.
func (s *Store) ClearProject(ctx context.Context, projectID string) error {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.clear_project")
	defer span.End()

	return s.deleteByFilter(ctx, map[string]string{"project_id": projectID})
}

// GetPathHashMapping returns a map of file paths to their content hashes for a project.
func (s *Store) GetPathHashMapping(ctx context.Context, projectID string) (map[string]string, error) {
	count := s.collection.Count()
	if count == 0 {
		return make(map[string]string), nil
	}

	dummyEmb := make([]float32, s.Dimension)
	// Only fetch records of type "file_meta" for efficiency
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, map[string]string{
		"project_id": projectID,
		"type":       "file_meta",
	}, nil)
	if err != nil {
		return nil, err
	}

	mapping := make(map[string]string)
	for _, r := range res {
		if path, ok := r.Metadata["path"]; ok {
			mapping[path] = r.Metadata["hash"]
		}
	}
	return mapping, nil
}

// GetFileHash retrieves the SHA256 content hash of a file at the specified path within a project.
func (s *Store) GetFileHash(ctx context.Context, path string, projectID string) (string, error) {
	dummyEmb := make([]float32, s.Dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, 1, map[string]string{
		"path":       path,
		"project_id": projectID,
		"type":       "file_meta",
	}, nil)
	if err != nil || len(res) == 0 {
		return "", err
	}
	return res[0].Metadata["hash"], nil
}

// Count returns the total number of records currently stored in the vector database.
func (s *Store) Count() int64 {
	return int64(s.collection.Count())
}

// GetAllMetadata returns all records with only their ID and Metadata populated.
func (s *Store) GetAllMetadata(ctx context.Context) ([]Record, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}

	dummyEmb := make([]float32, s.Dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, nil, nil)
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, doc := range res {
		records = append(records, Record{
			ID:       doc.ID,
			Metadata: doc.Metadata,
		})
	}
	return records, nil
}

// GetAllRecords retrieves all records from the database without any filtering or sorting.
func (s *Store) GetAllRecords(ctx context.Context) ([]Record, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}

	dummyEmb := make([]float32, s.Dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, nil, nil)
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, doc := range res {
		records = append(records, Record{
			ID:        doc.ID,
			Content:   doc.Content,
			Embedding: doc.Embedding,
			Metadata:  doc.Metadata,
		})
	}
	return records, nil
}

// GetByPath retrieves all records matching a specific file path and project identifier.
func (s *Store) GetByPath(ctx context.Context, path string, projectID string) ([]Record, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}

	dummyEmb := make([]float32, s.Dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, map[string]string{"path": path, "project_id": projectID}, nil)
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, doc := range res {
		records = append(records, Record{
			ID:        doc.ID,
			Content:   doc.Content,
			Embedding: doc.Embedding,
			Metadata:  doc.Metadata,
		})
	}
	return records, nil
}

// GetByPrefix retrieves all records matching a path prefix and project identifier.
func (s *Store) GetByPrefix(ctx context.Context, prefix string, projectID string) ([]Record, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}

	// Fetch all records for this project
	dummyEmb := make([]float32, s.Dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, map[string]string{"project_id": projectID}, nil)
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, doc := range res {
		path := doc.Metadata["path"]
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			records = append(records, Record{
				ID:        doc.ID,
				Content:   doc.Content,
				Embedding: doc.Embedding,
				Metadata:  doc.Metadata,
			})
		}
	}
	return records, nil
}

// SetStatus updates the indexing status of a project.
func (s *Store) SetStatus(ctx context.Context, projectID string, status string) error {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.set_status")
	defer span.End()

	id := fmt.Sprintf("status:%s", projectID)
	// Delete old status first
	if err := s.deleteByFilter(ctx, map[string]string{"type": "project_status", "project_id": projectID}); err != nil {
		return err
	}

	dummyEmb := make([]float32, s.Dimension)
	return s.Insert(ctx, []Record{{
		ID:        id,
		Content:   status,
		Embedding: dummyEmb,
		Metadata: map[string]string{
			"type":       "project_status",
			"project_id": projectID,
		},
	}})
}

// GetStatus retrieves the indexing status of a project.
func (s *Store) GetStatus(ctx context.Context, projectID string) (string, error) {
	dummyEmb := make([]float32, s.Dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, 1, map[string]string{"type": "project_status", "project_id": projectID}, nil)
	if err != nil || len(res) == 0 {
		return "", err
	}
	return res[0].Content, nil
}

// GetAllStatuses retrieves the indexing status of all projects.
func (s *Store) GetAllStatuses(ctx context.Context) (map[string]string, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}

	dummyEmb := make([]float32, s.Dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, map[string]string{"type": "project_status"}, nil)
	if err != nil {
		return nil, err
	}

	statuses := make(map[string]string)
	for _, r := range res {
		if projectID, ok := r.Metadata["project_id"]; ok {
			statuses[projectID] = r.Content
		}
	}
	return statuses, nil
}

var identifierRegexp = regexp.MustCompile(`[a-z][A-Z]|[a-z]_[a-z]|[:.()\[\]{}]`)

func hasCodeLikeIdentifiers(query string) bool {
	return identifierRegexp.MatchString(query)
}

// parseStringArray parses a JSON string array and caches the result
// to avoid repeated unmarshaling in search loops.
func (s *Store) parseStringArray(jsonStr string) []string {
	s.cacheMu.RLock()
	if val, ok := s.parsedCache[jsonStr]; ok {
		s.cacheMu.RUnlock()
		return val
	}
	s.cacheMu.RUnlock()

	var arr []string
	if err := json.Unmarshal([]byte(jsonStr), &arr); err != nil {
		return nil
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	// Partial eviction: if cache gets too big, remove ~10% of entries to prevent thundering herd
	if len(s.parsedCache) > MaxParsedCacheSize {
		evictCount := 1000
		for k := range s.parsedCache {
			delete(s.parsedCache, k)
			evictCount--
			if evictCount <= 0 {
				break
			}
		}
	}
	s.parsedCache[jsonStr] = arr
	return arr
}
