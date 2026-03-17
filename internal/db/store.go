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
	ID        string            `json:"id"`
	Content   string            `json:"content"`
	Embedding []float32         `json:"embedding"`
	Metadata  map[string]string `json:"metadata"`
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

func (s *Store) Search(ctx context.Context, queryEmbedding []float32, topK int) ([]Record, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}
	if topK > count {
		topK = count
	}
	res, err := s.collection.QueryEmbedding(ctx, queryEmbedding, topK, nil, nil)
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

func (s *Store) GetByPath(ctx context.Context, path string) ([]Record, error) {
	count := s.collection.Count()
	if count == 0 {
		return nil, nil
	}
	
	// Use a dummy query with a metadata filter
	dummyEmb := make([]float32, 1024)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, count, map[string]string{"path": path}, nil)
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

func (s *Store) DeleteByPath(ctx context.Context, path string) error {
	filter := map[string]string{"path": path}
	return s.collection.Delete(ctx, filter, nil)
}

func (s *Store) GetPathHashMapping(ctx context.Context) (map[string]string, error) {
	records, err := s.GetAllRecords(ctx)
	if err != nil {
		return nil, err
	}
	mapping := make(map[string]string)
	for _, r := range records {
		if path, ok := r.Metadata["path"]; ok {
			mapping[path] = r.Metadata["hash"]
		}
	}
	return mapping, nil
}

func (s *Store) GetFileHash(ctx context.Context, path string) (string, error) {
	// We only need one record to check the hash
	dummyEmb := make([]float32, 1024)
	res, err := s.collection.QueryEmbedding(ctx, dummyEmb, 1, map[string]string{"path": path}, nil)
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
	
	// Chromem-go doesn't have a direct "get all", so we use a dummy query with high topK
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
