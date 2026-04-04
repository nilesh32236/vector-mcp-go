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
type Service struct {
	Embedder    indexer.Embedder
	IndexQueue  chan string
	Store       *db.Store
	ProgressMap *sync.Map
}

type EmbedRequest struct {
	Text string
}

type EmbedBatchRequest struct {
	Texts []string
}

type EmbedResponse struct {
	Embedding []float32
}

type EmbedBatchResponse struct {
	Embeddings [][]float32
}

type RerankBatchRequest struct {
	Query string
	Texts []string
}

type RerankBatchResponse struct {
	Scores []float32
}

type IndexRequest struct {
	Path string
}

type IndexResponse struct {
	Success bool
}

type SearchRequest struct {
	Embedding []float32
	TopK      int
	PIDs      []string
	Category  string
}

type HybridSearchRequest struct {
	Query     string
	Embedding []float32
	TopK      int
	PIDs      []string
	Category  string
}

type SearchResponse struct {
	Records []db.Record
	Scores  []float32
}

type InsertRequest struct {
	Records []db.Record
}

type InsertResponse struct {
	Success bool
}

type DeleteRequest struct {
	Prefix    string
	ProjectID string
}

type DeleteResponse struct {
	Success bool
}

type StatusRequest struct {
	ProjectID string
}

type StatusResponse struct {
	Status      string
	AllStatuses map[string]string
}

type MappingRequest struct {
	ProjectID string
}

type MappingResponse struct {
	Mapping map[string]string
}

func (s *Service) Embed(req EmbedRequest, resp *EmbedResponse) error {
	emb, err := s.Embedder.Embed(context.Background(), req.Text)
	if err != nil {
		return err
	}
	resp.Embedding = emb
	return nil
}

func (s *Service) EmbedQuery(req EmbedRequest, resp *EmbedResponse) error {
	emb, err := s.Embedder.EmbedQuery(context.Background(), req.Text)
	if err != nil {
		return err
	}
	resp.Embedding = emb
	return nil
}

func (s *Service) EmbedBatch(req EmbedBatchRequest, resp *EmbedBatchResponse) error {
	embs, err := s.Embedder.EmbedBatch(context.Background(), req.Texts)
	if err != nil {
		return err
	}
	resp.Embeddings = embs
	return nil
}

func (s *Service) RerankBatch(req RerankBatchRequest, resp *RerankBatchResponse) error {
	scores, err := s.Embedder.RerankBatch(context.Background(), req.Query, req.Texts)
	if err != nil {
		return err
	}
	resp.Scores = scores
	return nil
}

func (s *Service) IndexProject(req IndexRequest, resp *IndexResponse) error {
	select {
	case s.IndexQueue <- req.Path:
		resp.Success = true
	default:
		return fmt.Errorf("master index queue is full")
	}
	return nil
}

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

func (s *Service) Count(req struct{}, resp *struct{ Count int64 }) error {
	if s.Store == nil {
		return fmt.Errorf("master store not initialized")
	}
	resp.Count = s.Store.Count()
	return nil
}

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

