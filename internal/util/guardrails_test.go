package util

import "testing"

func TestClampInt(t *testing.T) {
	tests := []struct {
		name       string
		value      int
		min        int
		max        int
		expected   int
	}{
		{name: "within range", value: 5, min: 1, max: 10, expected: 5},
		{name: "below range", value: -1, min: 1, max: 10, expected: 1},
		{name: "above range", value: 99, min: 1, max: 10, expected: 10},
		{name: "negative bounds", value: -8, min: -5, max: -1, expected: -5},
		{name: "swapped bounds", value: 7, min: 10, max: 1, expected: 7},
		{name: "swapped bounds below", value: -3, min: 10, max: 1, expected: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampInt(tc.value, tc.min, tc.max)
			if got != tc.expected {
				t.Fatalf("ClampInt(%d, %d, %d) = %d, want %d", tc.value, tc.min, tc.max, got, tc.expected)
			}
		})
	}
}

func TestClampInt64(t *testing.T) {
	tests := []struct {
		name     string
		value    int64
		min      int64
		max      int64
		expected int64
	}{
		{name: "within range", value: 50, min: 1, max: 100, expected: 50},
		{name: "below range", value: -100, min: -10, max: 10, expected: -10},
		{name: "above range", value: 101, min: 1, max: 100, expected: 100},
		{name: "swapped bounds", value: 7, min: 100, max: 1, expected: 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampInt64(tc.value, tc.min, tc.max)
			if got != tc.expected {
				t.Fatalf("ClampInt64(%d, %d, %d) = %d, want %d", tc.value, tc.min, tc.max, got, tc.expected)
			}
		})
	}
}

func TestClampFloat64(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		min      float64
		max      float64
		expected float64
	}{
		{name: "within range", value: 0.5, min: 0.0, max: 1.0, expected: 0.5},
		{name: "below range", value: -0.1, min: 0.0, max: 1.0, expected: 0.0},
		{name: "above range", value: 1.1, min: 0.0, max: 1.0, expected: 1.0},
		{name: "swapped bounds", value: 0.7, min: 1.0, max: 0.0, expected: 0.7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampFloat64(tc.value, tc.min, tc.max)
			if got != tc.expected {
				t.Fatalf("ClampFloat64(%f, %f, %f) = %f, want %f", tc.value, tc.min, tc.max, got, tc.expected)
			}
		})
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	invalidUTF8 := string([]byte{0xff, 0xfe, 'a', 0x80})

	tests := []struct {
		name     string
		in       string
		maxRunes int
		expected string
	}{
		{name: "empty string", in: "", maxRunes: 5, expected: ""},
		{name: "zero max", in: "hello", maxRunes: 0, expected: ""},
		{name: "negative max", in: "hello", maxRunes: -5, expected: ""},
		{name: "ascii truncate", in: "hello", maxRunes: 3, expected: "hel"},
		{name: "ascii no truncate", in: "hello", maxRunes: 5, expected: "hello"},
		{name: "multi-byte truncate", in: "Go語🙂", maxRunes: 3, expected: "Go語"},
		{name: "multi-byte no truncate", in: "Go語🙂", maxRunes: 4, expected: "Go語🙂"},
		{name: "invalid utf8 safe", in: invalidUTF8, maxRunes: 2, expected: string([]rune(invalidUTF8)[:2])},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateRuneSafe(tc.in, tc.maxRunes)
			if got != tc.expected {
				t.Fatalf("TruncateRuneSafe(%q, %d) = %q, want %q", tc.in, tc.maxRunes, got, tc.expected)
			}
		})
	}
}
