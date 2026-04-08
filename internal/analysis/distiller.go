package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/nilesh32236/vector-mcp-go/internal/db"
	"github.com/nilesh32236/vector-mcp-go/internal/indexer"
)

// IndexerStore is an interface that matches the one defined in mcp.
// We define it locally here or import it to avoid cycles if needed.
type IndexerStore interface {
	GetByPrefix(ctx context.Context, prefix string, projectID string) ([]db.Record, error)
	Insert(ctx context.Context, records []db.Record) error
}

// Distiller provides methods to generate high-level semantic summaries of the codebase.
const MaxDistillDepth = 5

type Distiller struct {
	Store    IndexerStore
	Embedder indexer.Embedder
	Logger   *slog.Logger
}

// NewDistiller creates a new Distiller instance.
func NewDistiller(store IndexerStore, embedder indexer.Embedder, logger *slog.Logger) *Distiller {
	return &Distiller{
		Store:    store,
		Embedder: embedder,
		Logger:   logger,
	}
}

// DistillPackagePurpose analyzes all files in a package directory and generates a high-level summary.
// It stores this summary back in the vector DB with a high 'priority' and 'category=distilled'.
func (d *Distiller) DistillPackagePurpose(ctx context.Context, projectRoot string, pkgPath string) (string, error) {
	d.Logger.Info("Distilling package purpose (Structural)", "package", pkgPath)

	records, err := d.Store.GetByPrefix(ctx, pkgPath, projectRoot)
	if err != nil {
		return "", fmt.Errorf("failed to fetch records for distillation: %w", err)
	}

	if len(records) == 0 {
		return "Empty package", nil
	}

	// 1. Structural Aggregation
	exportedAPI := make(map[string]string) // name -> type/details
	internalDetails := make(map[string]string)
	dependencies := make(map[string]bool)
	typeCounts := make(map[string]int)

	for _, r := range records {
		name := r.Metadata["name"]
		if name == "" {
			continue
		}

		// Relationships (Imports)
		var rels []string
		if err := json.Unmarshal([]byte(r.Metadata["relationships"]), &rels); err == nil {
			for _, rel := range rels {
				dependencies[rel] = true
			}
		}

		// Structural Metadata (Fields/Methods)
		var meta map[string]string
		_ = json.Unmarshal([]byte(r.Metadata["structural_metadata"]), &meta)

		t := r.Metadata["type"]
		typeCounts[t]++

		isExported := false
		if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
			isExported = true
		} else if t == "class" || t == "interface" { // Non-Go languages heuristic
			isExported = true
		}

		// Build entity description
		var desc strings.Builder
		fmt.Fprintf(&desc, "%s", t)
		if len(meta) > 0 {
			desc.WriteString(" {")
			var keys []string
			for k := range meta {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for i, k := range keys {
				if i > MaxDistillDepth {
					desc.WriteString("...")
					break
				}
				fmt.Fprintf(&desc, "%s", strings.TrimPrefix(k, "field:"))
				if i < len(keys)-1 && i < 5 {
					desc.WriteString(", ")
				}
			}
			desc.WriteString("}")
		}

		if isExported {
			exportedAPI[name] = desc.String()
		} else {
			internalDetails[name] = desc.String()
		}
	}

	// 2. Generate Manifest
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Package Architectural Manifest: `%s`\n\n", pkgPath)

	sb.WriteString("## 🚀 Exported API\n")
	if len(exportedAPI) == 0 {
		sb.WriteString("- No exported symbols found.\n")
	} else {
		var names []string
		for n := range exportedAPI {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&sb, "- **`%s`**: %s\n", n, exportedAPI[n])
		}
	}

	sb.WriteString("\n## 🧱 Internal Components\n")
	fmt.Fprintf(&sb, "Contains %d total entities across ", len(exportedAPI)+len(internalDetails))
	var types []string
	for t := range typeCounts {
		types = append(types, t)
	}
	sort.Strings(types)
	for i, t := range types {
		fmt.Fprintf(&sb, "%d %ss", typeCounts[t], t)
		if i < len(types)-1 {
			sb.WriteString(", ")
		}
	}
	sb.WriteString(".\n")

	sb.WriteString("\n## 🔗 Dependencies\n")
	if len(dependencies) == 0 {
		sb.WriteString("- No external dependencies.\n")
	} else {
		var deps []string
		for d := range dependencies {
			deps = append(deps, d)
		}
		sort.Strings(deps)
		for _, d := range deps {
			fmt.Fprintf(&sb, "- `%s`\n", d)
		}
	}

	summary := sb.String()

	// 3. Store the distillation with Priority 2.0
	dummyEmb, err := d.Embedder.Embed(ctx, summary)
	if err != nil {
		return "", fmt.Errorf("failed to embed distillation: %w", err)
	}

	distilledID := fmt.Sprintf("distilled:%s:%s", projectRoot, pkgPath)
	distilledRecord := db.Record{
		ID:        distilledID,
		Content:   summary,
		Embedding: dummyEmb,
		Metadata: map[string]string{
			"path":       pkgPath,
			"project_id": projectRoot,
			"category":   "distilled",
			"priority":   "2.0",
			"type":       "package_summary",
		},
	}

	if err := d.Store.Insert(ctx, []db.Record{distilledRecord}); err != nil {
		return "", fmt.Errorf("failed to save distillation: %w", err)
	}

	return summary, nil
}
