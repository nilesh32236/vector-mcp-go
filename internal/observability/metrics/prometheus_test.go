package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewCollector(t *testing.T) {
	c := NewCollector()
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
	if len(c.counters) != 0 {
		t.Errorf("expected empty counters, got %d", len(c.counters))
	}
	if len(c.gauges) != 0 {
		t.Errorf("expected empty gauges, got %d", len(c.gauges))
	}
}

func TestCounter_Inc(t *testing.T) {
	c := NewCollector()
	counter := c.NewCounter("test_counter", "Test counter", nil)

	if counter.Value() != 0 {
		t.Errorf("expected initial value 0, got %f", counter.Value())
	}

	counter.Inc()
	if counter.Value() != 1 {
		t.Errorf("expected value 1, got %f", counter.Value())
	}

	counter.Inc()
	if counter.Value() != 2 {
		t.Errorf("expected value 2, got %f", counter.Value())
	}
}

func TestCounter_Add(t *testing.T) {
	c := NewCollector()
	counter := c.NewCounter("test_counter", "Test counter", nil)

	counter.Add(5.5)
	if counter.Value() != 5.5 {
		t.Errorf("expected value 5.5, got %f", counter.Value())
	}

	counter.Add(2.5)
	if counter.Value() != 8 {
		t.Errorf("expected value 8, got %f", counter.Value())
	}
}

func TestGauge_Set(t *testing.T) {
	c := NewCollector()
	gauge := c.NewGauge("test_gauge", "Test gauge", nil)

	gauge.Set(10)
	if gauge.Value() != 10 {
		t.Errorf("expected value 10, got %f", gauge.Value())
	}

	gauge.Set(20)
	if gauge.Value() != 20 {
		t.Errorf("expected value 20, got %f", gauge.Value())
	}
}

func TestGauge_IncDec(t *testing.T) {
	c := NewCollector()
	gauge := c.NewGauge("test_gauge", "Test gauge", nil)

	gauge.Set(10)
	gauge.Inc()
	if gauge.Value() != 11 {
		t.Errorf("expected value 11, got %f", gauge.Value())
	}

	gauge.Dec()
	if gauge.Value() != 10 {
		t.Errorf("expected value 10, got %f", gauge.Value())
	}
}

func TestGauge_AddSub(t *testing.T) {
	c := NewCollector()
	gauge := c.NewGauge("test_gauge", "Test gauge", nil)

	gauge.Set(10)
	gauge.Add(5)
	if gauge.Value() != 15 {
		t.Errorf("expected value 15, got %f", gauge.Value())
	}

	gauge.Sub(3)
	if gauge.Value() != 12 {
		t.Errorf("expected value 12, got %f", gauge.Value())
	}
}

func TestHistogram_Observe(t *testing.T) {
	c := NewCollector()
	hist := c.NewHistogram("test_histogram", "Test histogram", nil)

	hist.Observe(0.005)
	hist.Observe(0.05)
	hist.Observe(0.5)
	hist.Observe(5)

	sum, count := hist.Value()
	if count != 4 {
		t.Errorf("expected count 4, got %d", count)
	}
	expectedSum := 0.005 + 0.05 + 0.5 + 5
	if sum != expectedSum {
		t.Errorf("expected sum %f, got %f", expectedSum, sum)
	}
}

func TestHistogram_Buckets(t *testing.T) {
	c := NewCollector()
	buckets := []float64{0.1, 0.5, 1.0}
	hist := c.NewHistogramWithBuckets("test_histogram", "Test", nil, buckets)

	// Values should go into appropriate buckets
	hist.Observe(0.05)  // bucket[0]
	hist.Observe(0.3)   // bucket[1]
	hist.Observe(0.7)   // bucket[2]
	hist.Observe(2.0)   // +Inf bucket

	_, count := hist.Value()
	if count != 4 {
		t.Errorf("expected count 4, got %d", count)
	}
}

func TestCollector_Export(t *testing.T) {
	c := NewCollector()
	counter := c.NewCounter("requests_total", "Total requests", map[string]string{"method": "GET"})
	gauge := c.NewGauge("active_connections", "Active connections", nil)
	hist := c.NewHistogram("request_duration", "Request duration", nil)

	counter.Inc()
	counter.Inc()
	gauge.Set(5)
	hist.Observe(0.1)

	output := c.Export()

	// Check counter
	if !strings.Contains(output, "requests_total") {
		t.Error("expected output to contain counter name")
	}
	if !strings.Contains(output, "Total requests") {
		t.Error("expected output to contain counter help")
	}

	// Check gauge
	if !strings.Contains(output, "active_connections") {
		t.Error("expected output to contain gauge name")
	}

	// Check histogram
	if !strings.Contains(output, "request_duration") {
		t.Error("expected output to contain histogram name")
	}
	if !strings.Contains(output, "request_duration_sum") {
		t.Error("expected output to contain histogram sum")
	}
	if !strings.Contains(output, "request_duration_count") {
		t.Error("expected output to contain histogram count")
	}

	// Check Go metrics
	if !strings.Contains(output, "goroutines") {
		t.Error("expected output to contain goroutines metric")
	}
}

