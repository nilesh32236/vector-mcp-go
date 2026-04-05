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

	"github.com/philippgille/chromem-go"
)

type Store struct {
	db          *chromem.DB
	collection  *chromem.Collection
	Dimension   int
	parsedCache map[string][]string
	cacheMu     sync.RWMutex
}

type Record struct {
	ID         string            `json:"id"`
	Content    string            `json:"content"`
	Embedding  []float32         `json:"embedding"`
	Metadata   map[string]string `json:"metadata"`
	Similarity float32           `json:"similarity,omitempty"`
}

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

	s := &Store{db: db, collection: col, Dimension: dimension, parsedCache: make(map[string][]string)}

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

	return s, nil
}

func (s *Store) Insert(ctx context.Context, records []Record) error {
	var docs []chromem.Document
	for _, r := range records {
		docs = append(docs, chromem.Document{
			ID:        r.ID,
			Content:   r.Content,
			Embedding: r.Embedding,
			Metadata:  r.Metadata,
		})
	}

	return s.collection.AddDocuments(ctx, docs, runtime.NumCPU())
}

func (s *Store) Search(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]Record, error) {
	records, _, err := s.SearchWithScore(ctx, queryEmbedding, topK, projectIDs, category)
	return records, err
}

func (s *Store) LexicalSearch(ctx context.Context, query string, topK int, projectIDs []string, category string) ([]Record, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}

	// Fetch all records for filtering
	// Using QueryEmbedding with dummy embedding to get all records
	dummyEmb := make([]float32, s.Dimension)
	var allResults []chromem.Result

	if len(projectIDs) == 0 {
		var where map[string]string
		if category != "" {
			where = map[string]string{"category": category}
		}
		res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, where, nil)
		if err != nil {
			return nil, err
		}
		allResults = res
	} else {
		for _, pid := range projectIDs {
			where := make(map[string]string)
			where["project_id"] = pid
			if category != "" {
				where["category"] = category
			}
			res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, where, nil)
			if err != nil {
				return nil, err
			}
			allResults = append(allResults, res...)
		}
	}

	var matches []Record
	queryLower := strings.ToLower(query)

	// Optimization: Parallelize the filtering loop for large datasets
	numCPU := runtime.NumCPU()
	if len(allResults) < 100 {
		numCPU = 1 // Don't overhead for small sets
	}

	resultChan := make(chan Record, len(allResults))
	var wg sync.WaitGroup
	chunkSize := (len(allResults) + numCPU - 1) / numCPU

	for i := 0; i < numCPU; i++ {
		start := i * chunkSize
		if start >= len(allResults) {
			break
		}
		end := start + chunkSize
		if end > len(allResults) {
			end = len(allResults)
		}

		wg.Add(1)
		go func(docs []chromem.Result) {
			defer wg.Done()
			for _, doc := range docs {
				isMatch := false

				// 1. Check Symbols metadata (JSON array)
				if symsJSON, ok := doc.Metadata["symbols"]; ok {
					if strings.Contains(strings.ToLower(symsJSON), queryLower) {
						if syms := s.parseStringArray(symsJSON); syms != nil {
							for _, sym := range syms {
								if strings.EqualFold(sym, query) || strings.Contains(strings.ToLower(sym), queryLower) {
									isMatch = true
									break
								}
							}
						}
					}
				}

				// 2. Check Name metadata
				if !isMatch {
					if name, ok := doc.Metadata["name"]; ok {
						if strings.EqualFold(name, query) || strings.Contains(strings.ToLower(name), queryLower) {
							isMatch = true
						}
					}
				}

				// 3. Check actual content
				if !isMatch {
					if strings.Contains(strings.ToLower(doc.Content), queryLower) {
						isMatch = true
					}
				}

				// 4. Check Calls metadata (JSON array)
				if !isMatch {
					if callsJSON, ok := doc.Metadata["calls"]; ok {
						if strings.Contains(strings.ToLower(callsJSON), queryLower) {
							if calls := s.parseStringArray(callsJSON); calls != nil {
								for _, call := range calls {
									if strings.EqualFold(call, query) {
										isMatch = true
										break
									}
								}
							}
						}
					}
				}

				if isMatch {
					resultChan <- Record{
						ID:       doc.ID,
						Content:  doc.Content,
						Metadata: doc.Metadata,
					}
				}
			}
		}(allResults[start:end])
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	for r := range resultChan {
		matches = append(matches, r)
	}

	if len(matches) > topK {
		matches = matches[:topK]
	}

	return matches, nil
}

func (s *Store) HybridSearch(ctx context.Context, query string, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]Record, error) {
	var (
		vectorResults  []Record
		lexicalResults []Record
		vectorErr      error
		lexicalErr     error
	)

	// 1. & 2. Concurrent Vector and Lexical Search (Fetch more for better RRF)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		vectorResults, vectorErr = s.Search(ctx, queryEmbedding, topK*2, projectIDs, category)
	}()

	go func() {
		defer wg.Done()
		lexicalResults, lexicalErr = s.LexicalSearch(ctx, query, topK*2, projectIDs, category)
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
	hasIdentifier := regexp.MustCompile(`[a-z][A-Z]|[a-z]_[a-z]|[:.()\[\]{}]`).MatchString(query)
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
					ageDays := float64(now-updatedAt) / (24 * 60 * 60)
					if ageDays < 0 {
						ageDays = 0
					}
					// Half-life of 14 days, max boost of 1.5x
					// Formula: 1.0 + 0.5 * 2^(-age/14)
					recencyBoost := 1.0 + 0.5*math.Pow(2, -ageDays/14.0)
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

func (s *Store) SearchWithScore(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]Record, []float32, error) {
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

func (s *Store) DeleteByPath(ctx context.Context, path string, projectID string) error {
	filter := map[string]string{"path": path, "project_id": projectID}
	return s.collection.Delete(ctx, filter, nil)
}

// DeleteByPrefix deletes all records where the metadata 'path' starts with the given prefix.
// This is critical for handling directory removals/renames correctly.
func (s *Store) DeleteByPrefix(ctx context.Context, prefix string, projectID string) error {
	count := s.collection.Count()
	if count == 0 {
		return nil
	}

	// Fetch all IDs for this project to check paths
	dummyEmb := make([]float32, s.Dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, map[string]string{"project_id": projectID}, nil)
	if err != nil {
		return err
	}

	for _, doc := range res {
		path := doc.Metadata["path"]
		// Check if it's the exact file OR a file within the directory (prefix + /)
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			s.collection.Delete(ctx, map[string]string{"path": path, "project_id": projectID}, nil)
		}
	}
	return nil
}

func (s *Store) ClearProject(ctx context.Context, projectID string) error {
	filter := map[string]string{"project_id": projectID}
	return s.collection.Delete(ctx, filter, nil)
}

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

func (s *Store) Count() int64 {
	return int64(s.collection.Count())
}

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

func (s *Store) SetStatus(ctx context.Context, projectID string, status string) error {
	id := fmt.Sprintf("status:%s", projectID)
	// Delete old status first
	s.collection.Delete(ctx, map[string]string{"type": "project_status", "project_id": projectID}, nil)

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

func (s *Store) GetStatus(ctx context.Context, projectID string) (string, error) {
	dummyEmb := make([]float32, s.Dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, 1, map[string]string{"type": "project_status", "project_id": projectID}, nil)
	if err != nil || len(res) == 0 {
		return "", err
	}
	return res[0].Content, nil
}

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
	if len(s.parsedCache) > 10000 {
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
