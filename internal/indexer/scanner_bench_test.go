package indexer

import (
	"testing"
)

func BenchmarkIsIgnoredFile(b *testing.B) {
	filenames := []string{
		"main.go",
		"package-lock.json",
		"utils.min.js",
		"IMAGE.PNG",
		"README.md",
		"very-long-filename-that-is-not-ignored.go",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, name := range filenames {
			IsIgnoredFile(name)
		}
	}
}
