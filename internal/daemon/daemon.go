/*
 * Package daemon provides RPC-based communication between the MCP server and a master process.
 * It enables shared resource management, background indexing, and centralized state.
 */
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/rpc"
	"os"
	"sync"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// Service defines the RPC methods available on the Master.
const (
	EmbedTimeout      = 30 * time.Second
	BatchEmbedTimeout = 60 * time.Second
	RerankTimeout     = 120 * time.Second
)

type Service struct {
	Embedder    indexer.Embedder
	IndexQueue  chan string
	Store       *db.Store
	ProgressMap *sync.Map
}

// EmbedRequest represents a request to generate an embedding for a single text.
type EmbedRequest struct {
	Text string
}

// EmbedBatchRequest represents a request to generate embeddings for multiple texts.
type EmbedBatchRequest struct {
	Texts []string
}

// EmbedResponse contains the generated embedding for a single text.
type EmbedResponse struct {
	Embedding []float32
}

// EmbedBatchResponse contains the generated embeddings for multiple texts.
type EmbedBatchResponse struct {
	Embeddings [][]float32
}

// RerankBatchRequest represents a request to rerank a list of texts against a query.
type RerankBatchRequest struct {
	Query string
	Texts []string
}

// RerankBatchResponse contains the reranking scores for the requested texts.
type RerankBatchResponse struct {
	Scores []float32
}

// IndexRequest represents a request to trigger an indexing run for a project path.
type IndexRequest struct {
	Path string
}

// IndexResponse indicates the success status of an indexing request.
type IndexResponse struct {
	Success bool
}

// SearchRequest represents a semantic search request using vector embeddings.
type SearchRequest struct {
	Embedding []float32
	TopK      int
	PIDs      []string
	Category  string
}

// HybridSearchRequest represents a combined semantic and lexical search request.
type HybridSearchRequest struct {
	Query     string
	Embedding []float32
	TopK      int
	PIDs      []string
	Category  string
}

// SearchResponse contains the matching records and their respective scores.
type SearchResponse struct {
	Records []db.Record
	Scores  []float32
}

// InsertRequest represents a request to insert new records into the vector store.
type InsertRequest struct {
	Records []db.Record
}

// InsertResponse indicates the success status of a record insertion request.
type InsertResponse struct {
	Success bool
}

// DeleteRequest represents a request to delete records by prefix or project ID.
type DeleteRequest struct {
	Prefix    string
	ProjectID string
}

// DeleteResponse indicates the success status of a deletion request.
type DeleteResponse struct {
	Success bool
}

// StatusRequest represents a request for project indexing status.
type StatusRequest struct {
	ProjectID string
}

// StatusResponse contains the current status of one or more projects.
type StatusResponse struct {
	Status      string
	AllStatuses map[string]string
}

// MappingRequest represents a request for path-to-hash mapping of a project.
type MappingRequest struct {
	ProjectID string
}

// MappingResponse contains the requested path-to-hash mappings.
type MappingResponse struct {
	Mapping map[string]string
}

// Embed generates a single vector embedding for the provided text.
func (s *Service) Embed(req EmbedRequest, resp *EmbedResponse) error {
	emb, err := s.Embedder.Embed(context.Background(), req.Text)
	if err != nil {
		return err
	}
	resp.Embedding = emb
	return nil
}

// EmbedBatch generates vector embeddings for a batch of texts.
func (s *Service) EmbedBatch(req EmbedBatchRequest, resp *EmbedBatchResponse) error {
	embs, err := s.Embedder.EmbedBatch(context.Background(), req.Texts)
	if err != nil {
		return err
	}
	resp.Embeddings = embs
	return nil
}

// RerankBatch reranks a list of texts relative to a query string.
func (s *Service) RerankBatch(req RerankBatchRequest, resp *RerankBatchResponse) error {
	scores, err := s.Embedder.RerankBatch(context.Background(), req.Query, req.Texts)
	if err != nil {
		return err
	}
	resp.Scores = scores
	return nil
}

