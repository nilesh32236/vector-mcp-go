package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nilesh32236/vector-mcp-go/internal/config"
	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

type deterministicEmbedder struct {
	dim int
}

// Embed returns a deterministic embedding for stable benchmark assertions.
func (m *deterministicEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.dim <= 0 {
		m.dim = 384
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(text))
	seed := h.Sum32()

	emb := make([]float32, m.dim)
	for i := 0; i < m.dim; i++ {
		v := float32((seed+uint32(i*31))%1000) / 1000.0
		emb[i] = v
	}
	return emb, nil
}

// EmbedBatch embeds a list of inputs using the same deterministic mapping as Embed.
func (m *deterministicEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, txt := range texts {
		emb, err := m.Embed(ctx, txt)
		if err != nil {
			return nil, err
		}
		out[i] = emb
	}
	return out, nil
}

// retrievalKPIThresholds captures minimum acceptable quality values for the fixture benchmark.
type retrievalKPIThresholds struct {
	MinRecall float64 `json:"min_recall"`
	MinMRR    float64 `json:"min_mrr"`
	MinNDCG   float64 `json:"min_ndcg"`
}

// countLines returns the number of lines in a string.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// loadKPIThresholds reads deterministic KPI thresholds for regression detection.
func loadKPIThresholds(path string) (retrievalKPIThresholds, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return retrievalKPIThresholds{}, err
	}
	var thresholds retrievalKPIThresholds
	if err := json.Unmarshal(b, &thresholds); err != nil {
		return retrievalKPIThresholds{}, err
	}
	return thresholds, nil
}

// TestRetrievalKPIsOnPolyglotFixture validates benchmark quality KPIs against fixture-backed minimums.
func TestRetrievalKPIsOnPolyglotFixture(t *testing.T) {
	ctx := context.Background()
	fixtureRoot := filepath.Join("fixtures", "polyglot")
	thresholdPath := filepath.Join(fixtureRoot, "kpi_thresholds.json")

	if _, err := os.Stat(fixtureRoot); err != nil {
		t.Fatalf("fixture not found at %s: %v", fixtureRoot, err)
	}
	thresholds, err := loadKPIThresholds(thresholdPath)
	if err != nil {
		t.Fatalf("failed to load KPI thresholds from %s: %v", thresholdPath, err)
	}

	tempDBDir := t.TempDir()
	store, err := db.Connect(ctx, tempDBDir, "bench_collection", 384)
	if err != nil {
		t.Fatalf("db connect failed: %v", err)
	}

	emb := &deterministicEmbedder{dim: 384}
	projectID := filepath.Clean(fixtureRoot)

	files, err := collectFixtureFiles(fixtureRoot)
	if err != nil {
		t.Fatalf("collect files failed: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no fixture files found")
	}

	totalLOC := 0
	indexStart := time.Now()
	for _, f := range files {
		b, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("read file failed (%s): %v", f, readErr)
		}
		content := string(b)
		totalLOC += countLines(content)
		chunks := indexer.CreateChunks(content, f)
		if len(chunks) == 0 {
			continue
		}

		relPath := config.GetRelativePath(f, fixtureRoot)
		recs := make([]db.Record, 0, len(chunks))
		for i, c := range chunks {
			vec, embErr := emb.Embed(ctx, c.Content)
			if embErr != nil {
				t.Fatalf("embed failed: %v", embErr)
			}
			recs = append(recs, db.Record{
				ID:        fmt.Sprintf("%s-%d", relPath, i),
				Content:   c.Content,
				Embedding: vec,
				Metadata: map[string]string{
					"path":       relPath,
					"project_id": projectID,
					"category":   "code",
				},
			})
		}
		if insErr := store.Insert(ctx, recs); insErr != nil {
			t.Fatalf("insert failed: %v", insErr)
		}
	}
	indexDur := time.Since(indexStart)

	if totalLOC <= 0 {
		t.Fatal("fixture LOC must be > 0")
	}

	type queryCase struct {
		query        string
		expectedPath string
	}
	queries := []queryCase{
		{query: "add two integers", expectedPath: "main.go"},
		{query: "find a user by id", expectedPath: "service.ts"},
		{query: "normalize string lowercase", expectedPath: "worker.py"},
	}

	k := 3
	hits := 0
	reciprocalRanks := make([]float64, 0, len(queries))
	ndcgs := make([]float64, 0, len(queries))
	latencies := make([]time.Duration, 0, len(queries))

	for _, q := range queries {
		qv, embErr := emb.Embed(ctx, q.query)
		if embErr != nil {
			t.Fatalf("query embed failed: %v", embErr)
		}

		start := time.Now()
		res, searchErr := store.HybridSearch(ctx, q.query, qv, k, []string{projectID}, "")
		latencies = append(latencies, time.Since(start))
		if searchErr != nil {
			t.Fatalf("search failed for query %q: %v", q.query, searchErr)
		}

		rankedPaths := extractPaths(res)
		if containsPath(rankedPaths[:min(k, len(rankedPaths))], q.expectedPath) {
			hits++
		}
		reciprocalRanks = append(reciprocalRanks, reciprocalRank(rankedPaths, q.expectedPath))
		ndcgs = append(ndcgs, ndcgAtK(rankedPaths, q.expectedPath, k))
	}

	recallAtK := float64(hits) / float64(len(queries))
	mrr := mean(reciprocalRanks)
	avgNDCG := mean(ndcgs)
	p50Latency := percentileDuration(latencies, 50)
	p95Latency := percentileDuration(latencies, 95)
	indexPerKLOC := indexDur.Seconds() / (float64(totalLOC) / 1000.0)

	t.Logf("KPI recall@%d=%.3f", k, recallAtK)
	t.Logf("KPI MRR=%.3f", mrr)
	t.Logf("KPI NDCG@%d=%.3f", k, avgNDCG)
	t.Logf("KPI index_time_per_KLOC=%.3fs", indexPerKLOC)
	t.Logf("KPI p50_latency=%s p95_latency=%s", p50Latency, p95Latency)

	if recallAtK < thresholds.MinRecall {
		t.Fatalf("recall@%d regression: got %.3f, want >= %.3f", k, recallAtK, thresholds.MinRecall)
	}
	if mrr < thresholds.MinMRR {
		t.Fatalf("mrr regression: got %.3f, want >= %.3f", mrr, thresholds.MinMRR)
	}
	if avgNDCG < thresholds.MinNDCG {
		t.Fatalf("ndcg@%d regression: got %.3f, want >= %.3f", k, avgNDCG, thresholds.MinNDCG)
	}
}

