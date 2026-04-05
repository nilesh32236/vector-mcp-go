package db

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/apache/arrow/go/v17/arrow"
	"github.com/apache/arrow/go/v17/arrow/array"
	"github.com/apache/arrow/go/v17/arrow/memory"
	"github.com/lancedb/lancedb-go/pkg/contracts"
	"github.com/lancedb/lancedb-go/pkg/lancedb"
	"github.com/nilesh32236/vector-mcp-go/internal/db/lexical"
	"github.com/nilesh32236/vector-mcp-go/internal/observability/tracing"
)

type Store struct {
	db          contracts.IConnection
	table       contracts.ITable
	tableName   string
	Dimension   int
	parsedCache map[string][]string
	cacheMu     sync.RWMutex
	bm25        *lexical.Index // BM25 inverted index for O(log n) lexical search
}

type Record struct {
	ID         string            `json:"id"`
	Content    string            `json:"content"`
	Embedding  []float32         `json:"embedding"`
	Metadata   map[string]string `json:"metadata"`
	Similarity float32           `json:"similarity,omitempty"`
}

func Connect(ctx context.Context, dbPath string, tableName string, dimension int) (*Store, error) {
	// Connect to LanceDB
	db, err := lancedb.Connect(ctx, dbPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to LanceDB: %w", err)
	}

	tableNames, err := db.TableNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}

	var table contracts.ITable
	exists := false
	for _, name := range tableNames {
		if name == tableName {
			exists = true
			break
		}
	}

	s := &Store{
		db:          db,
		tableName:   tableName,
		Dimension:   dimension,
		parsedCache: make(map[string][]string),
		bm25:        lexical.NewIndex(),
	}

	if exists {
		table, err = db.OpenTable(ctx, tableName)
		if err != nil {
			return nil, fmt.Errorf("failed to open table: %w", err)
		}
		s.table = table

		// Validate schema/dimension
		schema, _ := table.Schema(ctx)
		if schema != nil {
			indices := schema.FieldIndices("vector")
			if len(indices) > 0 {
				field := schema.Field(indices[0])
				if listType, ok := field.Type.(*arrow.FixedSizeListType); ok {
					if int(listType.Len()) != dimension {
						return nil, fmt.Errorf("dimension mismatch: db has %d, expected %d", listType.Len(), dimension)
					}
				}
			}
		}
	} else {
		// Create table with schema
		builder := lancedb.NewSchemaBuilder()
		builder.AddStringField("id", false)
		builder.AddStringField("content", false)
		builder.AddVectorField("vector", dimension, contracts.VectorDataTypeFloat32, false)
		builder.AddStringField("metadata", false)
		schema, err := builder.Build()
		if err != nil {
			return nil, fmt.Errorf("failed to build schema: %w", err)
		}

		table, err = db.CreateTable(ctx, tableName, schema)
		if err != nil {
			return nil, fmt.Errorf("failed to create table: %w", err)
		}
		s.table = table

		// Explicitly configure HNSW index
		err = table.CreateIndex(ctx, []string{"vector"}, contracts.IndexTypeHnswPq)
		if err != nil {
			fmt.Printf("Warning: failed to create initial HNSW index: %v\n", err)
		}
	}

	if err := s.rebuildLexicalIndex(ctx); err != nil {
		return nil, fmt.Errorf("failed to bootstrap lexical index: %w", err)
	}

	return s, nil
}

