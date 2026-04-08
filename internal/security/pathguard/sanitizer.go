// Package pathguard provides path validation and sanitization utilities
// to prevent directory traversal attacks and ensure file operations remain
// within designated project boundaries.
package pathguard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrPathTraversal indicates an attempt to escape the base directory
	ErrPathTraversal = errors.New("path traversal attempt detected")
	// ErrSymlinkDenied indicates symlinks are not allowed
	ErrSymlinkDenied = errors.New("symlinks are not allowed")
	// ErrPathTooDeep indicates the path exceeds maximum depth
	ErrPathTooDeep = errors.New("path exceeds maximum depth")
	// ErrInvalidPath indicates the path is invalid
	ErrInvalidPath = errors.New("invalid path")
)

// Options configures path validation behavior
type Options struct {
	// AllowSymlinks permits symbolic links within the base path
	AllowSymlinks bool
	// MaxPathDepth limits the number of path components (0 = unlimited)
	MaxPathDepth int
	// AllowAbsolute permits absolute paths that resolve within base path
	AllowAbsolute bool
}

// DefaultOptions returns the default validation options (restrictive)
func DefaultOptions() Options {
	return Options{
		AllowSymlinks: false,
		MaxPathDepth:  20,
		AllowAbsolute: true,
	}
}

// Validator handles path validation with configurable options
type Validator struct {
	basePath string
	options  Options
}

// NewValidator creates a path validator for the given base directory
func NewValidator(basePath string, opts Options) (*Validator, error) {
	// Resolve the base path to its absolute form
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve base path: %w", err)
	}

	// Ensure base path exists
	info, err := os.Stat(absBase)
	if err != nil {
		return nil, fmt.Errorf("base path does not exist: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("base path is not a directory")
	}

	return &Validator{
		basePath: absBase,
		options:  opts,
	}, nil
}

// ValidatePath validates and resolves a target path, ensuring it remains
// within the configured base directory. Returns the resolved absolute path
// or an error if validation fails.
func (v *Validator) ValidatePath(targetPath string) (string, error) {
	// Handle empty path
	if targetPath == "" {
		return "", ErrInvalidPath
	}

	// Clean the path to remove any ./ or ../ components
	cleanPath := filepath.Clean(targetPath)

	// Build the full path
	var fullPath string
	if filepath.IsAbs(cleanPath) {
		if !v.options.AllowAbsolute {
			return "", ErrPathTraversal
		}
		fullPath = cleanPath
	} else {
		fullPath = filepath.Join(v.basePath, cleanPath)
	}

	// Resolve symlinks if not allowed
	if !v.options.AllowSymlinks {
		// Check for symlinks in the path components
		if err := v.checkSymlinks(fullPath); err != nil {
			return "", err
		}
	}

	// Get the absolute resolved path
	resolvedPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// Evaluate any remaining symlinks for final resolution
	evaluatedPath, err := filepath.EvalSymlinks(resolvedPath)
	if err != nil {
		// Path may not exist yet, which is okay for creation operations
		// Just use the resolved path
		evaluatedPath = resolvedPath
	}

	// Ensure the resolved path is within the base directory
	if !v.isWithinBase(evaluatedPath) {
		return "", ErrPathTraversal
	}

	// Check path depth
	if v.options.MaxPathDepth > 0 {
		if err := v.checkDepth(evaluatedPath); err != nil {
			return "", err
		}
	}

	return evaluatedPath, nil
}

// ValidatePathExists validates a path and ensures it exists
func (v *Validator) ValidatePathExists(targetPath string) (string, error) {
	resolved, err := v.ValidatePath(targetPath)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(resolved); err != nil {
		return "", fmt.Errorf("path does not exist: %w", err)
	}

	return resolved, nil
}

// isWithinBase checks if a path is within the base directory
func (v *Validator) isWithinBase(path string) bool {
	// Ensure both paths are normalized for comparison
	absPath := filepath.Clean(path)
	absBase := filepath.Clean(v.basePath)

	// Add separator to prevent partial matches (e.g., /app matching /app2)
	if !strings.HasSuffix(absBase, string(filepath.Separator)) {
		absBase += string(filepath.Separator)
	}
	if !strings.HasSuffix(absPath, string(filepath.Separator)) {
		absPath += string(filepath.Separator)
	}

	// Check if the base path itself is the target
	if filepath.Clean(path) == filepath.Clean(v.basePath) {
		return true
	}

	return strings.HasPrefix(absPath, absBase)
}

// checkSymlinks verifies no symlinks exist in the path components
func (v *Validator) checkSymlinks(path string) error {
	// Check each component of the path
	parts := strings.Split(path, string(filepath.Separator))
	current := ""

	for _, part := range parts {
		if part == "" {
			continue
		}

		current = filepath.Join(current, part)
		if current == "" {
			continue
		}

		info, err := os.Lstat(current)
		if err != nil {
			// Path doesn't exist yet, which is fine
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("failed to stat path: %w", err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return ErrSymlinkDenied
		}
	}

	return nil
}

// checkDepth verifies the path doesn't exceed maximum depth
func (v *Validator) checkDepth(path string) error {
	rel, err := filepath.Rel(v.basePath, path)
	if err != nil {
		return err
	}

	// Count path components
	parts := strings.Split(rel, string(filepath.Separator))
	// Filter out empty parts
	count := 0
	for _, p := range parts {
		if p != "" && p != "." {
			count++
		}
	}

	if count > v.options.MaxPathDepth {
		return ErrPathTooDeep
	}

	return nil
}

// BasePath returns the configured base path
func (v *Validator) BasePath() string {
	return v.basePath
}

// SanitizeFilename removes dangerous characters from a filename
// and ensures it doesn't contain path separators
func SanitizeFilename(name string) string {
	// Remove any path separators
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")

	// Remove null bytes
	name = strings.ReplaceAll(name, "\x00", "")

	// Remove leading dots (hidden files on Unix)
	for strings.HasPrefix(name, ".") {
		name = strings.TrimPrefix(name, ".")
	}

	// Limit length
	if len(name) > 255 {
		name = name[:255]
	}

	// Ensure name is not empty
	if name == "" {
		name = "unnamed"
	}

	return name
}

// IsAllowedExtension checks if a file has an allowed extension
func IsAllowedExtension(path string, allowed []string) bool {
	if len(allowed) == 0 {
		return true // No restrictions if no extensions specified
	}

	ext := strings.ToLower(filepath.Ext(path))
	for _, a := range allowed {
		if strings.ToLower(a) == ext {
			return true
		}
	}
	return false
}

// ValidatePath is a convenience function that creates a validator with
// default options and validates the path
func ValidatePath(basePath, targetPath string) (string, error) {
	validator, err := NewValidator(basePath, DefaultOptions())
	if err != nil {
		return "", err
	}
	return validator.ValidatePath(targetPath)
}

// IsSafePath performs a quick check if a path is safe without creating a validator
func IsSafePath(basePath, targetPath string) bool {
	result, err := ValidatePath(basePath, targetPath)
	return err == nil && result != ""
}
