package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	ignore "github.com/sabhiram/go-gitignore"
)

var (
	AllowExts = []string{".ts", ".tsx", ".js", ".jsx", ".prisma", ".json", ".css", ".html", ".md", ".env", ".yml", ".yaml", ".go", ".py", ".rs", ".sh", ".txt"}
)

func ScanFiles(root string) ([]string, error) {
	var files []string

	// Try to load .vector-ignore first, then .gitignore
	var ignorer *ignore.GitIgnore
	if _, err := os.Stat(filepath.Join(root, ".vector-ignore")); err == nil {
		ignorer, _ = ignore.CompileIgnoreFile(filepath.Join(root, ".vector-ignore"))
	} else if _, err := os.Stat(filepath.Join(root, ".gitignore")); err == nil {
		ignorer, _ = ignore.CompileIgnoreFile(filepath.Join(root, ".gitignore"))
	}

	// Default hardcoded exclusions if no ignore file or as fallback safety
	defaultExcludes := []string{".git", "node_modules", ".vector-db", "dist", "build"}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(root, path)
		if relPath == "." {
			return nil
		}

		// Always exclude default dirs
		if info.IsDir() {
			for _, d := range defaultExcludes {
				if info.Name() == d {
					return filepath.SkipDir
				}
			}
		}

		// Check against ignore rules
		if ignorer != nil && ignorer.MatchesPath(relPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		ext := filepath.Ext(path)
		allowed := false
		for _, a := range AllowExts {
			if ext == a {
				allowed = true
				break
			}
		}

		if allowed {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func GetHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
