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
	ID        string
	Content   string
	Embedding []float32
	Metadata  map[string]string
}

func Connect(ctx context.Context, dbPath string, collectionName string) (*Store, error) {
	// Create or load the persistent DB
	// False for compress as code is generally small
	db, err := chromem.NewPersistentDB(dbPath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create persistent DB: %w", err)
	}
	
	// Get or create collection
	// We pass nil for embedding function because we'll provide them manually
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

	// Use number of CPUs for parallel processing
	return s.collection.AddDocuments(ctx, docs, runtime.NumCPU())
}

func (s *Store) Search(ctx context.Context, queryEmbedding []float32, topK int) ([]Record, error) {
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

func (s *Store) GetByID(ctx context.Context, id string) (Record, error) {
	doc, err := s.collection.GetByID(ctx, id)
	if err != nil {
		return Record{}, err
	}
	return Record{
		ID:      doc.ID,
		Content: doc.Content,
		Metadata: doc.Metadata,
	}, nil
}

func (s *Store) GetByMetadata(ctx context.Context, key, value string) ([]Record, error) {
	// Query with empty text but with metadata filter
	filter := map[string]string{key: value}
	
	// Chromem-go's Query requires a non-zero nResults if searching. 
	// If we just want to filter, we can use a large number or QueryWithOptions.
	res, err := s.collection.Query(ctx, "", 1000, filter, nil)
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
