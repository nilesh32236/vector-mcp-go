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
			if IsIgnoredDir(info.Name()) {
				return filepath.SkipDir
			}
		}

		// Always exclude heavy files and lockfiles
		if !info.IsDir() {
			if IsIgnoredFile(info.Name()) {
				return nil
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

func IsIgnoredDir(name string) bool {
	ignored := []string{
		"node_modules", ".git", ".next", ".turbo", "dist",
		"build", "generated", "coverage", "out", "vendor", ".vector-db", ".data",
	}
	for _, d := range ignored {
		if name == d {
			return true
		}
	}
	return false
}

func IsIgnoredFile(name string) bool {
	ignoredExact := []string{
		"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.sum",
	}
	for _, f := range ignoredExact {
		if name == f {
			return true
		}
	}

	ignoredSuffixes := []string{
		".map", ".min.js", ".svg",
	}
	for _, s := range ignoredSuffixes {
		if len(name) >= len(s) && name[len(name)-len(s):] == s {
			return true
		}
	}

	return false
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
