// Package metrics provides Prometheus-compatible metrics for monitoring.
package metrics

import (
	"expvar"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Collector collects and exposes metrics in Prometheus format.
type Collector struct {
	mu         sync.RWMutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
	startTime  time.Time
}

// Counter is a monotonically increasing counter.
type Counter struct {
	name   string
	help   string
	labels map[string]string
	value  float64
	mu     sync.Mutex
}

// Gauge is a value that can go up and down.
type Gauge struct {
	name   string
	help   string
	labels map[string]string
	value  float64
	mu     sync.Mutex
}

// Histogram tracks the distribution of values.
type Histogram struct {
	name    string
	help    string
	labels  map[string]string
	buckets []float64
	counts  []int64
	sum     float64
	count   int64
	mu      sync.Mutex
}

// NewCollector creates a new metrics collector.
func NewCollector() *Collector {
	return &Collector{
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
		startTime:  time.Now(),
	}
}

// NewCounter creates and registers a new counter.
func (c *Collector) NewCounter(name, help string, labels map[string]string) *Counter {
	c.mu.Lock()
	defer c.mu.Unlock()

	counter := &Counter{
		name:   name,
		help:   help,
		labels: labels,
		value:  0,
	}
	c.counters[name] = counter
	return counter
}

// NewGauge creates and registers a new gauge.
func (c *Collector) NewGauge(name, help string, labels map[string]string) *Gauge {
	c.mu.Lock()
	defer c.mu.Unlock()

	gauge := &Gauge{
		name:   name,
		help:   help,
		labels: labels,
		value:  0,
	}
	c.gauges[name] = gauge
	return gauge
}

// NewHistogram creates and registers a new histogram with default buckets.
func (c *Collector) NewHistogram(name, help string, labels map[string]string) *Histogram {
	return c.NewHistogramWithBuckets(name, help, labels, DefaultBuckets)
}

// NewHistogramWithBuckets creates and registers a new histogram with custom buckets.
func (c *Collector) NewHistogramWithBuckets(name, help string, labels map[string]string, buckets []float64) *Histogram {
	c.mu.Lock()
	defer c.mu.Unlock()

	histogram := &Histogram{
		name:    name,
		help:    help,
		labels:  labels,
		buckets: buckets,
		counts:  make([]int64, len(buckets)+1),
	}
	c.histograms[name] = histogram
	return histogram
}

// DefaultBuckets are the default histogram buckets for timing (in seconds).
var DefaultBuckets = []float64{
	0.001,  // 1ms
	0.005,  // 5ms
	0.01,   // 10ms
	0.025,  // 25ms
	0.05,   // 50ms
	0.1,    // 100ms
	0.25,   // 250ms
	0.5,    // 500ms
	1.0,    // 1s
	2.5,    // 2.5s
	5.0,    // 5s
	10.0,   // 10s
}

// Counter methods

// Inc increments the counter by 1.
func (c *Counter) Inc() {
	c.Add(1)
}

// Add adds the given value to the counter.
func (c *Counter) Add(v float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value += v
}

// Value returns the current counter value.
func (c *Counter) Value() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// Gauge methods

// Set sets the gauge to the given value.
func (g *Gauge) Set(v float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.value = v
}

// Inc increments the gauge by 1.
func (g *Gauge) Inc() {
	g.Add(1)
}

// Dec decrements the gauge by 1.
func (g *Gauge) Dec() {
	g.Add(-1)
}

// Add adds the given value to the gauge.
func (g *Gauge) Add(v float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.value += v
}

// Sub subtracts the given value from the gauge.
func (g *Gauge) Sub(v float64) {
	g.Add(-v)
}

// Value returns the current gauge value.
func (g *Gauge) Value() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.value
}

// Histogram methods

// Observe records an observation.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.sum += v
	h.count++

	// Find the appropriate bucket
	for i, bucket := range h.buckets {
		if v <= bucket {
			h.counts[i]++
			return
		}
	}
	// Goes into the +Inf bucket
	h.counts[len(h.buckets)]++
}

// Value returns the histogram sum and count.
func (h *Histogram) Value() (sum float64, count int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sum, h.count
}