// IndexProject triggers an indexing operation for a project at the specified path.
func (s *Service) IndexProject(req IndexRequest, resp *IndexResponse) error {
	select {
	case s.IndexQueue <- req.Path:
		resp.Success = true
	default:
		return fmt.Errorf("master index queue is full")
	}
	return nil
}

// Search performs a semantic search using vector embeddings.
func (s *Service) Search(req SearchRequest, resp *SearchResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	res, scores, err := s.Store.SearchWithScore(context.Background(), req.Embedding, req.TopK, req.PIDs, req.Category)
	if err != nil {
		return err
	}
	resp.Records = res
	resp.Scores = scores
	return nil
}

// HybridSearch performs a combined semantic and lexical search.
func (s *Service) HybridSearch(req HybridSearchRequest, resp *SearchResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	res, err := s.Store.HybridSearch(context.Background(), req.Query, req.Embedding, req.TopK, req.PIDs, req.Category)
	if err != nil {
		return err
	}
	resp.Records = res
	return nil
}

// LexicalSearch performs a keyword-based search on the index.
func (s *Service) LexicalSearch(req HybridSearchRequest, resp *SearchResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	res, err := s.Store.LexicalSearch(context.Background(), req.Query, req.TopK, req.PIDs, req.Category)
	if err != nil {
		return err
	}
	resp.Records = res
	return nil
}

// Count returns the total number of records in the vector store.
func (s *Service) Count(_ struct{}, resp *struct{ Count int64 }) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	resp.Count = s.Store.Count()
	return nil
}

// Insert adds new records to the vector store.
func (s *Service) Insert(req InsertRequest, resp *InsertResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	err := s.Store.Insert(context.Background(), req.Records)
	if err == nil {
		resp.Success = true
	}
	return err
}

// DeleteByPrefix removes records that match a given prefix and project ID.
func (s *Service) DeleteByPrefix(req DeleteRequest, resp *DeleteResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	err := s.Store.DeleteByPrefix(context.Background(), req.Prefix, req.ProjectID)
	if err == nil {
		resp.Success = true
	}
	return err
}

// ClearProject removes all records for a specific project ID.
func (s *Service) ClearProject(req DeleteRequest, resp *DeleteResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	err := s.Store.ClearProject(context.Background(), req.ProjectID)
	if err == nil {
		resp.Success = true
	}
	return err
}

// GetStatus retrieves the indexing status of a specific project.
func (s *Service) GetStatus(req StatusRequest, resp *StatusResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	st, err := s.Store.GetStatus(context.Background(), req.ProjectID)
	if err != nil {
		return err
	}
	resp.Status = st
	return nil
}

// GetAllStatuses retrieves the indexing status of all active projects.
func (s *Service) GetAllStatuses(_ StatusRequest, resp *StatusResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	sts, err := s.Store.GetAllStatuses(context.Background())
	if err != nil {
		return err
	}
	resp.AllStatuses = sts
	return nil
}

// GetPathHashMapping returns the path-to-hash mapping for a project ID.
func (s *Service) GetPathHashMapping(req MappingRequest, resp *MappingResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	m, err := s.Store.GetPathHashMapping(context.Background(), req.ProjectID)
	if err != nil {
		return err
	}
	resp.Mapping = m
	return nil
}

// GetAllRecords retrieves all records from the vector store.
func (s *Service) GetAllRecords(_ StatusRequest, resp *SearchResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	recs, err := s.Store.GetAllRecords(context.Background())
	if err != nil {
		return err
	}
	resp.Records = recs
	return nil
}

// GetAllMetadata retrieves only the metadata for all records in the store.
func (s *Service) GetAllMetadata(_ StatusRequest, resp *SearchResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	recs, err := s.Store.GetAllMetadata(context.Background())
	if err != nil {
		return err
	}
	resp.Records = recs
	return nil
}

