package pathguard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePath_BasicPath(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "pathguard-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	validator, err := NewValidator(tmpDir, DefaultOptions())
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	// Test valid relative path
	resolved, err := validator.ValidatePath("test.txt")
	if err != nil {
		t.Errorf("expected valid path, got error: %v", err)
	}
	if resolved == "" {
		t.Error("expected resolved path, got empty string")
	}

	// Test subdirectory path
	resolved, err = validator.ValidatePath("subdir/test.txt")
	if err != nil {
		t.Errorf("expected valid subdirectory path, got error: %v", err)
	}
	if resolved == "" {
		t.Error("expected resolved path, got empty string")
	}
}

func TestValidatePath_TraversalAttack(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pathguard-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	validator, err := NewValidator(tmpDir, DefaultOptions())
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{
			name:    "parent directory traversal",
			path:    "../etc/passwd",
			wantErr: ErrPathTraversal,
		},
		{
			name:    "multi-level traversal",
			path:    "../../etc/passwd",
			wantErr: ErrPathTraversal,
		},
		{
			name:    "deep traversal",
			path:    "../../../..",
			wantErr: ErrPathTraversal,
		},
		{
			name:    "mixed traversal",
			path:    "subdir/../../../etc/passwd",
			wantErr: ErrPathTraversal,
		},
		{
			name:    "traversal with null byte",
			path:    "test\x00.txt",
			wantErr: ErrInvalidPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validator.ValidatePath(tt.path)
			if err == nil {
				t.Errorf("expected error for path %q, got nil", tt.path)
			}
		})
	}
}

func TestValidatePath_AbsolutePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pathguard-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Test with absolute paths allowed (default)
	validator, err := NewValidator(tmpDir, DefaultOptions())
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	// Absolute path within base should be allowed
	absPath := filepath.Join(tmpDir, "test.txt")
	resolved, err := validator.ValidatePath(absPath)
	if err != nil {
		t.Errorf("expected absolute path within base to be allowed, got error: %v", err)
	}
	if resolved == "" {
		t.Error("expected resolved path")
	}

	// Absolute path outside base should be denied
	_, err = validator.ValidatePath("/etc/passwd")
	if err != ErrPathTraversal {
		t.Errorf("expected ErrPathTraversal for /etc/passwd, got: %v", err)
	}
}

func TestValidatePath_DisallowAbsolute(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pathguard-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Test with absolute paths disabled
	opts := DefaultOptions()
	opts.AllowAbsolute = false
	validator, err := NewValidator(tmpDir, opts)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	// Any absolute path should be denied
	_, err = validator.ValidatePath("/etc/passwd")
	if err != ErrPathTraversal {
		t.Errorf("expected ErrPathTraversal, got: %v", err)
	}

	// Even absolute path within base
	absPath := filepath.Join(tmpDir, "test.txt")
	_, err = validator.ValidatePath(absPath)
	if err != ErrPathTraversal {
		t.Errorf("expected ErrPathTraversal for absolute path when disabled, got: %v", err)
	}
}

