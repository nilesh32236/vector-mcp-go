// Package db provides data persistence and graph representation for code entities.
package db

import (
	"encoding/json"
	"strings"
	"sync"
)

// EntityNode represents a code entity in the knowledge graph.
type EntityNode struct {
	Name      string
	Type      string
	Path      string
	Docstring string
	Metadata  map[string]string
}

// KnowledgeGraph manages code relationships for high-order reasoning.
type KnowledgeGraph struct {
	nodes map[string]EntityNode      // ID -> Node
	edges map[string]map[string]bool // ID -> Set of related IDs (calls/uses)
	impls map[string][]string        // InterfaceName -> List of Struct IDs
	mu    sync.RWMutex
}

// NewKnowledgeGraph creates a new KnowledgeGraph instance.
func NewKnowledgeGraph() *KnowledgeGraph {
	return &KnowledgeGraph{
		nodes: make(map[string]EntityNode),
		edges: make(map[string]map[string]bool),
		impls: make(map[string][]string),
	}
}

// Populate builds the graph from database records.
func (g *KnowledgeGraph) Populate(records []Record) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Clear existing data (could be optimized)
	g.nodes = make(map[string]EntityNode)
	g.edges = make(map[string]map[string]bool)
	g.impls = make(map[string][]string)

	interfaces := make(map[string]map[string]string) // Name -> Methods

	// 1. First pass: Collect all entities and their structural metadata
	for _, r := range records {
		name := r.Metadata["name"]
		if name == "" {
			continue
		}

		node := EntityNode{
			Name:      name,
			Type:      r.Metadata["type"],
			Path:      r.Metadata["path"],
			Docstring: r.Metadata["docstring"],
			Metadata:  make(map[string]string),
		}

		var structMeta map[string]string
		if sm, ok := r.Metadata["structural_metadata"]; ok {
			if err := json.Unmarshal([]byte(sm), &structMeta); err != nil {
				// Log or skip if metadata is corrupted
				continue
			}
		}
		node.Metadata = structMeta
		g.nodes[r.ID] = node

		// If it's an interface, collect its methods for implementation matching
		if node.Type == "interface_type" || node.Type == "interface" {
			methods := make(map[string]string)
			for k, v := range structMeta {
				if strings.HasPrefix(k, "method:") {
					methods[strings.TrimPrefix(k, "method:")] = v
				}
			}
			interfaces[name] = methods
		}
	}

	// 2. Second pass: Detect implementations and build edge graph
	for id, node := range g.nodes {
		// Implementation detection for structs
		if node.Type == "struct_type" || node.Type == "struct" || node.Type == "class" {
			for interfaceName, interfaceMethods := range interfaces {
				isImpl := true
				for method := range interfaceMethods {
					if _, hasMethod := node.Metadata["method:"+method]; !hasMethod {
						isImpl = false
						break
					}
				}
				if isImpl && len(interfaceMethods) > 0 {
					g.impls[interfaceName] = append(g.impls[interfaceName], id)
				}
			}
		}

		// Edge generation from calls and relationships
		// (Already captured in metadata as 'calls' and 'relationships')
	}
}

// GetImplementations returns all struct IDs that implement the given interface name.
func (g *KnowledgeGraph) GetImplementations(interfaceName string) []EntityNode {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var results []EntityNode
	if ids, ok := g.impls[interfaceName]; ok {
		for _, id := range ids {
			results = append(results, g.nodes[id])
		}
	}
	return results
}

// FindUsage returns all nodes that use a specific field name.
func (g *KnowledgeGraph) FindUsage(fieldName string) []EntityNode {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var results []EntityNode
	for _, node := range g.nodes {
		if _, hasField := node.Metadata["field:"+fieldName]; hasField {
			results = append(results, node)
		}
	}
	return results
}

// GetNodeByID returns an entity node by its database record ID.
func (g *KnowledgeGraph) GetNodeByID(id string) (EntityNode, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
}

// SearchByName finds entities matching a simple name.
func (g *KnowledgeGraph) SearchByName(name string) []EntityNode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var results []EntityNode
	lowerName := strings.ToLower(name)
	for _, n := range g.nodes {
		if strings.Contains(strings.ToLower(n.Name), lowerName) {
			results = append(results, n)
		}
	}
	return results
}
