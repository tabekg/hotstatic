package hotstatic

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// HotStatic is the main framework instance.
type HotStatic struct {
	config    Config
	templates map[string]*TemplateDef
	builder   Builder
	mu        sync.RWMutex
}

// Builder interface for template rendering.
type Builder interface {
	Build(ctx context.Context, templateFile, outputPath string, data map[string]any) error
}

// New creates a new HotStatic instance.
func New(cfg Config) *HotStatic {
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./dist"
	}
	if cfg.Logger == nil {
		cfg.Logger = &slogAdapter{slog.Default()}
	}

	return &HotStatic{
		config:    cfg,
		templates: make(map[string]*TemplateDef),
	}
}

// DefineTemplate registers a template definition.
func (hs *HotStatic) DefineTemplate(name string, def TemplateDef) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.templates[name] = &def
}

// SetBuilder sets the template builder.
func (hs *HotStatic) SetBuilder(builder Builder) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.builder = builder
}

// Build builds a single page.
func (hs *HotStatic) Build(ctx context.Context, template string, id string) error {
	start := time.Now()

	hs.mu.RLock()
	def, ok := hs.templates[template]
	builder := hs.builder
	hs.mu.RUnlock()

	if !ok {
		return fmt.Errorf("template not found: %s", template)
	}

	if builder == nil {
		return fmt.Errorf("no builder set")
	}

	if def.Load == nil {
		return fmt.Errorf("template %s has no Load function", template)
	}

	// Load page data
	pageData, err := def.Load(ctx, id)
	if err != nil {
		return fmt.Errorf("load data: %w", err)
	}

	if pageData == nil {
		// nil means skip (e.g., deleted or inactive entity)
		hs.config.Logger.Debug("skipped (no data)",
			"template", template,
			"id", id,
		)
		return nil
	}

	// Build page
	if err := builder.Build(ctx, def.File, pageData.Path, pageData.Data); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	hs.config.Logger.Debug("built page",
		"template", template,
		"id", id,
		"output", pageData.Path,
		"duration", time.Since(start),
	)

	return nil
}

// BuildAll builds all pages for all templates.
func (hs *HotStatic) BuildAll(ctx context.Context) error {
	start := time.Now()

	hs.mu.RLock()
	templates := make(map[string]*TemplateDef)
	for k, v := range hs.templates {
		templates[k] = v
	}
	hs.mu.RUnlock()

	var totalPages int

	for name, def := range templates {
		if def.LoadAll == nil {
			continue
		}

		ids, err := def.LoadAll(ctx)
		if err != nil {
			return fmt.Errorf("LoadAll for %s: %w", name, err)
		}

		hs.config.Logger.Info("building template",
			"template", name,
			"count", len(ids),
		)

		for _, id := range ids {
			if err := hs.Build(ctx, name, id); err != nil {
				hs.config.Logger.Error("build failed",
					"template", name,
					"id", id,
					"error", err.Error(),
				)
			} else {
				totalPages++
			}
		}
	}

	hs.config.Logger.Info("build complete",
		"pages", totalPages,
		"duration", time.Since(start),
	)

	return nil
}

// Delete removes a generated page by path.
func (hs *HotStatic) Delete(path string) error {
	fullPath := filepath.Join(hs.config.OutputDir, path)

	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	hs.config.Logger.Info("deleted page", "path", path)

	return nil
}

// GetTemplate returns a template definition by name.
func (hs *HotStatic) GetTemplate(name string) (*TemplateDef, bool) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	def, ok := hs.templates[name]
	return def, ok
}

// slogAdapter wraps slog.Logger to implement Logger interface.
type slogAdapter struct {
	*slog.Logger
}

func (s *slogAdapter) Debug(msg string, args ...any) {
	s.Logger.Debug(msg, args...)
}

func (s *slogAdapter) Info(msg string, args ...any) {
	s.Logger.Info(msg, args...)
}

func (s *slogAdapter) Warn(msg string, args ...any) {
	s.Logger.Warn(msg, args...)
}

func (s *slogAdapter) Error(msg string, args ...any) {
	s.Logger.Error(msg, args...)
}