// Export exports all metrics in Prometheus text format.
func (c *Collector) Export() string {
	var sb strings.Builder

	c.mu.RLock()
	defer c.mu.RUnlock()

	// Export counters
	for _, counter := range c.counters {
		sb.WriteString(formatMetric("counter", counter.name, counter.help, counter.labels, counter.value))
	}

	// Export gauges
	for _, gauge := range c.gauges {
		sb.WriteString(formatMetric("gauge", gauge.name, gauge.help, gauge.labels, gauge.value))
	}

	// Export histograms
	for _, hist := range c.histograms {
		sb.WriteString(formatHistogram(hist))
	}

	// Add Go runtime metrics
	c.exportGoMetrics(&sb)

	return sb.String()
}

func formatMetric(typ, name, help string, labels map[string]string, value float64) string {
	var sb strings.Builder

	// Help
	if help != "" {
		sb.WriteString(fmt.Sprintf("# HELP %s %s\n", name, help))
	}

	// Type
	sb.WriteString(fmt.Sprintf("# TYPE %s %s\n", name, typ))

	// Value with labels
	labelStr := formatLabels(labels)
	sb.WriteString(fmt.Sprintf("%s%s %g\n", name, labelStr, value))

	return sb.String()
}

func formatHistogram(h *Histogram) string {
	var sb strings.Builder

	h.mu.Lock()
	defer h.mu.Unlock()

	// Help
	if h.help != "" {
		sb.WriteString(fmt.Sprintf("# HELP %s %s\n", h.name, h.help))
	}

	// Type
	sb.WriteString(fmt.Sprintf("# TYPE %s histogram\n", h.name))

	labelStr := formatLabels(h.labels)

	// Bucket counts
	var cumulative int64
	for i, bucket := range h.buckets {
		cumulative += h.counts[i]
		bucketLabel := fmt.Sprintf(`{le="%g"`, bucket)
		if labelStr != "" {
			bucketLabel = fmt.Sprintf("%s,%s", strings.TrimSuffix(labelStr, "}"), strings.TrimPrefix(bucketLabel, "{"))
		} else {
			bucketLabel = fmt.Sprintf("%s%s", h.name, bucketLabel+"}")
		}
		sb.WriteString(fmt.Sprintf("%s %d\n", bucketLabel, cumulative))
	}

	// +Inf bucket
	cumulative += h.counts[len(h.buckets)]
	infLabel := fmt.Sprintf(`{le="+Inf"}`)
	if labelStr != "" {
		infLabel = fmt.Sprintf("%s%s,%s", h.name, strings.TrimSuffix(labelStr, "}"), strings.TrimPrefix(infLabel, "{")+"}")
	} else {
		infLabel = fmt.Sprintf("%s%s", h.name, infLabel)
	}
	sb.WriteString(fmt.Sprintf("%s %d\n", infLabel, cumulative))

	// Sum and count
	sb.WriteString(fmt.Sprintf("%s_sum%s %g\n", h.name, labelStr, h.sum))
	sb.WriteString(fmt.Sprintf("%s_count%s %d\n", h.name, labelStr, h.count))

	return sb.String()
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	var pairs []string
	for k, v := range labels {
		pairs = append(pairs, fmt.Sprintf(`%s="%s"`, k, v))
	}
	return fmt.Sprintf("{%s}", strings.Join(pairs, ","))
}

func (c *Collector) exportGoMetrics(sb *strings.Builder) {
	// Uptime
	uptime := time.Since(c.startTime).Seconds()
	sb.WriteString(fmt.Sprintf("# HELP vector_mcp_uptime_seconds Server uptime in seconds\n"))
	sb.WriteString(fmt.Sprintf("# TYPE vector_mcp_uptime_seconds gauge\n"))
	sb.WriteString(fmt.Sprintf("vector_mcp_uptime_seconds %g\n", uptime))

	// Goroutines
	sb.WriteString(fmt.Sprintf("# HELP vector_mcp_goroutines Number of goroutines\n"))
	sb.WriteString(fmt.Sprintf("# TYPE vector_mcp_goroutines gauge\n"))
	sb.WriteString(fmt.Sprintf("vector_mcp_goroutines %d\n", runtime.NumGoroutine()))

	// Memory stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	sb.WriteString(fmt.Sprintf("# HELP vector_mcp_memory_alloc_bytes Bytes of allocated heap objects\n"))
	sb.WriteString(fmt.Sprintf("# TYPE vector_mcp_memory_alloc_bytes gauge\n"))
	sb.WriteString(fmt.Sprintf("vector_mcp_memory_alloc_bytes %d\n", m.Alloc))

	sb.WriteString(fmt.Sprintf("# HELP vector_mcp_memory_sys_bytes Total bytes of memory obtained from the OS\n"))
	sb.WriteString(fmt.Sprintf("# TYPE vector_mcp_memory_sys_bytes gauge\n"))
	sb.WriteString(fmt.Sprintf("vector_mcp_memory_sys_bytes %d\n", m.Sys))

	sb.WriteString(fmt.Sprintf("# HELP vector_mcp_gc_pause_total_seconds Total GC pause time\n"))
	sb.WriteString(fmt.Sprintf("# TYPE vector_mcp_gc_pause_total_seconds counter\n"))
	sb.WriteString(fmt.Sprintf("vector_mcp_gc_pause_total_seconds %g\n", float64(m.PauseTotalNs)/1e9))
}

