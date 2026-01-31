package builder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/flosch/pongo2/v6"
)

// PongoBuilder renders templates using pongo2 (Django/Jinja2 syntax).
type PongoBuilder struct {
	templateSet *pongo2.TemplateSet
	templates   map[string]*pongo2.Template
	templateDir string
	outputDir   string
	mu          sync.RWMutex
}

// PongoConfig for PongoBuilder.
type PongoConfig struct {
	// TemplateDir is the directory containing templates
	TemplateDir string

	// OutputDir is where rendered files are written
	OutputDir string
}

// NewPongoBuilder creates a new pongo2-based builder.
func NewPongoBuilder(cfg PongoConfig) (*PongoBuilder, error) {
	loader := pongo2.MustNewLocalFileSystemLoader(cfg.TemplateDir)
	templateSet := pongo2.NewSet("hotstatic", loader)

	b := &PongoBuilder{
		templateSet: templateSet,
		templates:   make(map[string]*pongo2.Template),
		templateDir: cfg.TemplateDir,
		outputDir:   cfg.OutputDir,
	}

	if cfg.TemplateDir != "" {
		if err := b.LoadTemplates(); err != nil {
			return nil, err
		}
	}

	return b, nil
}

// LoadTemplates loads all templates from the template directory.
func (b *PongoBuilder) LoadTemplates() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.templates = make(map[string]*pongo2.Template)

	return filepath.WalkDir(b.templateDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		ext := filepath.Ext(path)
		if ext != ".html" && ext != ".htm" && ext != ".jinja2" && ext != ".j2" {
			return nil
		}

		relPath, err := filepath.Rel(b.templateDir, path)
		if err != nil {
			return err
		}

		tmpl, err := b.templateSet.FromFile(relPath)
		if err != nil {
			return fmt.Errorf("parse template %s: %w", relPath, err)
		}

		b.templates[relPath] = tmpl

		return nil
	})
}

// RegisterTemplate adds a template from string content.
func (b *PongoBuilder) RegisterTemplate(name, content string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	tmpl, err := b.templateSet.FromString(content)
	if err != nil {
		return err
	}

	b.templates[name] = tmpl
	return nil
}

// Render renders a template with data and returns the content.
func (b *PongoBuilder) Render(ctx context.Context, templateName string, data map[string]any) ([]byte, error) {
	b.mu.RLock()
	tmpl, ok := b.templates[templateName]
	b.mu.RUnlock()

	if !ok {
		// Try to load on-demand
		var err error
		tmpl, err = b.templateSet.FromFile(templateName)
		if err != nil {
			return nil, fmt.Errorf("template not found: %s", templateName)
		}
		b.mu.Lock()
		b.templates[templateName] = tmpl
		b.mu.Unlock()
	}

	result, err := tmpl.ExecuteBytes(pongo2.Context(data))
	if err != nil {
		return nil, fmt.Errorf("render template %s: %w", templateName, err)
	}

	return result, nil
}

// Build renders a template and writes to output file.
func (b *PongoBuilder) Build(ctx context.Context, templateName, outputPath string, data map[string]any) (*BuildResult, error) {
	content, err := b.Render(ctx, templateName, data)
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
func (b *PongoBuilder) Delete(ctx context.Context, outputPath string) error {
	fullPath := filepath.Join(b.outputDir, outputPath)
	return os.Remove(fullPath)
}

// OutputDir returns the output directory.
func (b *PongoBuilder) OutputDir() string {
	return b.outputDir
}

// TemplateDir returns the template directory.
func (b *PongoBuilder) TemplateDir() string {
	return b.templateDir
}

// ListTemplates returns registered template names.
func (b *PongoBuilder) ListTemplates() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.templates))
	for name := range b.templates {
		names = append(names, name)
	}
	return names
}

// AddFilter adds a custom filter function.
// Usage in template: {{ value|myfilter }}
func (b *PongoBuilder) AddFilter(name string, fn pongo2.FilterFunction) error {
	return pongo2.RegisterFilter(name, fn)
}

// AddGlobal adds a global variable available in all templates.
func (b *PongoBuilder) AddGlobal(name string, value any) {
	pongo2.Globals[name] = value
}

// TemplateSet returns the underlying pongo2 template set.
func (b *PongoBuilder) TemplateSet() *pongo2.TemplateSet {
	return b.templateSet
}
