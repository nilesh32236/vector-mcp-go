package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

var (
	ExcludeDirs = []string{"node_modules", ".git", ".turbo", ".next", "dist", "build", ".vector-db", "image", "video", "public"}
	AllowExts   = []string{".ts", ".tsx", ".js", ".jsx", ".prisma", ".json", ".css", ".html", ".md", ".env", ".yml", ".yaml"}
)

func ScanFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			for _, d := range ExcludeDirs {
				if info.Name() == d {
					return filepath.SkipDir
				}
			}
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