func TestValidatePath_Symlinks(t *testing.T) {
	// Skip if not running as root (symlinks to parent dirs may be allowed)
	if os.Getuid() == 0 {
		t.Skip("skipping symlink test when running as root")
	}

	tmpDir, err := os.MkdirTemp("", "pathguard-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a symlink pointing outside the base
	outsideFile := filepath.Join(os.TempDir(), "outside-test.txt")
	if err := os.WriteFile(outsideFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create outside file: %v", err)
	}
	defer func() { _ = os.Remove(outsideFile) }()

	linkPath := filepath.Join(tmpDir, "link")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Test with symlinks disabled (default)
	validator, err := NewValidator(tmpDir, DefaultOptions())
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	_, err = validator.ValidatePath("link")
	// Symlink should be rejected - either as symlink or as path traversal (after evaluation)
	if err != ErrSymlinkDenied && err != ErrPathTraversal {
		t.Errorf("expected ErrSymlinkDenied or ErrPathTraversal, got: %v", err)
	}
}

func TestValidatePath_AllowSymlinks(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pathguard-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a file inside the temp dir
	internalFile := filepath.Join(tmpDir, "internal.txt")
	if err := os.WriteFile(internalFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create internal file: %v", err)
	}

	// Create a symlink to the internal file
	linkPath := filepath.Join(tmpDir, "link")
	if err := os.Symlink(internalFile, linkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Test with symlinks allowed
	opts := DefaultOptions()
	opts.AllowSymlinks = true
	validator, err := NewValidator(tmpDir, opts)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	// Symlink to file within base should be allowed
	resolved, err := validator.ValidatePath("link")
	if err != nil {
		t.Errorf("expected symlink to internal file to be allowed, got error: %v", err)
	}
	if resolved == "" {
		t.Error("expected resolved path")
	}
}

func TestValidatePath_MaxDepth(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pathguard-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Test with max depth of 3
	opts := DefaultOptions()
	opts.MaxPathDepth = 3
	validator, err := NewValidator(tmpDir, opts)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	// Path with 3 components should be allowed
	_, err = validator.ValidatePath("a/b/c")
	if err != nil {
		t.Errorf("expected path with 3 components to be allowed, got error: %v", err)
	}

	// Path with 4 components should be denied
	_, err = validator.ValidatePath("a/b/c/d")
	if err != ErrPathTooDeep {
		t.Errorf("expected ErrPathTooDeep for path with 4 components, got: %v", err)
	}
}

func TestValidatePath_EmptyPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pathguard-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	validator, err := NewValidator(tmpDir, DefaultOptions())
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	_, err = validator.ValidatePath("")
	if err != ErrInvalidPath {
		t.Errorf("expected ErrInvalidPath for empty path, got: %v", err)
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal filename",
			input:    "test.txt",
			expected: "test.txt",
		},
		{
			name:     "filename with slashes",
			input:    "path/to/file.txt",
			expected: "path_to_file.txt",
		},
		{
			name:     "hidden file",
			input:    ".hidden",
			expected: "hidden",
		},
		{
			name:     "double hidden",
			input:    "..hidden",
			expected: "hidden",
		},
		{
			name:     "null byte",
			input:    "test\x00.txt",
			expected: "test.txt",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "unnamed",
		},
		{
			name:     "only dots",
			input:    "...",
			expected: "unnamed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitizeFilename_Length(t *testing.T) {
	// Create a filename that exceeds 255 characters
	longName := ""
	for i := 0; i < 300; i++ {
		longName += "a"
	}

	result := SanitizeFilename(longName)
	if len(result) > 255 {
		t.Errorf("SanitizeFilename returned name longer than 255 chars: %d", len(result))
	}
}

func TestIsAllowedExtension(t *testing.T) {
	allowed := []string{".go", ".js", ".ts"}

	tests := []struct {
		path     string
		allowed  []string
		expected bool
	}{
		{"main.go", allowed, true},
		{"app.js", allowed, true},
		{"component.ts", allowed, true},
		{"Component.tsx", allowed, false},
		{"README.md", allowed, false},
		{"any.txt", nil, true},     // nil means all allowed
		{"MAIN.GO", allowed, true}, // case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := IsAllowedExtension(tt.path, tt.allowed)
			if result != tt.expected {
				t.Errorf("IsAllowedExtension(%q, %v) = %v, want %v", tt.path, tt.allowed, result, tt.expected)
			}
		})
	}
}

func TestValidatePath_Exists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pathguard-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create a file that exists
	existingFile := filepath.Join(tmpDir, "exists.txt")
	if err := os.WriteFile(existingFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	validator, err := NewValidator(tmpDir, DefaultOptions())
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	// Existing file should pass
	_, err = validator.ValidatePathExists("exists.txt")
	if err != nil {
		t.Errorf("expected existing file to pass, got error: %v", err)
	}

	// Non-existing file should fail
	_, err = validator.ValidatePathExists("notexists.txt")
	if err == nil {
		t.Error("expected error for non-existing file")
	}
}

func TestIsSafePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pathguard-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tests := []struct {
		path     string
		expected bool
	}{
		{"test.txt", true},
		{"../etc/passwd", false},
		{"subdir/file.txt", true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := IsSafePath(tmpDir, tt.path)
			if result != tt.expected {
				t.Errorf("IsSafePath(base, %q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}
