package db

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/philippgille/chromem-go"
)

type Store struct {
	db         *chromem.DB
	collection *chromem.Collection
	dimension  int
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

	s := &Store{db: db, collection: col, dimension: dimension}

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
	dummyEmb := make([]float32, s.dimension)
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
			where := map[string]string{"project_id": pid}
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

	for _, doc := range allResults {
		isMatch := false

		// Check Symbols metadata (stored as JSON array)
		if symsJSON, ok := doc.Metadata["symbols"]; ok {
			var syms []string
			if err := json.Unmarshal([]byte(symsJSON), &syms); err == nil {
				for _, sym := range syms {
					if strings.EqualFold(sym, query) || strings.Contains(strings.ToLower(sym), queryLower) {
						isMatch = true
						break
					}
				}
			}
		}

		// Check Name metadata (just in case)
		if name, ok := doc.Metadata["name"]; ok {
			if strings.EqualFold(name, query) || strings.Contains(strings.ToLower(name), queryLower) {
				isMatch = true
			}
		}

		// Check actual content for small snippets/declarations
		if !isMatch {
			if strings.Contains(strings.ToLower(doc.Content), queryLower) {
				isMatch = true
			}
		}

		if isMatch {
			matches = append(matches, Record{
				ID:       doc.ID,
				Content:  doc.Content,
				Metadata: doc.Metadata,
			})
		}
	}

	if len(matches) > topK {
		matches = matches[:topK]
	}

	return matches, nil
}

func (s *Store) HybridSearch(ctx context.Context, query string, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]Record, error) {
	// 1. Vector Search (Fetch more for better RRF)
	vectorResults, err := s.Search(ctx, queryEmbedding, topK*2, projectIDs, category)
	if err != nil {
		return nil, err
	}

	// 2. Lexical Search (Fetch more for better RRF)
	lexicalResults, err := s.LexicalSearch(ctx, query, topK*2, projectIDs, category)
	if err != nil {
		return nil, err
	}

	// 3. Reciprocal Rank Fusion (RRF)
	k := 60.0
	scores := make(map[string]float64)
	recordMap := make(map[string]Record)

	for i, r := range vectorResults {
		scores[r.ID] += 1.0 / (k + float64(i+1))
		recordMap[r.ID] = r
	}

	for i, r := range lexicalResults {
		scores[r.ID] += 1.0 / (k + float64(i+1))
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
			where := map[string]string{"project_id": pid}
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
		records = append(records, Record{
			ID:         doc.ID,
			Content:    doc.Content,
			Metadata:   doc.Metadata,
			Similarity: doc.Similarity,
		})
		scores = append(scores, doc.Similarity)
	}
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
	dummyEmb := make([]float32, s.dimension)
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

	dummyEmb := make([]float32, s.dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, map[string]string{"project_id": projectID}, nil)
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
	dummyEmb := make([]float32, s.dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, 1, map[string]string{"path": path, "project_id": projectID}, nil)
	if err != nil || len(res) == 0 {
		return "", err
	}
	return res[0].Metadata["hash"], nil
}

func (s *Store) Count() int {
	return s.collection.Count()
}

func (s *Store) GetAllRecords(ctx context.Context) ([]Record, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}

	dummyEmb := make([]float32, s.dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, nil, nil)
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, doc := range res {
		records = append(records, Record{
			ID:       doc.ID,
			Content:  doc.Content,
			Metadata: doc.Metadata,
		})
	}
	return records, nil
}

func (s *Store) GetByPath(ctx context.Context, path string, projectID string) ([]Record, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}

	dummyEmb := make([]float32, s.dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, map[string]string{"path": path, "project_id": projectID}, nil)
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, doc := range res {
		records = append(records, Record{
			ID:       doc.ID,
			Content:  doc.Content,
			Metadata: doc.Metadata,
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
	dummyEmb := make([]float32, s.dimension)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, map[string]string{"project_id": projectID}, nil)
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, doc := range res {
		path := doc.Metadata["path"]
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			records = append(records, Record{
				ID:       doc.ID,
				Content:  doc.Content,
				Metadata: doc.Metadata,
			})
		}
	}
	return records, nil
}

func (s *Store) SetStatus(ctx context.Context, projectID string, status string) error {
	id := fmt.Sprintf("status:%s", projectID)
	// Delete old status first
	s.collection.Delete(ctx, map[string]string{"type": "project_status", "project_id": projectID}, nil)

	dummyEmb := make([]float32, s.dimension)
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
	dummyEmb := make([]float32, s.dimension)
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

	dummyEmb := make([]float32, s.dimension)
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
