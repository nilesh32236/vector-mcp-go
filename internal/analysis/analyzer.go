package analysis

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Issue represents a potential problem found during analysis.
type Issue struct {
	Path     string // File path
	Line     int    // Line number
	Column   int    // Column number
	Message  string // Description of the issue
	Severity string // Severity (Error, Warning, Info)
	Source   string // Analyzer name
}

// Analyzer defines the interface for proactive code analysis.
type Analyzer interface {
	Analyze(ctx context.Context, path string) ([]Issue, error)
	Name() string
}

// PatternAnalyzer scans files for specific regex patterns (e.g., TODOs, deprecated APIs).
type PatternAnalyzer struct {
	patterns map[string]*regexp.Regexp
}

func NewPatternAnalyzer() *PatternAnalyzer {
	return &PatternAnalyzer{
		patterns: map[string]*regexp.Regexp{
			"TODO":       regexp.MustCompile(`(?i)TODO[:\s]+(.*)`),
			"FIXME":      regexp.MustCompile(`(?i)FIXME[:\s]+(.*)`),
			"HACK":       regexp.MustCompile(`(?i)HACK[:\s]+(.*)`),
			"DEPRECATED": regexp.MustCompile(`(?i)DEPRECATED[:\s]+(.*)`),
		},
	}
}

func (p *PatternAnalyzer) Name() string { return "PatternAnalyzer" }

func (p *PatternAnalyzer) Analyze(ctx context.Context, path string) ([]Issue, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var issues []Issue
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		for category, re := range p.patterns {
			matches := re.FindStringSubmatch(line)
			if len(matches) > 0 {
				issues = append(issues, Issue{
					Path:     path,
					Line:     i + 1,
					Message:  fmt.Sprintf("[%s]: %s", category, strings.TrimSpace(matches[1])),
					Severity: "Info",
					Source:   p.Name(),
				})
			}
		}
	}
	return issues, nil
}

// VettingAnalyzer runs 'go vet' on Go files.
type VettingAnalyzer struct {
	ProjectRoot string
}

func NewVettingAnalyzer(root string) *VettingAnalyzer {
	return &VettingAnalyzer{ProjectRoot: root}
}

func (v *VettingAnalyzer) Name() string { return "VettingAnalyzer" }

func (v *VettingAnalyzer) Analyze(ctx context.Context, path string) ([]Issue, error) {
	if filepath.Ext(path) != ".go" {
		return nil, nil
	}

	// For a more robust implementation, we'd run go vet on the package,
	// but for proactive single-file indexing, we'll try to vet the file directly
	// or the directory it belongs to.
	cmd := exec.CommandContext(ctx, "go", "vet", path)
	cmd.Dir = v.ProjectRoot
	output, err := cmd.CombinedOutput()

	if err == nil {
		return nil, nil // No issues found
	}

	// Basic parsing of go vet output: file:line:message
	var issues []Issue
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Example: main.go:10:2: ...
		parts := strings.SplitN(line, ":", 4)
		if len(parts) >= 3 {
			issues = append(issues, Issue{
				Path:     parts[0],
				Message:  strings.TrimSpace(parts[len(parts)-1]),
				Severity: "Warning",
				Source:   v.Name(),
			})
		}
	}

	return issues, nil
}

// MultiAnalyzer runs multiple analyzers in sequence.
type MultiAnalyzer struct {
	analyzers []Analyzer
}

func NewMultiAnalyzer(analyzers ...Analyzer) *MultiAnalyzer {
	return &MultiAnalyzer{analyzers: analyzers}
}

func (m *MultiAnalyzer) Name() string { return "MultiAnalyzer" }

func (m *MultiAnalyzer) Analyze(ctx context.Context, path string) ([]Issue, error) {
	var allIssues []Issue
	for _, a := range m.analyzers {
		issues, err := a.Analyze(ctx, path)
		if err != nil {
			// Log error but continue with other analyzers
			continue
		}
		allIssues = append(allIssues, issues...)
	}
	return allIssues, nil
}