func (s *Store) rebuildLexicalIndex(ctx context.Context) error {
	if s.table == nil {
		return nil
	}

	count, err := s.table.Count(ctx)
	if err != nil || count == 0 {
		return nil
	}

	results, err := s.table.Select(ctx, contracts.QueryConfig{
		Columns: []string{"id", "content", "metadata"},
	})
	if err != nil {
		return err
	}

	index := lexical.NewIndex()
	for _, row := range results {
		id := row["id"].(string)
		content := row["content"].(string)
		metadataJSON := row["metadata"].(string)
		
		var meta map[string]string
		json.Unmarshal([]byte(metadataJSON), &meta)
		
		index.Add(id, s.lexicalDocumentText(content, meta))
	}
	s.bm25 = index
	return nil
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

func (s *Store) Insert(ctx context.Context, records []Record) error {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.insert")
	defer span.End()

	if len(records) == 0 {
		return nil
	}

	// Create Arrow Record
	pool := memory.NewGoAllocator()
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "id", Type: arrow.BinaryTypes.String},
		{Name: "content", Type: arrow.BinaryTypes.String},
		{Name: "vector", Type: arrow.FixedSizeListOf(int32(s.Dimension), arrow.PrimitiveTypes.Float32)},
		{Name: "metadata", Type: arrow.BinaryTypes.String},
	}, nil)

	builder := array.NewRecordBuilder(pool, schema)
	defer builder.Release()

	idBuilder := builder.Field(0).(*array.StringBuilder)
	contentBuilder := builder.Field(1).(*array.StringBuilder)
	vectorBuilder := builder.Field(2).(*array.FixedSizeListBuilder)
	vectorValueBuilder := vectorBuilder.ValueBuilder().(*array.Float32Builder)
	metaBuilder := builder.Field(3).(*array.StringBuilder)

	for _, r := range records {
		idBuilder.Append(r.ID)
		contentBuilder.Append(r.Content)
		vectorBuilder.Append(true)
		vectorValueBuilder.AppendValues(r.Embedding, nil)
		
		metaJSON, _ := json.Marshal(r.Metadata)
		metaBuilder.Append(string(metaJSON))

		// Sync BM25
		s.bm25.Add(r.ID, s.lexicalDocumentText(r.Content, r.Metadata))
	}

	arrowRec := builder.NewRecord()
	defer arrowRec.Release()

	err := s.table.Add(ctx, arrowRec, &contracts.AddDataOptions{
		Mode: contracts.WriteModeAppend,
	})
	if err != nil {
		return fmt.Errorf("failed to add records to LanceDB: %w", err)
	}

	return nil
}

func (s *Store) Search(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]Record, error) {
	if topK <= 0 {
		return nil, nil
	}

	records, _, err := s.SearchWithScore(ctx, queryEmbedding, topK, projectIDs, category)
	return records, err
}

