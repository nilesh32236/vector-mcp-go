package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/system"
)

// LanguageServerMapping associates file extensions with their respective LSP server commands.
var LanguageServerMapping = map[string][]string{
	".go":  {"gopls"},
	".js":  {"typescript-language-server", "--stdio"},
	".ts":  {"typescript-language-server", "--stdio"},
	".jsx": {"typescript-language-server", "--stdio"},
	".tsx": {"typescript-language-server", "--stdio"},
}

// GetServerCommand returns the command and arguments for the LSP server
// associated with a given file extension.
func GetServerCommand(extension string) ([]string, bool) {
	cmd, ok := LanguageServerMapping[strings.ToLower(extension)]
	return cmd, ok
}

// LSPManager handles the lifecycle and communication with a language server.
type LSPManager struct {
	serverCmd []string
	rootPath  string
	logger    *slog.Logger
	throttler *system.MemThrottler

	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.Reader
	lastUsed   time.Time
	idCounter  int64
	pending    map[int64]chan []byte
	handlers   map[string][]func([]byte)
	isStopping bool
}

// NewLSPManager creates a new LSPManager instance.
func NewLSPManager(serverCmd []string, rootPath string, logger *slog.Logger, throttler *system.MemThrottler) *LSPManager {
	return &LSPManager{
		serverCmd: serverCmd,
		rootPath:  rootPath,
		logger:    logger,
		throttler: throttler,
		pending:   make(map[int64]chan []byte),
		handlers:  make(map[string][]func([]byte)),
	}
}

// EnsureStarted ensures the LSP server is running, starting it if necessary.
func (m *LSPManager) EnsureStarted(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		m.lastUsed = time.Now()
		return nil
	}

	// Memory Throttling Check
	if m.throttler != nil && m.throttler.ShouldThrottle() {
		return fmt.Errorf("system memory pressure too high to start LSP")
	}

	if len(m.serverCmd) == 0 {
		return fmt.Errorf("no server command configured")
	}

	m.logger.Info("Starting LSP server", "command", m.serverCmd, "root", m.rootPath)
	cmd := exec.Command(m.serverCmd[0], m.serverCmd[1:]...)
	cmd.Dir = m.rootPath

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	m.cmd = cmd
	m.stdin = stdin
	m.stdout = stdout
	m.lastUsed = time.Now()
	m.isStopping = false

	// Start background reader
	go m.readLoop()

	// Start background TTL monitor
	go m.monitorTTL()

	// Initialize the LSP
	return m.initialize(ctx)
}

func (m *LSPManager) initialize(ctx context.Context) error {
	params := map[string]interface{}{
		"processId": os.Getpid(),
		"rootUri":   fmt.Sprintf("file://%s", m.rootPath),
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"definition": map[string]interface{}{
					"dynamicRegistration": true,
				},
				"references": map[string]interface{}{
					"dynamicRegistration": true,
				},
			},
		},
	}

	var result interface{}
	err := m.callLocked(ctx, "initialize", params, &result)
	if err != nil {
		return err
	}

	// Send initialized notification
	return m.notifyLocked("initialized", map[string]interface{}{})
}

// Call sends a request to the LSP and waits for a response.
func (m *LSPManager) Call(ctx context.Context, method string, params interface{}, result interface{}) error {
	if err := m.EnsureStarted(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callLocked(ctx, method, params, result)
}

func (m *LSPManager) callLocked(ctx context.Context, method string, params interface{}, result interface{}) error {
	m.idCounter++
	id := m.idCounter
	m.lastUsed = time.Now()

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	ch := make(chan []byte, 1)
	m.pending[id] = ch

	if err := m.writeLocked(data); err != nil {
		delete(m.pending, id)
		return err
	}

	m.mu.Unlock()
	defer m.mu.Lock()

	select {
	case resp := <-ch:
		if result != nil {
			var r struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp, &r); err != nil {
				return err
			}
			if r.Error != nil {
				return fmt.Errorf("LSP error (%d): %s", r.Error.Code, r.Error.Message)
			}
			return json.Unmarshal(r.Result, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Notify sends a notification to the LSP (no response expected).
func (m *LSPManager) Notify(ctx context.Context, method string, params interface{}) error {
	if err := m.EnsureStarted(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notifyLocked(method, params)
}

func (m *LSPManager) notifyLocked(method string, params interface{}) error {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return m.writeLocked(data)
}

// RegisterNotificationHandler registers a callback for a specific LSP notification method.
func (m *LSPManager) RegisterNotificationHandler(method string, handler func([]byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[method] = append(m.handlers[method], handler)
}

func (m *LSPManager) writeLocked(data []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := io.WriteString(m.stdin, header); err != nil {
		return err
	}
	if _, err := m.stdin.Write(data); err != nil {
		return err
	}
	return nil
}

func (m *LSPManager) readLoop() {
	reader := bufio.NewReader(m.stdout)
	for {
		// Read headers
		var contentLength int
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				m.handleDisconnect()
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length: ") {
				lengthStr := strings.TrimPrefix(line, "Content-Length: ")
				contentLength, _ = strconv.Atoi(lengthStr)
			}
		}

		// Read body
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			m.handleDisconnect()
			return
		}

		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &msg); err == nil {
			if len(msg.ID) > 0 {
				// Response to a request
				var id int64
				if err := json.Unmarshal(msg.ID, &id); err == nil {
					m.mu.Lock()
					if ch, ok := m.pending[id]; ok {
						ch <- body
						delete(m.pending, id)
					}
					m.mu.Unlock()
				}
			} else if msg.Method != "" {
				// Notification from server
				m.mu.Lock()
				handlers := m.handlers[msg.Method]
				m.mu.Unlock()
				for _, h := range handlers {
					go h(body)
				}
			}
		}
	}
}

func (m *LSPManager) handleDisconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isStopping {
		return
	}
	m.logger.Warn("LSP server disconnected unexpectedly")
	m.cleanupLocked()
}

func (m *LSPManager) cleanupLocked() {
	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
	}
	m.cmd = nil
	m.stdin = nil
	m.stdout = nil
	for id, ch := range m.pending {
		close(ch)
		delete(m.pending, id)
	}
}

func (m *LSPManager) monitorTTL() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		if m.cmd == nil {
			m.mu.Unlock()
			return
		}
		if time.Since(m.lastUsed) > 10*time.Minute {
			m.logger.Info("LSP server idle TTL reached, shutting down")
			m.isStopping = true
			m.cleanupLocked()
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
	}
}

func (m *LSPManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isStopping = true
	m.cleanupLocked()
}
