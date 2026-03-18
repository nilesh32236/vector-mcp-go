package daemon

import (
	"context"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// Service defines the RPC methods available on the Master.
type Service struct {
	Embedder   indexer.Embedder
	IndexQueue chan string
}

type EmbedRequest struct {
	Text string
}

type EmbedResponse struct {
	Embedding []float32
}

type IndexRequest struct {
	Path string
}

type IndexResponse struct {
	Success bool
}

func (s *Service) Embed(req EmbedRequest, resp *EmbedResponse) error {
	emb, err := s.Embedder.Embed(context.Background(), req.Text)
	if err != nil {
		return err
	}
	resp.Embedding = emb
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

// MasterServer manages the RPC server lifecycle.
type MasterServer struct {
	socketPath string
	listener   net.Listener
}

func StartMasterServer(socketPath string, embedder indexer.Embedder, indexQueue chan string) (*MasterServer, error) {
	// Clean up old socket if it exists
	_ = os.Remove(socketPath)

	svc := &Service{
		Embedder:   embedder,
		IndexQueue: indexQueue,
	}

	err := rpc.RegisterName("VectorDaemon", svc)
	if err != nil {
		return nil, err
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
	}, nil
}

func (s *MasterServer) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
	_ = os.Remove(s.socketPath)
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
