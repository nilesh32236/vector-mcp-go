package input

import (
	"regexp"
	"testing"
)

func TestRegexValidator_Validate(t *testing.T) {
	v := DefaultRegexValidator()

	tests := []struct {
		name      string
		pattern   string
		wantErr   error
		wantMatch bool // If compiled, should it match "test"?
	}{
		{
			name:    "simple pattern",
			pattern: "test",
			wantErr: nil,
		},
		{
			name:    "pattern with repetition",
			pattern: "a+",
			wantErr: nil,
		},
		{
			name:    "nested quantifier - ReDoS",
			pattern: "(a+)+",
			wantErr: ErrPatternTooComplex,
		},
		{
			name:    "nested star quantifier - ReDoS",
			pattern: "(a*)+",
			wantErr: ErrPatternTooComplex,
		},
		{
			name:    "nested optional - ReDoS",
			pattern: "(a?)+",
			wantErr: ErrPatternTooComplex,
		},
		{
			name:    "character class",
			pattern: "[a-z]+",
			wantErr: nil,
		},
		{
			name:    "alternation",
			pattern: "cat|dog",
			wantErr: nil,
		},
		{
			name:    "too many alternations",
			pattern: "a|b|c|d|e|f|g|h|i|j|k|l|m|n|o|p|q|r|s|t|u|v",
			wantErr: ErrPatternTooComplex,
		},
		{
			name:    "deep nesting",
			pattern: "((((((a))))))",
			wantErr: ErrPatternTooComplex, // 6 levels, default max is 5
		},
		{
			name:    "moderate nesting",
			pattern: "((((a))))",
			wantErr: nil, // 4 levels, default max is 5
		},
		{
			name:    "very deep nesting",
			pattern: "((((((((((a))))))))))",
			wantErr: ErrPatternTooComplex,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.pattern)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestRegexValidator_CompileRegex(t *testing.T) {
	v := DefaultRegexValidator()

	// Valid pattern
	re, err := v.CompileRegex("test")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !re.MatchString("test") {
		t.Error("expected pattern to match 'test'")
	}

	// Invalid pattern (ReDoS)
	_, err = v.CompileRegex("(a+)+")
	if err != ErrPatternTooComplex {
		t.Errorf("expected ErrPatternTooComplex, got %v", err)
	}
}

func TestRegexValidator_PatternTooLong(t *testing.T) {
	v := DefaultRegexValidator()

	// Create a very long pattern
	longPattern := ""
	for i := 0; i < 1100; i++ {
		longPattern += "a"
	}

	err := v.Validate(longPattern)
	if err != ErrPatternTooLong {
		t.Errorf("expected ErrPatternTooLong, got %v", err)
	}
}

func TestSanitizeQuery(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "normal query",
			input:    "search term",
			maxLen:   100,
			expected: "search term",
		},
		{
			name:     "query with null bytes",
			input:    "search\x00term",
			maxLen:   100,
			expected: "searchterm",
		},
		{
			name:     "query with control chars",
			input:    "search\x01\x02term",
			maxLen:   100,
			expected: "searchterm",
		},
		{
			name:     "query with tab and newline",
			input:    "search\tterm\n",
			maxLen:   100,
			expected: "search\tterm\n",
		},
		{
			name:     "truncated query",
			input:    "very long search term",
			maxLen:   10,
			expected: "very long ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SanitizeQuery(tt.input, tt.maxLen)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestValidateSearchQuery(t *testing.T) {
	err := ValidateSearchQuery("test query", 1000)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	err = ValidateSearchQuery("test", 2)
	if err != ErrInputTooLong {
		t.Errorf("expected ErrInputTooLong, got %v", err)
	}

	err = ValidateSearchQuery("test\x00query", 1000)
	if err != ErrInvalidCharacters {
		t.Errorf("expected ErrInvalidCharacters, got %v", err)
	}
}

func TestStringValidator_Validate(t *testing.T) {
	v := DefaultStringValidator()

	err := v.Validate("test string")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Empty allowed by default
	err = v.Validate("")
	if err != nil {
		t.Errorf("unexpected error for empty: %v", err)
	}

	// Too long
	longStr := ""
	for i := 0; i < 11000; i++ {
		longStr += "a"
	}
	err = v.Validate(longStr)
	if err != ErrInputTooLong {
		t.Errorf("expected ErrInputTooLong, got %v", err)
	}

	// Test with empty not allowed
	v2 := &StringValidator{
		MaxLength:  100,
		MinLength:  1,
		AllowEmpty: false,
	}
	err = v2.Validate("")
	if err == nil {
		t.Error("expected error for empty string")
	}

	// Test too short
	err = v2.Validate("a")
	if err != nil { // Min length 1, so "a" should be valid
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClampInt(t *testing.T) {
	tests := []struct {
		value, min, max, expected int
	}{
		{5, 0, 10, 5},
		{-5, 0, 10, 0},
		{15, 0, 10, 10},
		{5, 10, 20, 10},
	}

	for _, tt := range tests {
		result := ClampInt(tt.value, tt.min, tt.max)
		if result != tt.expected {
			t.Errorf("ClampInt(%d, %d, %d) = %d, want %d",
				tt.value, tt.min, tt.max, result, tt.expected)
		}
	}
}

func TestClampInt64(t *testing.T) {
	result := ClampInt64(50, 0, 100)
	if result != 50 {
		t.Errorf("expected 50, got %d", result)
	}

	result = ClampInt64(-10, 0, 100)
	if result != 0 {
		t.Errorf("expected 0, got %d", result)
	}

	result = ClampInt64(200, 0, 100)
	if result != 100 {
		t.Errorf("expected 100, got %d", result)
	}
}

func TestClampFloat64(t *testing.T) {
	result := ClampFloat64(5.5, 0.0, 10.0)
	if result != 5.5 {
		t.Errorf("expected 5.5, got %f", result)
	}

	result = ClampFloat64(-1.0, 0.0, 10.0)
	if result != 0.0 {
		t.Errorf("expected 0.0, got %f", result)
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	tests := []struct {
		input    string
		maxRunes int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel"},
		{"hello world", 5, "hello"},
		{"héllo wörld", 5, "héllo"}, // UTF-8 safe
		{"日本語テスト", 3, "日本語"},        // Japanese
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := TruncateRuneSafe(tt.input, tt.maxRunes)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestTruncateRuneSafeWithSuffix(t *testing.T) {
	result := TruncateRuneSafeWithSuffix("hello world", 5, "...")
	if result != "hello..." {
		t.Errorf("expected 'hello...', got %q", result)
	}

	// No truncation needed
	result = TruncateRuneSafeWithSuffix("hi", 5, "...")
	if result != "hi" {
		t.Errorf("expected 'hi', got %q", result)
	}

	// UTF-8 safe
	result = TruncateRuneSafeWithSuffix("héllo wörld", 5, "…")
	if result != "héllo…" {
		t.Errorf("expected 'héllo…', got %q", result)
	}
}

func TestValidateTopK(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{0, 1},
		{-5, 1},
		{5, 5},
		{500, 500},
		{2000, 1000},
	}

	for _, tt := range tests {
		result := ValidateTopK(tt.input)
		if result != tt.expected {
			t.Errorf("ValidateTopK(%d) = %d, want %d", tt.input, result, tt.expected)
		}
	}
}

func TestValidateLimit(t *testing.T) {
	result := ValidateLimit(50, 100)
	if result != 50 {
		t.Errorf("expected 50, got %d", result)
	}

	result = ValidateLimit(0, 100)
	if result != 1 {
		t.Errorf("expected 1 (min), got %d", result)
	}

	result = ValidateLimit(200, 100)
	if result != 100 {
		t.Errorf("expected 100 (max), got %d", result)
	}
}

func TestValidateOffset(t *testing.T) {
	result := ValidateOffset(50, 1000)
	if result != 50 {
		t.Errorf("expected 50, got %d", result)
	}

	result = ValidateOffset(-5, 1000)
	if result != 0 {
		t.Errorf("expected 0 (min), got %d", result)
	}

	result = ValidateOffset(200000, 1000)
	if result != 1000 {
		t.Errorf("expected 1000 (max), got %d", result)
	}
}

func TestIsPrintable(t *testing.T) {
	if !IsPrintable("hello world") {
		t.Error("expected printable string to be valid")
	}

	if !IsPrintable("héllo wörld") {
		t.Error("expected UTF-8 string to be valid")
	}

	if IsPrintable("hello\x00world") {
		t.Error("expected null byte to make string non-printable")
	}

	if IsPrintable("hello\x01world") {
		t.Error("expected control char to make string non-printable")
	}
}

func TestStripNonPrintable(t *testing.T) {
	result := StripNonPrintable("hello\x00world")
	if result != "helloworld" {
		t.Errorf("expected 'helloworld', got %q", result)
	}

	result = StripNonPrintable("hello\x01\x02world")
	if result != "helloworld" {
		t.Errorf("expected 'helloworld', got %q", result)
	}

	result = StripNonPrintable("normal text")
	if result != "normal text" {
		t.Errorf("expected 'normal text', got %q", result)
	}
}

func BenchmarkRegexValidator_Validate(b *testing.B) {
	v := DefaultRegexValidator()
	pattern := "[a-zA-Z0-9]+@[a-zA-Z0-9]+\\.[a-zA-Z]{2,}"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.Validate(pattern)
	}
}

func BenchmarkTruncateRuneSafe(b *testing.B) {
	input := "This is a long string that needs to be truncated for safety"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		TruncateRuneSafe(input, 20)
	}
}

func BenchmarkSanitizeQuery(b *testing.B) {
	input := "This is a search query with some\x00null\x01control chars"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SanitizeQuery(input, 1000)
	}
}

// Compile check
var _ *regexp.Regexp
