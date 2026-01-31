package builder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Builder handles template rendering and file output.
type Builder struct {
	templates   map[string]*template.Template
	templateDir string
	outputDir   string
	funcMap     template.FuncMap
	mu          sync.RWMutex
}

// Config for Builder.
type Config struct {
	// TemplateDir is the directory containing templates
	TemplateDir string

	// OutputDir is where rendered files are written
	OutputDir string

	// FuncMap provides custom template functions
	FuncMap template.FuncMap
}

// New creates a new Builder.
func New(cfg Config) (*Builder, error) {
	b := &Builder{
		templates:   make(map[string]*template.Template),
		templateDir: cfg.TemplateDir,
		outputDir:   cfg.OutputDir,
		funcMap:     cfg.FuncMap,
	}

	if b.funcMap == nil {
		b.funcMap = defaultFuncMap()
	}

	if cfg.TemplateDir != "" {
		if err := b.LoadTemplates(); err != nil {
			return nil, err
		}
	}

	return b, nil
}

// LoadTemplates loads all templates from the template directory.
func (b *Builder) LoadTemplates() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.templates = make(map[string]*template.Template)

	if b.templateDir == "" {
		return nil
	}

	return filepath.WalkDir(b.templateDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		ext := filepath.Ext(path)
		if ext != ".html" && ext != ".tmpl" && ext != ".gohtml" {
			return nil
		}

		relPath, err := filepath.Rel(b.templateDir, path)
		if err != nil {
			return err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		tmpl, err := template.New(relPath).Funcs(b.funcMap).Parse(string(content))
		if err != nil {
			return fmt.Errorf("parse template %s: %w", relPath, err)
		}

		b.templates[relPath] = tmpl
		// Also register without extension for convenience
		nameNoExt := strings.TrimSuffix(relPath, ext)
		b.templates[nameNoExt] = tmpl

		return nil
	})
}

// RegisterTemplate adds a template from string content.
func (b *Builder) RegisterTemplate(name, content string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	tmpl, err := template.New(name).Funcs(b.funcMap).Parse(content)
	if err != nil {
		return err
	}

	b.templates[name] = tmpl
	return nil
}

// Render renders a template with data and returns the content.
func (b *Builder) Render(ctx context.Context, templateName string, data any) ([]byte, error) {
	b.mu.RLock()
	tmpl, ok := b.templates[templateName]
	b.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("template not found: %s", templateName)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template %s: %w", templateName, err)
	}

	return buf.Bytes(), nil
}

// Build renders a template and writes to output file.
func (b *Builder) Build(ctx context.Context, templateName, outputPath string, data any) (*BuildResult, error) {
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
	hashStr := hex.EncodeToString(hash[:8]) // First 8 bytes for short hash

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
func (b *Builder) Delete(ctx context.Context, outputPath string) error {
	fullPath := filepath.Join(b.outputDir, outputPath)
	return os.Remove(fullPath)
}

// Exists checks if an output file exists.
func (b *Builder) Exists(ctx context.Context, outputPath string) (bool, error) {
	fullPath := filepath.Join(b.outputDir, outputPath)
	_, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// SetFuncMap updates the template function map.
func (b *Builder) SetFuncMap(funcMap template.FuncMap) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Merge with existing
	for k, v := range funcMap {
		b.funcMap[k] = v
	}
}

// OutputDir returns the output directory path.
func (b *Builder) OutputDir() string {
	return b.outputDir
}

// TemplateDir returns the template directory path.
func (b *Builder) TemplateDir() string {
	return b.templateDir
}

// ListTemplates returns all registered template names.
func (b *Builder) ListTemplates() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.templates))
	for name := range b.templates {
		names = append(names, name)
	}
	return names
}

// BuildResult contains information about a build operation.
type BuildResult struct {
	Path        string
	FullPath    string
	ContentHash string
	Changed     bool
	Size        int
}

func defaultFuncMap() template.FuncMap {
	return template.FuncMap{
		"safe": func(s string) template.HTML {
			return template.HTML(s)
		},
		"join": strings.Join,
		"split": strings.Split,
		"contains": strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"title": strings.Title,
		"trim": strings.TrimSpace,
		"default": func(def, val any) any {
			if val == nil || val == "" {
				return def
			}
			return val
		},
		"dict": func(pairs ...any) map[string]any {
			m := make(map[string]any)
			for i := 0; i < len(pairs)-1; i += 2 {
				key, _ := pairs[i].(string)
				m[key] = pairs[i+1]
			}
			return m
		},
		"list": func(items ...any) []any {
			return items
		},
		"first": func(items []any) any {
			if len(items) > 0 {
				return items[0]
			}
			return nil
		},
		"last": func(items []any) any {
			if len(items) > 0 {
				return items[len(items)-1]
			}
			return nil
		},
	}
}