// GetByPath retrieves records matching a specific file path and project ID.
func (s *Service) GetByPath(req DeleteRequest, resp *SearchResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	recs, err := s.Store.GetByPath(context.Background(), req.Prefix, req.ProjectID)
	if err != nil {
		return err
	}
	resp.Records = recs
	return nil
}

// GetByPrefix retrieves records matching a path prefix and project ID.
func (s *Service) GetByPrefix(req DeleteRequest, resp *SearchResponse) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	recs, err := s.Store.GetByPrefix(context.Background(), req.Prefix, req.ProjectID)
	if err != nil {
		return err
	}
	resp.Records = recs
	return nil
}

// ProgressResponse contains indexing progress information for all active projects.
type ProgressResponse struct {
	Progress map[string]string
}

// GetProgress retrieves the current indexing progress from the progress map.
func (s *Service) GetProgress(_ StatusRequest, resp *ProgressResponse) error {
	resp.Progress = make(map[string]string)
	if s.ProgressMap != nil {
		s.ProgressMap.Range(func(k, v any) bool {
			resp.Progress[k.(string)] = v.(string)
			return true
		})
	}
	return nil
}

// MasterServer manages the RPC server lifecycle and allows updating its service.
type MasterServer struct {
	socketPath string
	listener   net.Listener
	svc        *Service
}

// StartMasterServer initializes and starts the master daemon on the specified socket path.
func StartMasterServer(socketPath string, embedder indexer.Embedder, indexQueue chan string, store *db.Store, progressMap *sync.Map) (*MasterServer, error) {
	svc := &Service{
		Embedder:    embedder,
		IndexQueue:  indexQueue,
		Store:       store,
		ProgressMap: progressMap,
	}

	// rpc.RegisterName is global for the default server.
	// Since we only ever have one MasterServer in a process, we can register it once.
	// However, to support updating the embedder later, we use a service pointer.
	if err := rpc.RegisterName("VectorDaemon", svc); err != nil {
		return nil, fmt.Errorf("failed to register RPC service: %w", err)
	}

	// Check if a master is already listening on the socket before removing it.
	if conn, err := net.DialTimeout("unix", socketPath, time.Second); err == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("master already running on %s", socketPath)
	}
	// Socket exists but is stale (no one listening) — safe to remove.
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Failed to remove stale socket file", "path", socketPath, "error", err)
	}

	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go rpc.ServeConn(conn)
		}
	}()

	return &MasterServer{
		socketPath: socketPath,
		listener:   l,
		svc:        svc,
	}, nil
}

// UpdateEmbedder dynamically updates the embedding engine used by the server.
func (s *MasterServer) UpdateEmbedder(e indexer.Embedder) {
	if s.svc != nil {
		s.svc.Embedder = e
	}
}

// UpdateStore dynamically updates the database store used by the server.
func (s *MasterServer) UpdateStore(st *db.Store) {
	if s.svc != nil {
		s.svc.Store = st
	}
}

// Close gracefully shuts down the master server and removes its socket file.
func (s *MasterServer) Close() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Failed to remove socket file on close", "path", s.socketPath, "error", err)
	}
}

// Client facilitates communication from Slave to Master.
type Client struct {
	socketPath string
}

// NewClient returns a new RPC client for interacting with the master daemon.
func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

