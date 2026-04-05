package daemon

import (
	"context"
	"sync"
	"testing"

	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/testutil/mocks"
)

func TestService_Embed(t *testing.T) {
	mockEmbedder := mocks.NewMockEmbedder(384)
	svc := &Service{
		Embedder: mockEmbedder,
	}

	req := EmbedRequest{Text: "test text"}
	var resp EmbedResponse

	err := svc.Embed(req, &resp)
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(resp.Embedding) != 384 {
		t.Errorf("Expected embedding dimension 384, got %d", len(resp.Embedding))
	}
}

func TestService_EmbedBatch(t *testing.T) {
	mockEmbedder := mocks.NewMockEmbedder(384)
	svc := &Service{
		Embedder: mockEmbedder,
	}

	req := EmbedBatchRequest{Texts: []string{"text1", "text2", "text3"}}
	var resp EmbedBatchResponse

	err := svc.EmbedBatch(req, &resp)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	if len(resp.Embeddings) != 3 {
		t.Errorf("Expected 3 embeddings, got %d", len(resp.Embeddings))
	}
}

func TestService_Embed_Error(t *testing.T) {
	mockEmbedder := mocks.NewMockEmbedder(384).WithEmbedError(context.Canceled)
	svc := &Service{
		Embedder: mockEmbedder,
	}

	req := EmbedRequest{Text: "test text"}
	var resp EmbedResponse

	err := svc.Embed(req, &resp)
	if err == nil {
		t.Error("Expected error from mock embedder")
	}
}

func TestService_IndexProject(t *testing.T) {
	indexQueue := make(chan string, 1)
	svc := &Service{
		IndexQueue: indexQueue,
	}

	req := IndexRequest{Path: "/test/path"}
	var resp IndexResponse

	err := svc.IndexProject(req, &resp)
	if err != nil {
		t.Fatalf("IndexProject failed: %v", err)
	}

	if !resp.Success {
		t.Error("Expected success to be true")
	}

	select {
	case path := <-indexQueue:
		if path != "/test/path" {
			t.Errorf("Expected path /test/path, got %s", path)
		}
	default:
		t.Error("Expected path to be sent to index queue")
	}
}

func TestService_IndexProject_QueueFull(t *testing.T) {
	// Create a full queue
	indexQueue := make(chan string, 1)
	indexQueue <- "/existing/path"

	svc := &Service{
		IndexQueue: indexQueue,
	}

	req := IndexRequest{Path: "/test/path"}
	var resp IndexResponse

	err := svc.IndexProject(req, &resp)
	if err == nil {
		t.Error("Expected error when queue is full")
	}
}

func TestService_Search_NilStore(t *testing.T) {
	svc := &Service{
		Store: nil,
	}

	req := SearchRequest{
		Embedding: []float32{0.1, 0.2, 0.3},
		TopK:      5,
	}
	var resp SearchResponse

	err := svc.Search(req, &resp)
	if err == nil {
		t.Error("Expected error with nil store")
	}
}

func TestService_Insert_NilStore(t *testing.T) {
	svc := &Service{
		Store: nil,
	}

	req := InsertRequest{
		Records: []db.Record{{ID: "test"}},
	}
	var resp InsertResponse

	err := svc.Insert(req, &resp)
	if err == nil {
		t.Error("Expected error with nil store")
	}
}

func TestService_DeleteByPrefix_NilStore(t *testing.T) {
	svc := &Service{
		Store: nil,
	}

	req := DeleteRequest{Prefix: "test"}
	var resp DeleteResponse

	err := svc.DeleteByPrefix(req, &resp)
	if err == nil {
		t.Error("Expected error with nil store")
	}
}

func TestService_Count_NilStore(t *testing.T) {
	svc := &Service{
		Store: nil,
	}

	var resp struct{ Count int64 }

	err := svc.Count(struct{}{}, &resp)
	if err == nil {
		t.Error("Expected error with nil store")
	}
}

func TestService_GetProgress(t *testing.T) {
	progressMap := &sync.Map{}
	progressMap.Store("project1", "indexing")
	progressMap.Store("project2", "complete")

	svc := &Service{
		ProgressMap: progressMap,
	}

	var resp ProgressResponse

	err := svc.GetProgress(StatusRequest{}, &resp)
	if err != nil {
		t.Fatalf("GetProgress failed: %v", err)
	}

	if len(resp.Progress) != 2 {
		t.Errorf("Expected 2 progress entries, got %d", len(resp.Progress))
	}
}

func TestService_GetProgress_NilMap(t *testing.T) {
	svc := &Service{
		ProgressMap: nil,
	}

	var resp ProgressResponse

	err := svc.GetProgress(StatusRequest{}, &resp)
	if err != nil {
		t.Fatalf("GetProgress failed: %v", err)
	}

	if resp.Progress == nil {
		t.Error("Expected non-nil progress map")
	}
}

func TestNewClient(t *testing.T) {
	socketPath := "/tmp/test.sock"
	client := NewClient(socketPath)

	if client == nil {
		t.Fatal("Expected non-nil client")
	}
	if client.socketPath != socketPath {
		t.Errorf("Expected socketPath %s, got %s", socketPath, client.socketPath)
	}
}

func TestNewRemoteEmbedder(t *testing.T) {
	socketPath := "/tmp/test.sock"
	re := NewRemoteEmbedder(socketPath)

	if re == nil {
		t.Fatal("Expected non-nil RemoteEmbedder")
	}
	if re.socketPath != socketPath {
		t.Errorf("Expected socketPath %s, got %s", socketPath, re.socketPath)
	}
}

func TestNewRemoteStore(t *testing.T) {
	socketPath := "/tmp/test.sock"
	rs := NewRemoteStore(socketPath)

	if rs == nil {
		t.Fatal("Expected non-nil RemoteStore")
	}
	if rs.socketPath != socketPath {
		t.Errorf("Expected socketPath %s, got %s", socketPath, rs.socketPath)
	}
}

func TestRemoteStore_Count_ConnectionError(t *testing.T) {
	rs := NewRemoteStore("/nonexistent/socket")

	count := rs.Count()
	if count != 0 {
		t.Errorf("Expected count 0 on connection error, got %d", count)
	}
}

func TestRemoteStore_SetStatus(t *testing.T) {
	rs := NewRemoteStore("/nonexistent/socket")

	// SetStatus is a no-op that always returns nil
	err := rs.SetStatus(context.Background(), "project1", "complete")
	if err != nil {
		t.Errorf("SetStatus should return nil, got %v", err)
	}
}
