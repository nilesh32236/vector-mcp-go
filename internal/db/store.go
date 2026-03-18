package db

import (
	"context"
	"fmt"
	"github.com/philippgille/chromem-go"
	"runtime"
	"sort"
	"strings"
)

type Store struct {
	db         *chromem.DB
	collection *chromem.Collection
}

type Record struct {
	ID         string            `json:"id"`
	Content    string            `json:"content"`
	Embedding  []float32         `json:"embedding"`
	Metadata   map[string]string `json:"metadata"`
	Similarity float32           `json:"similarity,omitempty"`
}

func Connect(ctx context.Context, dbPath string, collectionName string) (*Store, error) {
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

	return &Store{
		db:         db,
		collection: col,
	}, nil
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

func (s *Store) Search(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string) ([]Record, error) {
	records, _, err := s.SearchWithScore(ctx, queryEmbedding, topK, projectIDs)
	return records, err
}

func (s *Store) SearchWithScore(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string) ([]Record, []float32, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil, nil
	}

	if topK > count {
		topK = count
	}

	var allResults []chromem.Result
	if len(projectIDs) == 0 {
		res, err := s.collection.QueryEmbedding(ctx, queryEmbedding, topK, nil, nil)
		if err != nil {
			return nil, nil, err
		}
		allResults = res
	} else {
		// Perform individual queries for each project to avoid truncation issues
		for _, pid := range projectIDs {
			res, err := s.collection.QueryEmbedding(ctx, queryEmbedding, topK, map[string]string{"project_id": pid}, nil)
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
	dummyEmb := make([]float32, 1024)
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

	dummyEmb := make([]float32, 1024)
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
	dummyEmb := make([]float32, 1024)
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

	dummyEmb := make([]float32, 1024)
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

	dummyEmb := make([]float32, 1024)
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

func (s *Store) SetStatus(ctx context.Context, projectID string, status string) error {
	id := fmt.Sprintf("status:%s", projectID)
	// Delete old status first
	s.collection.Delete(ctx, map[string]string{"type": "project_status", "project_id": projectID}, nil)

	dummyEmb := make([]float32, 1024)
	return s.Insert(ctx, []Record{{
		ID:      id,
		Content: status,
		Embedding: dummyEmb,
		Metadata: map[string]string{
			"type":       "project_status",
			"project_id": projectID,
		},
	}})
}

func (s *Store) GetStatus(ctx context.Context, projectID string) (string, error) {
	dummyEmb := make([]float32, 1024)
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

	dummyEmb := make([]float32, 1024)
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