func (s *Store) SearchWithScore(ctx context.Context, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]Record, []float32, error) {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.vector_search")
	defer span.End()

	if s.table == nil || topK <= 0 {
		return nil, nil, nil
	}

	// Use VectorSearch
	results, err := s.table.VectorSearch(ctx, "vector", queryEmbedding, topK)
	if err != nil {
		return nil, nil, fmt.Errorf("vector search failed: %w", err)
	}

	var records []Record
	var scores []float32

	for _, row := range results {
		id := row["id"].(string)
		content := row["content"].(string)
		metadataJSON := row["metadata"].(string)
		
		var meta map[string]string
		json.Unmarshal([]byte(metadataJSON), &meta)
		
		distance := float32(0.0)
		if d, ok := row["_distance"]; ok {
			distance = float32(d.(float64))
		}
		// Assuming cosine metric used, similarity = 1 - distance
		similarity := 1.0 - distance

		if category != "" && meta["category"] != category {
			continue
		}
		if len(projectIDs) > 0 {
			found := false
			for _, pid := range projectIDs {
				if meta["project_id"] == pid {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		boost := float32(1.0)
		if pStr, ok := meta["priority"]; ok {
			if p, err := strconv.ParseFloat(pStr, 32); err == nil {
				boost = float32(p)
			}
		}

		records = append(records, Record{
			ID:         id,
			Content:    content,
			Metadata:   meta,
			Similarity: similarity * boost,
		})
		scores = append(scores, similarity*boost)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Similarity > records[j].Similarity
	})

	return records, scores, nil
}

func (s *Store) LexicalSearch(ctx context.Context, query string, topK int, projectIDs []string, category string) ([]Record, error) {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.lexical_search")
	defer span.End()

	if topK <= 0 || s.table == nil {
		return nil, nil
	}

	bm25Results := s.bm25.Search(query, topK*3)
	if len(bm25Results) == 0 {
		return nil, nil
	}

	idSet := make(map[string]float64)
	for _, r := range bm25Results {
		idSet[r.DocID] = r.Score
	}

	results, err := s.table.Select(ctx, contracts.QueryConfig{
		Columns: []string{"id", "content", "metadata"},
	})
	if err != nil {
		return nil, err
	}

	var matches []Record
	for _, row := range results {
		id := row["id"].(string)
		if score, ok := idSet[id]; ok {
			var meta map[string]string
			json.Unmarshal([]byte(row["metadata"].(string)), &meta)
			
			if category != "" && meta["category"] != category {
				continue
			}
			if len(projectIDs) > 0 {
				found := false
				for _, pid := range projectIDs {
					if meta["project_id"] == pid {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			matches = append(matches, Record{
				ID:       id,
				Content:  row["content"].(string),
				Metadata: meta,
				Similarity: float32(score),
			})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Similarity > matches[j].Similarity
	})

	if len(matches) > topK {
		matches = matches[:topK]
	}
	return matches, nil
}

func (s *Store) HybridSearch(ctx context.Context, query string, queryEmbedding []float32, topK int, projectIDs []string, category string) ([]Record, error) {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.hybrid_search")
	defer span.End()

	if topK <= 0 {
		return nil, nil
	}

	var (
		vectorResults  []Record
		lexicalResults []Record
		vectorErr      error
		lexicalErr     error
	)

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
		return nil, vectorErr
	}
	if lexicalErr != nil {
		return nil, lexicalErr
	}

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

	type ranked struct {
		r     Record
		score float64
	}
	var list []ranked
	for id, score := range scores {
		list = append(list, ranked{recordMap[id], score})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].score > list[j].score })

	var final []Record
	for i := 0; i < len(list) && i < topK; i++ {
		final = append(final, list[i].r)
	}
	return final, nil
}

func (s *Store) DeleteByPath(ctx context.Context, path string, projectID string) error {
	if s.table == nil {
		return nil
	}
	
	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"id", "metadata"}})
	if err != nil {
		return err
	}

	for _, row := range results {
		var meta map[string]string
		json.Unmarshal([]byte(row["metadata"].(string)), &meta)
		if meta["path"] == path && meta["project_id"] == projectID {
			id := row["id"].(string)
			s.bm25.Remove(id)
			s.table.Delete(ctx, fmt.Sprintf("id = '%s'", id))
		}
	}
	return nil
}

func (s *Store) DeleteByPrefix(ctx context.Context, prefix string, projectID string) error {
	if s.table == nil {
		return nil
	}
	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"id", "metadata"}})
	if err != nil {
		return err
	}

	for _, row := range results {
		var meta map[string]string
		json.Unmarshal([]byte(row["metadata"].(string)), &meta)
		if meta["project_id"] == projectID && (meta["path"] == prefix || strings.HasPrefix(meta["path"], prefix+"/")) {
			id := row["id"].(string)
			s.bm25.Remove(id)
			s.table.Delete(ctx, fmt.Sprintf("id = '%s'", id))
		}
	}
	return nil
}

func (s *Store) ClearProject(ctx context.Context, projectID string) error {
	if s.table == nil {
		return nil
	}
	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"id", "metadata"}})
	if err != nil {
		return err
	}

	for _, row := range results {
		var meta map[string]string
		json.Unmarshal([]byte(row["metadata"].(string)), &meta)
		if meta["project_id"] == projectID {
			id := row["id"].(string)
			s.bm25.Remove(id)
			s.table.Delete(ctx, fmt.Sprintf("id = '%s'", id))
		}
	}
	return nil
}

func (s *Store) GetPathHashMapping(ctx context.Context, projectID string) (map[string]string, error) {
	mapping := make(map[string]string)
	if s.table == nil {
		return mapping, nil
	}

	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"metadata"}})
	if err != nil {
		return nil, err
	}

	for _, row := range results {
		var meta map[string]string
		json.Unmarshal([]byte(row["metadata"].(string)), &meta)
		if meta["project_id"] == projectID && meta["type"] == "file_meta" {
			mapping[meta["path"]] = meta["hash"]
		}
	}
	return mapping, nil
}

