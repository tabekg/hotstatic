package builder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/a-h/templ"
)

// TemplBuilder renders templ components to static files.
type TemplBuilder struct {
	outputDir  string
	components map[string]TemplComponentFunc
	mu         sync.RWMutex
}

// TemplComponentFunc creates a templ component from data.
type TemplComponentFunc func(data any) templ.Component

// TemplConfig for TemplBuilder.
type TemplConfig struct {
	OutputDir string
}

// NewTemplBuilder creates a new templ-based builder.
func NewTemplBuilder(cfg TemplConfig) *TemplBuilder {
	return &TemplBuilder{
		outputDir:  cfg.OutputDir,
		components: make(map[string]TemplComponentFunc),
	}
}

// Register adds a component factory function.
func (b *TemplBuilder) Register(name string, factory TemplComponentFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.components[name] = factory
}

// Render renders a component to bytes.
func (b *TemplBuilder) Render(ctx context.Context, name string, data any) ([]byte, error) {
	b.mu.RLock()
	factory, ok := b.components[name]
	b.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("component not found: %s", name)
	}

	component := factory(data)

	var buf bytes.Buffer
	if err := component.Render(ctx, &buf); err != nil {
		return nil, fmt.Errorf("render component %s: %w", name, err)
	}

	return buf.Bytes(), nil
}

// Build renders and writes to file.
func (b *TemplBuilder) Build(ctx context.Context, name, outputPath string, data any) (*BuildResult, error) {
	content, err := b.Render(ctx, name, data)
	if err != nil {
		return nil, err
	}

	fullPath := filepath.Join(b.outputDir, outputPath)

	// Ensure directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", dir, err)
	}

	// Calculate content hash
	hash := sha256.Sum256(content)
	hashStr := hex.EncodeToString(hash[:8])

	// Check if content changed
	changed := true
	if existing, err := os.ReadFile(fullPath); err == nil {
		existingHash := sha256.Sum256(existing)
		changed = hash != existingHash
	}

	// Write file
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		return nil, fmt.Errorf("write file %s: %w", fullPath, err)
	}

	return &BuildResult{
		Path:        outputPath,
		FullPath:    fullPath,
		ContentHash: hashStr,
		Changed:     changed,
		Size:        len(content),
	}, nil
}

// Delete removes an output file.
func (b *TemplBuilder) Delete(ctx context.Context, outputPath string) error {
	fullPath := filepath.Join(b.outputDir, outputPath)
	return os.Remove(fullPath)
}

// OutputDir returns the output directory.
func (b *TemplBuilder) OutputDir() string {
	return b.outputDir
}

// ListComponents returns registered component names.
func (b *TemplBuilder) ListComponents() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.components))
	for name := range b.components {
		names = append(names, name)
	}
	return names
}

// RenderComponent renders a templ.Component directly.
func RenderComponent(ctx context.Context, component templ.Component) ([]byte, error) {
	var buf bytes.Buffer
	if err := component.Render(ctx, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteComponent renders and writes a component to file.
func WriteComponent(ctx context.Context, component templ.Component, outputPath string) (*BuildResult, error) {
	content, err := RenderComponent(ctx, component)
	if err != nil {
		return nil, err
	}

	// Ensure directory exists
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", dir, err)
	}

	// Calculate content hash
	hash := sha256.Sum256(content)
	hashStr := hex.EncodeToString(hash[:8])

	// Check if content changed
	changed := true
	if existing, err := os.ReadFile(outputPath); err == nil {
		existingHash := sha256.Sum256(existing)
		changed = hash != existingHash
	}

	// Write file
	if err := os.WriteFile(outputPath, content, 0644); err != nil {
		return nil, fmt.Errorf("write file %s: %w", outputPath, err)
	}

	return &BuildResult{
		Path:        outputPath,
		FullPath:    outputPath,
		ContentHash: hashStr,
		Changed:     changed,
		Size:        len(content),
	}, nil
}
