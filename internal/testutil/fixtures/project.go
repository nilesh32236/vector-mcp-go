// Package fixtures provides test fixtures and utilities for testing.
package fixtures

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectBuilder helps create temporary test project structures.
type ProjectBuilder struct {
	rootDir string
	files   map[string]string
	dirs    []string
}

// NewProjectBuilder creates a new project builder.
func NewProjectBuilder() *ProjectBuilder {
	return &ProjectBuilder{
		files: make(map[string]string),
	}
}

// WithDir adds a directory to be created.
func (b *ProjectBuilder) WithDir(path string) *ProjectBuilder {
	b.dirs = append(b.dirs, path)
	return b
}

// WithFile adds a file with content to be created.
func (b *ProjectBuilder) WithFile(path, content string) *ProjectBuilder {
	b.files[path] = content
	return b
}

// WithGoFile adds a Go file with the given content.
func (b *ProjectBuilder) WithGoFile(path, content string) *ProjectBuilder {
	if !strings.HasSuffix(path, ".go") {
		path += ".go"
	}
	return b.WithFile(path, content)
}

// WithTypeScriptFile adds a TypeScript file with the given content.
func (b *ProjectBuilder) WithTypeScriptFile(path, content string) *ProjectBuilder {
	if !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx") {
		path += ".ts"
	}
	return b.WithFile(path, content)
}

// WithMarkdownFile adds a Markdown file with the given content.
func (b *ProjectBuilder) WithMarkdownFile(path, content string) *ProjectBuilder {
	if !strings.HasSuffix(path, ".md") {
		path += ".md"
	}
	return b.WithFile(path, content)
}

// Build creates the project structure in a temporary directory.
func (b *ProjectBuilder) Build() (string, error) {
	rootDir, err := os.MkdirTemp("", "test-project-*")
	if err != nil {
		return "", err
	}
	b.rootDir = rootDir

	// Create directories
	for _, dir := range b.dirs {
		fullPath := filepath.Join(rootDir, dir)
		if err := os.MkdirAll(fullPath, 0755); err != nil {
			b.Cleanup()
			return "", err
		}
	}

	// Create files
	for path, content := range b.files {
		fullPath := filepath.Join(rootDir, path)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			b.Cleanup()
			return "", err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			b.Cleanup()
			return "", err
		}
	}

	return rootDir, nil
}

// Cleanup removes the temporary project directory.
func (b *ProjectBuilder) Cleanup() {
	if b.rootDir != "" {
		os.RemoveAll(b.rootDir)
	}
}

// Root returns the root directory path.
func (b *ProjectBuilder) Root() string {
	return b.rootDir
}

// SampleGoProject creates a sample Go project for testing.
func SampleGoProject() (string, error) {
	builder := NewProjectBuilder()

	// Main package
	builder.WithGoFile("main.go", `package main

import "fmt"

func main() {
	message := GetMessage()
	fmt.Println(message)
}
`)

	// Sample package
	builder.WithGoFile("pkg/message/message.go", `package message

// GetMessage returns a greeting message.
func GetMessage() string {
	return "Hello, World!"
}

// Calculate performs a calculation.
func Calculate(a, b int) int {
	return a + b
}
`)

	// Another package with dependencies
	builder.WithGoFile("pkg/calculator/operations.go", `package calculator

import "errors"

// Add adds two numbers.
func Add(a, b int) int {
	return a + b
}

// Subtract subtracts b from a.
func Subtract(a, b int) int {
	return a - b
}

// Divide divides a by b.
func Divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a / b, nil
}
`)

	// README
	builder.WithMarkdownFile("README.md", `# Sample Project

This is a sample project for testing.

## Features

- Feature 1: Something cool
- Feature 2: Another cool thing

## Usage

Run with: go run main.go
`)

	return builder.Build()
}

// SampleTypeScriptProject creates a sample TypeScript project for testing.
func SampleTypeScriptProject() (string, error) {
	builder := NewProjectBuilder()

	builder.WithTypeScriptFile("index.ts", `import { greet } from './utils/greet';

const message = greet('World');
console.log(message);
`)

	builder.WithTypeScriptFile("utils/greet.ts", `export function greet(name: string): string {
    return "Hello, " + name + "!";
}

export function farewell(name: string): string {
    return "Goodbye, " + name + "!";
}
`)

	builder.WithTypeScriptFile("utils/calculator.ts", `export interface Calculator {
    add(a: number, b: number): number;
    subtract(a: number, b: number): number;
}

export class BasicCalculator implements Calculator {
    add(a: number, b: number): number {
        return a + b;
    }

    subtract(a: number, b: number): number {
        return a - b;
    }
}
`)

	builder.WithFile("package.json", `{
    "name": "sample-typescript-project",
    "version": "1.0.0",
    "main": "index.ts",
    "dependencies": {}
}
`)

	return builder.Build()
}

// LargeProject creates a larger project for performance testing.
func LargeProject(fileCount int) (string, error) {
	builder := NewProjectBuilder()

	for i := 0; i < fileCount; i++ {
		content := generateGoFile(i)
		builder.WithGoFile(filepath.Join("pkg", "module", "file"+string(rune('0'+i%10))+".go"), content)
	}

	return builder.Build()
}

func generateGoFile(index int) string {
	return `package module

// Function` + string(rune('A'+index%26)) + ` is a sample function.
func Function` + string(rune('A'+index%26)) + `(input string) string {
	return input + " processed"
}
`
}