// Handler returns an http.Handler that serves Prometheus metrics.
func (c *Collector) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write([]byte(c.Export()))
	})
}

// DefaultCollector is the default global collector.
var DefaultCollector = NewCollector()

// Global metric instances
var (
	// SearchDuration tracks search operation durations
	SearchDuration *Histogram
	// IndexFiles counts indexed files
	IndexFiles *Counter
	// IndexBytes counts indexed bytes
	IndexBytes *Counter
	// EmbeddingPoolAvailable tracks available embedders
	EmbeddingPoolAvailable *Gauge
	// DBRecordsTotal tracks total records in database
	DBRecordsTotal *Gauge
	// RequestDuration tracks HTTP request durations
	RequestDuration *Histogram
	// ErrorsTotal counts errors by type
	ErrorsTotal *Counter
)

// InitializeDefaultMetrics initializes the default metrics.
func InitializeDefaultMetrics() {
	SearchDuration = DefaultCollector.NewHistogram(
		"vector_mcp_search_duration_seconds",
		"Duration of search operations in seconds",
		nil,
	)

	IndexFiles = DefaultCollector.NewCounter(
		"vector_mcp_index_files_total",
		"Total number of indexed files",
		nil,
	)

	IndexBytes = DefaultCollector.NewCounter(
		"vector_mcp_index_bytes_total",
		"Total bytes indexed",
		nil,
	)

	EmbeddingPoolAvailable = DefaultCollector.NewGauge(
		"vector_mcp_embedding_pool_available",
		"Number of available embedders in the pool",
		nil,
	)

	DBRecordsTotal = DefaultCollector.NewGauge(
		"vector_mcp_db_records_total",
		"Total number of records in the database",
		nil,
	)

	RequestDuration = DefaultCollector.NewHistogram(
		"vector_mcp_request_duration_seconds",
		"Duration of HTTP requests in seconds",
		nil,
	)

	ErrorsTotal = DefaultCollector.NewCounter(
		"vector_mcp_errors_total",
		"Total number of errors",
		nil,
	)
}

// Timer helps track operation durations.
type Timer struct {
	start    time.Time
	histogram *Histogram
}

// NewTimer creates a new timer.
func NewTimer(h *Histogram) *Timer {
	return &Timer{
		start:     time.Now(),
		histogram: h,
	}
}

// ObserveDuration records the duration since the timer was created.
func (t *Timer) ObserveDuration() {
	if t.histogram != nil {
		t.histogram.Observe(time.Since(t.start).Seconds())
	}
}

// Duration returns the duration since the timer was created.
func (t *Timer) Duration() time.Duration {
	return time.Since(t.start)
}

// expvar integration for compatibility with existing expvar-based metrics
func init() {
	// Register a custom expvar that exports Prometheus metrics
	expvar.Publish("prometheus", expvar.Func(func() interface{} {
		return DefaultCollector.Export()
	}))
}

// CounterFromExpvar creates a counter that mirrors an expvar.Int.
func CounterFromExpvar(name, expvarName string) {
	// This allows bridging expvar metrics to Prometheus format
	// The expvar package already handles atomic updates
	expvar.Publish(name, expvar.Func(func() interface{} {
		v := expvar.Get(expvarName)
		if v == nil {
			return 0
		}
		if s, ok := v.(fmt.Stringer); ok {
			if f, err := strconv.ParseFloat(s.String(), 64); err == nil {
				return f
			}
		}
		return 0
	}))
}