func TestCollector_Handler(t *testing.T) {
	c := NewCollector()
	counter := c.NewCounter("test_counter", "Test", nil)
	counter.Inc()

	handler := c.Handler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("expected text/plain content type, got %s", contentType)
	}
}

func TestTimer(t *testing.T) {
	c := NewCollector()
	hist := c.NewHistogram("test_timer", "Test timer", nil)

	timer := NewTimer(hist)
	time.Sleep(10 * time.Millisecond)
	timer.ObserveDuration()

	_, count := hist.Value()
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
}

func TestTimer_Duration(t *testing.T) {
	timer := NewTimer(nil)
	time.Sleep(10 * time.Millisecond)
	duration := timer.Duration()

	if duration < 10*time.Millisecond {
		t.Errorf("expected duration >= 10ms, got %v", duration)
	}
}

func TestInitializeDefaultMetrics(t *testing.T) {
	InitializeDefaultMetrics()

	if SearchDuration == nil {
		t.Error("expected SearchDuration to be initialized")
	}
	if IndexFiles == nil {
		t.Error("expected IndexFiles to be initialized")
	}
	if EmbeddingPoolAvailable == nil {
		t.Error("expected EmbeddingPoolAvailable to be initialized")
	}
	if DBRecordsTotal == nil {
		t.Error("expected DBRecordsTotal to be initialized")
	}
	if RequestDuration == nil {
		t.Error("expected RequestDuration to be initialized")
	}
	if ErrorsTotal == nil {
		t.Error("expected ErrorsTotal to be initialized")
	}
}

func TestDefaultCollector(t *testing.T) {
	if DefaultCollector == nil {
		t.Fatal("expected DefaultCollector to be initialized")
	}

	counter := DefaultCollector.NewCounter("default_test", "Test", nil)
	counter.Inc()

	output := DefaultCollector.Export()
	if !strings.Contains(output, "default_test") {
		t.Error("expected output to contain default_test counter")
	}
}

func TestLabels(t *testing.T) {
	c := NewCollector()
	labels := map[string]string{
		"method": "GET",
		"path":   "/api/search",
	}
	counter := c.NewCounter("http_requests", "HTTP requests", labels)
	counter.Inc()

	output := c.Export()
	if !strings.Contains(output, `method="GET"`) {
		t.Error("expected output to contain method label")
	}
	if !strings.Contains(output, `path="/api/search"`) {
		t.Error("expected output to contain path label")
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := NewCollector()
	counter := c.NewCounter("concurrent_test", "Test", nil)

	done := make(chan bool)
	for i := 0; i < 100; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				counter.Inc()
			}
			done <- true
		}()
	}

	for i := 0; i < 100; i++ {
		<-done
	}

	if counter.Value() != 10000 {
		t.Errorf("expected 10000, got %f", counter.Value())
	}
}

func BenchmarkCounter_Inc(b *testing.B) {
	c := NewCollector()
	counter := c.NewCounter("bench_counter", "Benchmark counter", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		counter.Inc()
	}
}

func BenchmarkGauge_Set(b *testing.B) {
	c := NewCollector()
	gauge := c.NewGauge("bench_gauge", "Benchmark gauge", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gauge.Set(float64(i))
	}
}

func BenchmarkHistogram_Observe(b *testing.B) {
	c := NewCollector()
	hist := c.NewHistogram("bench_hist", "Benchmark histogram", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hist.Observe(0.1)
	}
}

func BenchmarkCollector_Export(b *testing.B) {
	c := NewCollector()
	counter := c.NewCounter("bench_counter", "Benchmark counter", nil)
	gauge := c.NewGauge("bench_gauge", "Benchmark gauge", nil)
	hist := c.NewHistogram("bench_hist", "Benchmark histogram", nil)

	for i := 0; i < 100; i++ {
		counter.Inc()
		gauge.Set(float64(i))
		hist.Observe(float64(i) * 0.01)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Export()
	}
}