// collectFixtureFiles returns all files under the fixture root.
func collectFixtureFiles(root string) ([]string, error) {
	files := []string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

// extractPaths pulls path metadata from records in ranked order.
func extractPaths(records []db.Record) []string {
	paths := make([]string, 0, len(records))
	for _, r := range records {
		paths = append(paths, r.Metadata["path"])
	}
	return paths
}

// containsPath checks whether expected appears in the ranked path list.
func containsPath(paths []string, expected string) bool {
	for _, p := range paths {
		if strings.Contains(p, expected) {
			return true
		}
	}
	return false
}

// reciprocalRank computes reciprocal rank for a single relevant expected path.
func reciprocalRank(paths []string, expected string) float64 {
	for i, p := range paths {
		if strings.Contains(p, expected) {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

// ndcgAtK computes a binary-relevance NDCG at k for expected path membership.
func ndcgAtK(paths []string, expected string, k int) float64 {
	if k <= 0 {
		return 0
	}
	limit := min(k, len(paths))
	dcg := 0.0
	for i := 0; i < limit; i++ {
		rel := 0.0
		if strings.Contains(paths[i], expected) {
			rel = 1.0
		}
		dcg += rel / math.Log2(float64(i+2))
	}
	idcg := 1.0
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// mean returns the arithmetic average of values.
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total / float64(len(values))
}

// percentileDuration returns the nearest-rank percentile for a duration slice.
func percentileDuration(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	copied := append([]time.Duration(nil), values...)
	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })

	idx := int(math.Ceil((p/100.0)*float64(len(copied)))) - 1
	idx = min(max(idx, 0), len(copied)-1)
	return copied[idx]
}

// min returns the smaller integer.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// max returns the larger integer.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
