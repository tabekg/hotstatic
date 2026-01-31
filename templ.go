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

	// Component factory creates the templ component from loaded data
	Component func(ctx context.Context, params map[string]string, data any) templ.Component

	// DataLoader fetches data and returns subscriptions
	DataLoader func(ctx context.Context, params map[string]string) (any, []string, error)

	// Priority for rebuild queue (default: 0)
	Priority int

	// Condition optional filter - return false to skip page
	Condition func(params map[string]string) bool
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

// GenerateTemplPages generates pages using templ components.
func (ths *TemplHotStatic) GenerateTemplPages(ctx context.Context, configName string, ids []string) error {
	ths.mu.RLock()
	cfg, ok := ths.templConfigs[configName]
	ths.mu.RUnlock()

	if !ok {
		return fmt.Errorf("templ page config not found: %s", configName)
	}

	for _, id := range ids {
		params := extractParams(cfg.PathPattern, id)

		// Check condition
		if cfg.Condition != nil && !cfg.Condition(params) {
			continue
		}

		// Load data
		data, subs, err := cfg.DataLoader(ctx, params)
		if err != nil {
			ths.logger.Error("data loader failed",
				"config", configName,
				"id", id,
				"error", err.Error(),
			)
			continue
		}

		// Build path
		path := buildPath(cfg.PathPattern, params)
		fullPath := ths.config.OutputDir + path

		// Create component
		component := cfg.Component(ctx, params, data)

		// Render and write
		result, err := builder.WriteComponent(ctx, component, fullPath)
		if err != nil {
			ths.logger.Error("templ build failed",
				"path", path,
				"error", err.Error(),
			)
			continue
		}

		// Subscribe page
		err = ths.registry.Subscribe(ctx, registry.PageMeta{
			Path:          path,
			Template:      "templ:" + configName,
			Params:        params,
			Subscriptions: subs,
			LastBuilt:     time.Now(),
			ContentHash:   result.ContentHash,
		})
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", path, err)
		}
	}

	return nil
}

// BuildTemplPage rebuilds a single templ page.
func (ths *TemplHotStatic) BuildTemplPage(ctx context.Context, pagePath string) (*BuildResult, error) {
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
		return ths.Build(ctx, pagePath)
	}

	configName := strings.TrimPrefix(meta.Template, "templ:")

	ths.mu.RLock()
	cfg, ok := ths.templConfigs[configName]
	ths.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("templ config not found: %s", configName)
	}

	start := time.Now()

	// Load fresh data
	data, subs, err := cfg.DataLoader(ctx, meta.Params)
	if err != nil {
		return &BuildResult{
			Page:      Page{Path: meta.Path},
			Success:   false,
			Error:     err.Error(),
			Duration:  time.Since(start),
			Timestamp: time.Now(),
		}, err
	}

	// Update subscriptions if changed
	if !equalSlices(subs, meta.Subscriptions) {
		meta.Subscriptions = subs
		ths.registry.Subscribe(ctx, *meta)
	}

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
			Path:          meta.Path,
			Template:      meta.Template,
			Subscriptions: meta.Subscriptions,
			Params:        meta.Params,
			LastBuilt:     time.Now(),
			ContentHash:   result.ContentHash,
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