func (s *Store) GetFileHash(ctx context.Context, path string, projectID string) (string, error) {
	if s.table == nil {
		return "", nil
	}
	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"metadata"}})
	if err != nil {
		return "", err
	}

	for _, row := range results {
		var meta map[string]string
		json.Unmarshal([]byte(row["metadata"].(string)), &meta)
		if meta["project_id"] == projectID && meta["path"] == path && meta["type"] == "file_meta" {
			return meta["hash"], nil
		}
	}
	return "", nil
}

func (s *Store) Count() int64 {
	if s.table == nil {
		return 0
	}
	c, _ := s.table.Count(context.Background())
	return c
}

func (s *Store) GetAllMetadata(ctx context.Context) ([]Record, error) {
	if s.table == nil {
		return nil, nil
	}
	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"id", "metadata"}})
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, row := range results {
		var meta map[string]string
		json.Unmarshal([]byte(row["metadata"].(string)), &meta)
		records = append(records, Record{
			ID:       row["id"].(string),
			Metadata: meta,
		})
	}
	return records, nil
}

func (s *Store) GetAllRecords(ctx context.Context) ([]Record, error) {
	if s.table == nil {
		return nil, nil
	}
	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"id", "content", "metadata"}})
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, row := range results {
		var meta map[string]string
		json.Unmarshal([]byte(row["metadata"].(string)), &meta)
		records = append(records, Record{
			ID:       row["id"].(string),
			Content:  row["content"].(string),
			Metadata: meta,
		})
	}
	return records, nil
}

func (s *Store) GetByPath(ctx context.Context, path string, projectID string) ([]Record, error) {
	if s.table == nil {
		return nil, nil
	}
	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"id", "content", "metadata"}})
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, row := range results {
		var meta map[string]string
		json.Unmarshal([]byte(row["metadata"].(string)), &meta)
		if meta["path"] == path && meta["project_id"] == projectID {
			records = append(records, Record{
				ID:       row["id"].(string),
				Content:  row["content"].(string),
				Metadata: meta,
			})
		}
	}
	return records, nil
}

func (s *Store) GetByPrefix(ctx context.Context, prefix string, projectID string) ([]Record, error) {
	if s.table == nil {
		return nil, nil
	}
	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"id", "content", "metadata"}})
	if err != nil {
		return nil, err
	}

	var records []Record
	for _, row := range results {
		var meta map[string]string
		json.Unmarshal([]byte(row["metadata"].(string)), &meta)
		if meta["project_id"] == projectID && (meta["path"] == prefix || strings.HasPrefix(meta["path"], prefix+"/")) {
			records = append(records, Record{
				ID:       row["id"].(string),
				Content:  row["content"].(string),
				Metadata: meta,
			})
		}
	}
	return records, nil
}

func (s *Store) SetStatus(ctx context.Context, projectID string, status string) error {
	ctx, span := tracing.StartSpan(ctx, "db.store", "db.set_status")
	defer span.End()

	if s.table != nil {
		s.table.Delete(ctx, fmt.Sprintf("id = 'status:%s'", projectID))
	}

	dummyEmb := make([]float32, s.Dimension)
	return s.Insert(ctx, []Record{{
		ID:        fmt.Sprintf("status:%s", projectID),
		Content:   status,
		Embedding: dummyEmb,
		Metadata: map[string]string{
			"type":       "project_status",
			"project_id": projectID,
		},
	}})
}

func (s *Store) GetStatus(ctx context.Context, projectID string) (string, error) {
	if s.table == nil {
		return "", nil
	}
	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"id", "content"}})
	if err != nil {
		return "", err
	}

	targetID := fmt.Sprintf("status:%s", projectID)
	for _, row := range results {
		if row["id"].(string) == targetID {
			return row["content"].(string), nil
		}
	}
	return "", nil
}

func (s *Store) GetAllStatuses(ctx context.Context) (map[string]string, error) {
	statuses := make(map[string]string)
	if s.table == nil {
		return statuses, nil
	}
	results, err := s.table.Select(ctx, contracts.QueryConfig{Columns: []string{"content", "metadata"}})
	if err != nil {
		return nil, err
	}

	for _, row := range results {
		var meta map[string]string
		json.Unmarshal([]byte(row["metadata"].(string)), &meta)
		if meta["type"] == "project_status" {
			statuses[meta["project_id"]] = row["content"].(string)
		}
	}
	return statuses, nil
}

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