// TriggerIndex requests the master server to start indexing a project path.
func (c *Client) TriggerIndex(path string) error {
	client, err := rpc.Dial("unix", c.socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	var resp IndexResponse
	err = client.Call("VectorDaemon.IndexProject", IndexRequest{Path: path}, &resp)
	if err != nil {
		return err
	}
	return nil
}

// GetProgress retrieves the latest indexing progress from the master server.
func (c *Client) GetProgress() (map[string]string, error) {
	client, err := rpc.Dial("unix", c.socketPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	var resp ProgressResponse
	if err := client.Call("VectorDaemon.GetProgress", StatusRequest{}, &resp); err != nil {
		return nil, err
	}
	return resp.Progress, nil
}

// RemoteEmbedder implements indexer.Embedder by delegating to the Master.
type RemoteEmbedder struct {
	socketPath string
}

// NewRemoteEmbedder returns an embedder that delegates operations to the master daemon.
func NewRemoteEmbedder(socketPath string) *RemoteEmbedder {
	return &RemoteEmbedder{socketPath: socketPath}
}

// Embed generates a single vector embedding by calling the master daemon.
func (re *RemoteEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	client, err := rpc.Dial("unix", re.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var resp EmbedResponse
	// RPC calls don't natively support context propagation easily in net/rpc,
	// but for local IPC this is usually acceptable.
	done := make(chan error, 1)
	go func() {
		done <- client.Call("VectorDaemon.Embed", EmbedRequest{Text: text}, &resp)
	}()

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return resp.Embedding, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(EmbedTimeout):
		return nil, fmt.Errorf("embedding RPC timeout")
	}
}

// EmbedBatch generates multiple vector embeddings by calling the master daemon.
func (re *RemoteEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	client, err := rpc.Dial("unix", re.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var resp EmbedBatchResponse
	done := make(chan error, 1)
	go func() {
		done <- client.Call("VectorDaemon.EmbedBatch", EmbedBatchRequest{Texts: texts}, &resp)
	}()

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return resp.Embeddings, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(BatchEmbedTimeout):
		return nil, fmt.Errorf("embedding batch RPC timeout")
	}
}

// RemoteStore implements db.Store methods by delegating to the Master via RPC.
type RemoteStore struct {
	socketPath string
}

// NewRemoteStore returns a store that delegates database operations to the master daemon.
func NewRemoteStore(socketPath string) *RemoteStore {
	return &RemoteStore{socketPath: socketPath}
}

func (rs *RemoteStore) call(method string, req any, resp any) error {
	client, err := rpc.Dial("unix", rs.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to master daemon: %w", err)
	}
	defer func() { _ = client.Close() }()
	return client.Call("VectorDaemon."+method, req, resp)
}

// Search performs a semantic search via the master daemon RPC interface.
func (rs *RemoteStore) Search(_ context.Context, embedding []float32, topK int, pids []string, category string) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("Search", SearchRequest{Embedding: embedding, TopK: topK, PIDs: pids, Category: category}, &resp)
	return resp.Records, err
}

// HybridSearch performs a hybrid search via the master daemon RPC interface.
func (rs *RemoteStore) HybridSearch(_ context.Context, query string, embedding []float32, topK int, pids []string, category string) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("HybridSearch", HybridSearchRequest{Query: query, Embedding: embedding, TopK: topK, PIDs: pids, Category: category}, &resp)
	return resp.Records, err
}

// Insert adds records to the database via the master daemon RPC interface.
func (rs *RemoteStore) Insert(_ context.Context, records []db.Record) error {
	var resp InsertResponse
	return rs.call("Insert", InsertRequest{Records: records}, &resp)
}

// DeleteByPrefix removes records via the master daemon RPC interface.
func (rs *RemoteStore) DeleteByPrefix(_ context.Context, prefix string, projectID string) error {
	var resp DeleteResponse
	return rs.call("DeleteByPrefix", DeleteRequest{Prefix: prefix, ProjectID: projectID}, &resp)
}

// ClearProject removes all project records via the master daemon RPC interface.
func (rs *RemoteStore) ClearProject(_ context.Context, projectID string) error {
	var resp DeleteResponse
	return rs.call("ClearProject", DeleteRequest{ProjectID: projectID}, &resp)
}

// GetStatus retrieves project status via the master daemon RPC interface.
func (rs *RemoteStore) GetStatus(_ context.Context, projectID string) (string, error) {
	var resp StatusResponse
	err := rs.call("GetStatus", StatusRequest{ProjectID: projectID}, &resp)
	return resp.Status, err
}

