package hotstatic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/tabekg/hotstatic/pkg/builder"
	"github.com/tabekg/hotstatic/pkg/registry"
)

// TemplPageConfig defines a page that uses templ components.
type TemplPageConfig struct {
	// Name identifies this page config
	Name string

	// PathPattern with placeholders (e.g., "/products/{id}.html")
	PathPattern string

	// Component factory creates the templ component from data
	Component func(ctx context.Context, params map[string]string, data any) templ.Component

	// Priority for rebuild queue (default: 0)
	Priority int
}

// TemplHotStatic extends HotStatic with templ support.
type TemplHotStatic struct {
	*HotStatic
	templBuilder *builder.TemplBuilder
	templConfigs map[string]*TemplPageConfig
	mu           sync.RWMutex
}

// NewWithTempl creates HotStatic with templ support.
func NewWithTempl(cfg Config) (*TemplHotStatic, error) {
	hs, err := New(cfg)
	if err != nil {
		return nil, err
	}

	return &TemplHotStatic{
		HotStatic:    hs,
		templBuilder: builder.NewTemplBuilder(builder.TemplConfig{OutputDir: cfg.OutputDir}),
		templConfigs: make(map[string]*TemplPageConfig),
	}, nil
}

// RegisterTemplPage registers a templ-based page configuration.
func (ths *TemplHotStatic) RegisterTemplPage(name string, cfg TemplPageConfig) {
	ths.mu.Lock()
	defer ths.mu.Unlock()

	cfg.Name = name
	ths.templConfigs[name] = &cfg
}

// GenerateTemplPage generates a single page using templ component.
func (ths *TemplHotStatic) GenerateTemplPage(ctx context.Context, configName string, page Page, data any) error {
	ths.mu.RLock()
	cfg, ok := ths.templConfigs[configName]
	ths.mu.RUnlock()

	if !ok {
		return fmt.Errorf("templ page config not found: %s", configName)
	}

	fullPath := ths.config.OutputDir + page.Path

	// Create component
	component := cfg.Component(ctx, page.Params, data)

	// Render and write
	result, err := builder.WriteComponent(ctx, component, fullPath)
	if err != nil {
		return fmt.Errorf("templ build %s: %w", page.Path, err)
	}

	// Register page dependencies
	err = ths.registry.AddDependencies(ctx, registry.PageMeta{
		Path:         page.Path,
		Template:     "templ:" + configName,
		Params:       page.Params,
		Dependencies: page.Dependencies,
		LastBuilt:    time.Now(),
		ContentHash:  result.ContentHash,
	})
	if err != nil {
		return fmt.Errorf("add dependencies %s: %w", page.Path, err)
	}

	return nil
}

// BuildTemplPage rebuilds a single templ page with the given data.
func (ths *TemplHotStatic) BuildTemplPage(ctx context.Context, pagePath string, data any) (*BuildResult, error) {
	meta, err := ths.registry.GetPage(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, fmt.Errorf("page not found: %s", pagePath)
	}

	// Check if it's a templ page
	if !strings.HasPrefix(meta.Template, "templ:") {
		// Fallback to regular build
		if payload, ok := data.(map[string]any); ok {
			return ths.Build(ctx, pagePath, payload)
		}
		return nil, fmt.Errorf("non-templ page requires map[string]any payload")
	}

	configName := strings.TrimPrefix(meta.Template, "templ:")

	ths.mu.RLock()
	cfg, ok := ths.templConfigs[configName]
	ths.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("templ config not found: %s", configName)
	}

	start := time.Now()

	// Create and render component
	fullPath := ths.config.OutputDir + meta.Path
	component := cfg.Component(ctx, meta.Params, data)

	result, err := builder.WriteComponent(ctx, component, fullPath)
	if err != nil {
		return &BuildResult{
			Page:      Page{Path: meta.Path},
			Success:   false,
			Error:     err.Error(),
			Duration:  time.Since(start),
			Timestamp: time.Now(),
		}, err
	}

	// Update registry
	ths.registry.UpdateLastBuilt(ctx, meta.Path, time.Now(), result.ContentHash)

	return &BuildResult{
		Page: Page{
			Path:         meta.Path,
			Template:     meta.Template,
			Dependencies: meta.Dependencies,
			Params:       meta.Params,
			LastBuilt:    time.Now(),
			ContentHash:  result.ContentHash,
		},
		Success:   true,
		Duration:  time.Since(start),
		Changed:   result.Changed,
		Timestamp: time.Now(),
	}, nil
}

// TemplBuilder returns the underlying templ builder.
func (ths *TemplHotStatic) TemplBuilder() *builder.TemplBuilder {
	return ths.templBuilder
}
