// Package input provides input validation and sanitization utilities
// to protect against injection attacks and denial-of-service vectors.
package input

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	// ErrPatternTooLong indicates the pattern exceeds maximum length
	ErrPatternTooLong = errors.New("pattern exceeds maximum length")
	// ErrPatternTooComplex indicates the pattern is too complex (ReDoS risk)
	ErrPatternTooComplex = errors.New("pattern is too complex (potential ReDoS)")
	// ErrInvalidCharacters indicates invalid characters in input
	ErrInvalidCharacters = errors.New("input contains invalid characters")
	// ErrInputTooLong indicates input exceeds maximum length
	ErrInputTooLong = errors.New("input exceeds maximum length")
)

// RegexValidator validates regex patterns for safety.
type RegexValidator struct {
	MaxLength      int
	MaxRepetition  int // Maximum consecutive repetition operators
	MaxNesting     int // Maximum nesting depth
	MaxAlternation int // Maximum alternation (|) count
}

// DefaultRegexValidator returns a validator with safe defaults.
func DefaultRegexValidator() *RegexValidator {
	return &RegexValidator{
		MaxLength:      1000,
		MaxRepetition:  3,
		MaxNesting:     5,
		MaxAlternation: 20,
	}
}

// Validate checks if a regex pattern is safe to compile.
func (v *RegexValidator) Validate(pattern string) error {
	if len(pattern) > v.MaxLength {
		return ErrPatternTooLong
	}

	// Check for nested quantifiers (a common ReDoS pattern)
	// e.g., (a+)+, (a*)+, (a?)+
	nestedQuantifier := regexp.MustCompile(`\([^)]*[+*?][^)]*\)[+*?]`)
	if nestedQuantifier.MatchString(pattern) {
		return ErrPatternTooComplex
	}

	// Count repetition operators
	repetitionCount := strings.Count(pattern, "+") +
		strings.Count(pattern, "*") +
		strings.Count(pattern, "?") +
		strings.Count(pattern, "{")
	if repetitionCount > v.MaxRepetition*10 {
		return ErrPatternTooComplex
	}

	// Count alternations
	alternationCount := strings.Count(pattern, "|")
	if alternationCount > v.MaxAlternation {
		return ErrPatternTooComplex
	}

	// Check nesting depth
	depth := 0
	maxDepth := 0
	for _, ch := range pattern {
		if ch == '(' {
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		} else if ch == ')' {
			depth--
		}
	}
	if maxDepth > v.MaxNesting {
		return ErrPatternTooComplex
	}

	return nil
}

// CompileRegex safely compiles a regex pattern after validation.
func (v *RegexValidator) CompileRegex(pattern string) (*regexp.Regexp, error) {
	if err := v.Validate(pattern); err != nil {
		return nil, err
	}
	return regexp.Compile(pattern)
}

// CompileRegexPOSIX safely compiles a POSIX regex pattern after validation.
func (v *RegexValidator) CompileRegexPOSIX(pattern string) (*regexp.Regexp, error) {
	if err := v.Validate(pattern); err != nil {
		return nil, err
	}
	return regexp.CompilePOSIX(pattern)
}

// SanitizeQuery sanitizes a search query for safe use.
// Removes control characters and limits length.
func SanitizeQuery(query string, maxLength int) (string, error) {
	if maxLength > 0 && len(query) > maxLength {
		// Truncate safely using rune-aware slicing
		runes := []rune(query)
		if len(runes) > maxLength {
			query = string(runes[:maxLength])
		}
	}

	// Remove null bytes and other control characters
	var sb strings.Builder
	sb.Grow(len(query))

	for _, r := range query {
		if r == 0x00 {
			continue // Skip null bytes
		}
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			continue // Skip control characters except tab, newline, carriage return
		}
		sb.WriteRune(r)
	}

	return sb.String(), nil
}

// ValidateSearchQuery validates a search query for safety.
func ValidateSearchQuery(query string, maxLength int) error {
	if maxLength > 0 && utf8.RuneCountInString(query) > maxLength {
		return ErrInputTooLong
	}

	// Check for null bytes
	if strings.ContainsRune(query, 0x00) {
		return ErrInvalidCharacters
	}

	return nil
}

// StringValidator validates string inputs.
type StringValidator struct {
	MaxLength   int
	MinLength   int
	AllowEmpty  bool
	TrimSpace   bool
	AllowedSets map[string]bool // Allowed character sets
}

// DefaultStringValidator returns a validator with safe defaults.
func DefaultStringValidator() *StringValidator {
	return &StringValidator{
		MaxLength:  10000,
		MinLength:  0,
		AllowEmpty: true,
		TrimSpace:  true,
	}
}

// Validate checks if a string input is valid.
func (v *StringValidator) Validate(s string) error {
	if v.TrimSpace {
		s = strings.TrimSpace(s)
	}

	if !v.AllowEmpty && s == "" {
		return errors.New("empty input not allowed")
	}

	runeCount := utf8.RuneCountInString(s)
	if v.MaxLength > 0 && runeCount > v.MaxLength {
		return ErrInputTooLong
	}

	if v.MinLength > 0 && runeCount < v.MinLength {
		return errors.New("input too short")
	}

	return nil
}

// ClampInt clamps an integer value between min and max.
func ClampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// ClampInt64 clamps an int64 value between min and max.
func ClampInt64(value, min, max int64) int64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// ClampFloat64 clamps a float64 value between min and max.
func ClampFloat64(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// TruncateRuneSafe truncates a string to maxRunes runes safely.
// This prevents UTF-8 corruption by working with runes instead of bytes.
func TruncateRuneSafe(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

// TruncateRuneSafeWithSuffix truncates a string to maxRunes runes safely,
// adding a suffix if truncation occurred.
func TruncateRuneSafeWithSuffix(s string, maxRunes int, suffix string) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + suffix
}

// ValidateTopK validates and clamps a topK parameter.
func ValidateTopK(topK int) int {
	return ClampInt(topK, 1, 1000)
}

// ValidateLimit validates and clamps a limit parameter.
func ValidateLimit(limit int, maxLimit int) int {
	if maxLimit <= 0 {
		maxLimit = 1000 // Default max
	}
	return ClampInt(limit, 1, maxLimit)
}

// ValidateOffset validates and clamps an offset parameter.
func ValidateOffset(offset int, maxOffset int) int {
	if maxOffset <= 0 {
		maxOffset = 100000 // Default max
	}
	return ClampInt(offset, 0, maxOffset)
}

// IsPrintable checks if a string contains only printable characters.
func IsPrintable(s string) bool {
	for _, r := range s {
		if !unicodePrintable(r) {
			return false
		}
	}
	return true
}

func unicodePrintable(r rune) bool {
	// Allow printable ASCII and common Unicode categories
	if r >= 0x20 && r <= 0x7E {
		return true
	}
	if r >= 0xA0 && r <= 0xFF {
		return true
	}
	// Allow common Unicode letters and numbers
	if r >= 0x100 && r <= 0xFFFF {
		return true
	}
	return false
}

// StripNonPrintable removes non-printable characters from a string.
func StripNonPrintable(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))

	for _, r := range s {
		if unicodePrintable(r) {
			sb.WriteRune(r)
		}
	}

	return sb.String()
}