// GetAllStatuses retrieves all project statuses via the master daemon RPC interface.
func (rs *RemoteStore) GetAllStatuses(_ context.Context) (map[string]string, error) {
	var resp StatusResponse
	err := rs.call("GetAllStatuses", StatusRequest{}, &resp)
	return resp.AllStatuses, err
}

// GetPathHashMapping retrieves path-to-hash mappings via the master daemon RPC interface.
func (rs *RemoteStore) GetPathHashMapping(_ context.Context, projectID string) (map[string]string, error) {
	var resp MappingResponse
	err := rs.call("GetPathHashMapping", MappingRequest{ProjectID: projectID}, &resp)
	return resp.Mapping, err
}

// GetAllRecords retrieves all records via the master daemon RPC interface.
func (rs *RemoteStore) GetAllRecords(_ context.Context) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("GetAllRecords", StatusRequest{}, &resp)
	return resp.Records, err
}

// GetAllMetadata retrieves record metadata via the master daemon RPC interface.
func (rs *RemoteStore) GetAllMetadata(_ context.Context) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("GetAllMetadata", StatusRequest{}, &resp)
	return resp.Records, err
}

// GetByPath retrieves records by file path via the master daemon RPC interface.
func (rs *RemoteStore) GetByPath(_ context.Context, path string, projectID string) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("GetByPath", DeleteRequest{Prefix: path, ProjectID: projectID}, &resp)
	return resp.Records, err
}

// GetByPrefix retrieves records by prefix via the master daemon RPC interface.
func (rs *RemoteStore) GetByPrefix(_ context.Context, prefix string, projectID string) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("GetByPrefix", DeleteRequest{Prefix: prefix, ProjectID: projectID}, &resp)
	return resp.Records, err
}

// LexicalSearch performs a keyword search via the master daemon RPC interface.
func (rs *RemoteStore) LexicalSearch(_ context.Context, query string, topK int, projectIDs []string, category string) ([]db.Record, error) {
	var resp SearchResponse
	// We'll reuse HybridSearchRequest structure or create a new LexicalSearchRequest.
	// For simplicity, let's just use HybridSearchRequest but the master will call LexicalSearch.
	// Actually, let's create a LexicalSearchRequest for clarity if we want.
	// For now, let's assume we use HybridSearchRequest.
	err := rs.call("LexicalSearch", HybridSearchRequest{Query: query, TopK: topK, PIDs: projectIDs, Category: category}, &resp)
	return resp.Records, err
}

// Count returns the total records in the store via the master daemon RPC interface.
func (rs *RemoteStore) Count() int64 {
	var resp struct {
		Count int64
	}
	err := rs.call("Count", struct{}{}, &resp)
	if err != nil {
		return 0
	}
	return resp.Count
}

func (rs *RemoteStore) SetStatus(_ context.Context, projectID string, status string) error {
	// For now, let's just let slaves read status. Setting status is usually master's job during indexing.
	return nil
}

// SearchWithScore performs a semantic search and returns scores via the master daemon RPC interface.
func (rs *RemoteStore) SearchWithScore(_ context.Context, queryEmbedding []float32, topK int, pids []string, category string) ([]db.Record, []float32, error) {
	var resp SearchResponse
	err := rs.call("Search", SearchRequest{Embedding: queryEmbedding, TopK: topK, PIDs: pids, Category: category}, &resp)
	if err != nil {
		return nil, nil, err
	}
	return resp.Records, resp.Scores, nil
}

// RerankBatch reranks multiple texts relative to a query via the master daemon.
func (re *RemoteEmbedder) RerankBatch(ctx context.Context, query string, texts []string) ([]float32, error) {
	client, err := rpc.Dial("unix", re.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var resp RerankBatchResponse
	done := make(chan error, 1)
	go func() {
		done <- client.Call("VectorDaemon.RerankBatch", RerankBatchRequest{Query: query, Texts: texts}, &resp)
	}()

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return resp.Scores, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(RerankTimeout):
		return nil, fmt.Errorf("rerank batch RPC timeout")
	}
}
