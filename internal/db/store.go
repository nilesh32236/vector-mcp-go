package db

import (
	"context"
	"fmt"
	"runtime"
	"github.com/philippgille/chromem-go"
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

	// Note: chromem-go doesn't support "OR" filters in 'where' easily.
	// We'll perform one broad search and filter by project_id manually if projectIDs is provided.
	res, err := s.collection.QueryEmbedding(ctx, queryEmbedding, topK*len(projectIDs)+topK, nil, nil)
	if err != nil {
		return nil, nil, err
	}

	var records []Record
	var scores []float32
	for _, doc := range res {
		if len(projectIDs) > 0 {
			match := false
			for _, pid := range projectIDs {
				if doc.Metadata["project_id"] == pid {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		records = append(records, Record{
			ID:      doc.ID,
			Content: doc.Content,
			Metadata: doc.Metadata,
			Similarity: doc.Similarity,
		})
		scores = append(scores, doc.Similarity)
		if len(records) >= topK {
			break
		}
	}
	return records, scores, nil
}

func (s *Store) DeleteByPath(ctx context.Context, path string, projectID string) error {
	filter := map[string]string{"path": path, "project_id": projectID}
	return s.collection.Delete(ctx, filter, nil)
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
			ID:      doc.ID,
			Content: doc.Content,
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
			ID:      doc.ID,
			Content: doc.Content,
			Metadata: doc.Metadata,
		})
	}
	return records, nil
}