func (s *Service) GetAllStatuses(req StatusRequest, resp *StatusResponse) error {
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

func (s *Service) GetAllRecords(req StatusRequest, resp *SearchResponse) error {
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

func (s *Service) GetAllMetadata(req StatusRequest, resp *SearchResponse) error {
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

type ProgressResponse struct {
	Progress map[string]string
}

func (s *Service) GetProgress(_ StatusRequest, resp *ProgressResponse) error {
	resp.Progress = make(map[string]string)
	if s.ProgressMap != nil {
		s.ProgressMap.Range(func(k, v interface{}) bool {
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
		conn.Close()
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

func (s *MasterServer) UpdateEmbedder(e indexer.Embedder) {
	if s.svc != nil {
		s.svc.Embedder = e
	}
}

func (s *MasterServer) UpdateStore(st *db.Store) {
	if s.svc != nil {
		s.svc.Store = st
	}
}

func (s *MasterServer) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Failed to remove socket file on close", "path", s.socketPath, "error", err)
	}
}

// Client facilitates communication from Slave to Master.
type Client struct {
	socketPath string
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

func (c *Client) TriggerIndex(path string) error {
	client, err := rpc.Dial("unix", c.socketPath)
	if err != nil {
		return err
	}
	defer client.Close()

	var resp IndexResponse
	err = client.Call("VectorDaemon.IndexProject", IndexRequest{Path: path}, &resp)
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) GetProgress() (map[string]string, error) {
	client, err := rpc.Dial("unix", c.socketPath)
	if err != nil {
		return nil, err
	}
	defer client.Close()

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

func NewRemoteEmbedder(socketPath string) *RemoteEmbedder {
	return &RemoteEmbedder{socketPath: socketPath}
}

func (re *RemoteEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	client, err := rpc.Dial("unix", re.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master daemon: %w", err)
	}
	defer client.Close()

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
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("embedding RPC timeout")
	}
}

func (re *RemoteEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	client, err := rpc.Dial("unix", re.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master daemon: %w", err)
	}
	defer client.Close()

	var resp EmbedResponse
	done := make(chan error, 1)
	go func() {
		done <- client.Call("VectorDaemon.EmbedQuery", EmbedRequest{Text: text}, &resp)
	}()

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return resp.Embedding, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("embedding RPC timeout")
	}
}

func (re *RemoteEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	client, err := rpc.Dial("unix", re.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master daemon: %w", err)
	}
	defer client.Close()

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
	case <-time.After(60 * time.Second):
		return nil, fmt.Errorf("embedding batch RPC timeout")
	}
}

// RemoteStore implements db.Store methods by delegating to the Master via RPC.
type RemoteStore struct {
	socketPath string
}

func NewRemoteStore(socketPath string) *RemoteStore {
	return &RemoteStore{socketPath: socketPath}
}

func (rs *RemoteStore) call(method string, req interface{}, resp interface{}) error {
	client, err := rpc.Dial("unix", rs.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to master daemon: %w", err)
	}
	defer client.Close()
	return client.Call("VectorDaemon."+method, req, resp)
}

func (rs *RemoteStore) Search(ctx context.Context, embedding []float32, topK int, pids []string, category string) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("Search", SearchRequest{Embedding: embedding, TopK: topK, PIDs: pids, Category: category}, &resp)
	return resp.Records, err
}

func (rs *RemoteStore) HybridSearch(ctx context.Context, query string, embedding []float32, topK int, pids []string, category string) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("HybridSearch", HybridSearchRequest{Query: query, Embedding: embedding, TopK: topK, PIDs: pids, Category: category}, &resp)
	return resp.Records, err
}

func (rs *RemoteStore) Insert(ctx context.Context, records []db.Record) error {
	var resp InsertResponse
	return rs.call("Insert", InsertRequest{Records: records}, &resp)
}

func (rs *RemoteStore) DeleteByPrefix(ctx context.Context, prefix string, projectID string) error {
	var resp DeleteResponse
	return rs.call("DeleteByPrefix", DeleteRequest{Prefix: prefix, ProjectID: projectID}, &resp)
}

func (rs *RemoteStore) ClearProject(ctx context.Context, projectID string) error {
	var resp DeleteResponse
	return rs.call("ClearProject", DeleteRequest{ProjectID: projectID}, &resp)
}

func (rs *RemoteStore) GetStatus(ctx context.Context, projectID string) (string, error) {
	var resp StatusResponse
	err := rs.call("GetStatus", StatusRequest{ProjectID: projectID}, &resp)
	return resp.Status, err
}

func (rs *RemoteStore) GetAllStatuses(ctx context.Context) (map[string]string, error) {
	var resp StatusResponse
	err := rs.call("GetAllStatuses", StatusRequest{}, &resp)
	return resp.AllStatuses, err
}

func (rs *RemoteStore) GetPathHashMapping(ctx context.Context, projectID string) (map[string]string, error) {
	var resp MappingResponse
	err := rs.call("GetPathHashMapping", MappingRequest{ProjectID: projectID}, &resp)
	return resp.Mapping, err
}

func (rs *RemoteStore) GetAllRecords(ctx context.Context) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("GetAllRecords", StatusRequest{}, &resp)
	return resp.Records, err
}

func (rs *RemoteStore) GetAllMetadata(ctx context.Context) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("GetAllMetadata", StatusRequest{}, &resp)
	return resp.Records, err
}

func (rs *RemoteStore) GetByPath(ctx context.Context, path string, projectID string) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("GetByPath", DeleteRequest{Prefix: path, ProjectID: projectID}, &resp)
	return resp.Records, err
}

func (rs *RemoteStore) GetByPrefix(ctx context.Context, prefix string, projectID string) ([]db.Record, error) {
	var resp SearchResponse
	err := rs.call("GetByPrefix", DeleteRequest{Prefix: prefix, ProjectID: projectID}, &resp)
	return resp.Records, err
}

func (rs *RemoteStore) LexicalSearch(ctx context.Context, query string, topK int, projectIDs []string, category string) ([]db.Record, error) {
	var resp SearchResponse
	// We'll reuse HybridSearchRequest structure or create a new LexicalSearchRequest.
	// For simplicity, let's just use HybridSearchRequest but the master will call LexicalSearch.
	// Actually, let's create a LexicalSearchRequest for clarity if we want.
	// For now, let's assume we use HybridSearchRequest.
	err := rs.call("LexicalSearch", HybridSearchRequest{Query: query, TopK: topK, PIDs: projectIDs, Category: category}, &resp)
	return resp.Records, err
}

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

func (rs *RemoteStore) SetStatus(ctx context.Context, projectID string, status string) error {
	// For now, let's just let slaves read status. Setting status is usually master's job during indexing.
	return nil
}

func (rs *RemoteStore) SearchWithScore(ctx context.Context, queryEmbedding []float32, topK int, pids []string, category string) ([]db.Record, []float32, error) {
	var resp SearchResponse
	err := rs.call("Search", SearchRequest{Embedding: queryEmbedding, TopK: topK, PIDs: pids, Category: category}, &resp)
	if err != nil {
		return nil, nil, err
	}
	return resp.Records, resp.Scores, nil
}
func (re *RemoteEmbedder) RerankBatch(ctx context.Context, query string, texts []string) ([]float32, error) {
	client, err := rpc.Dial("unix", re.socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master daemon: %w", err)
	}
	defer client.Close()

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
	case <-time.After(120 * time.Second):
		return nil, fmt.Errorf("rerank batch RPC timeout")
	}
}
