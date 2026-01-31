package hotstatic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/flosch/pongo2/v6"
	"github.com/tabekg/hotstatic/pkg/builder"
	"github.com/tabekg/hotstatic/pkg/registry"
	"github.com/tabekg/hotstatic/pkg/worker"
)

// PongoPageConfig defines a page that uses pongo2 templates.
type PongoPageConfig struct {
	// Name identifies this page config
	Name string

	// PathPattern with placeholders (e.g., "/products/{id}.html")
	PathPattern string

	// Template file path relative to template dir
	Template string

	// Priority for rebuild queue (default: 0)
	Priority int
}

// PongoHotStatic extends HotStatic with pongo2 support.
type PongoHotStatic struct {
	*HotStatic
	pongoBuilder *builder.PongoBuilder
	pongoConfigs map[string]*PongoPageConfig
	mu           sync.RWMutex
}

// NewWithPongo creates HotStatic with pongo2 (Django/Jinja2) support.
func NewWithPongo(cfg Config) (*PongoHotStatic, error) {
	// Save TemplateDir for pongo, but don't pass to base HotStatic
	// (base uses html/template which can't parse pongo2 syntax)
	pongoTemplateDir := cfg.TemplateDir
	cfg.TemplateDir = ""

	// Create pongo builder first
	pongoBuilder, err := builder.NewPongoBuilder(builder.PongoConfig{
		TemplateDir: pongoTemplateDir,
		OutputDir:   cfg.OutputDir,
	})
	if err != nil {
		return nil, fmt.Errorf("init pongo builder: %w", err)
	}

	// Pre-create PongoHotStatic so we can reference it in the handler
	phs := &PongoHotStatic{
		pongoBuilder: pongoBuilder,
		pongoConfigs: make(map[string]*PongoPageConfig),
	}

	// Set custom build handler that knows about pongo configs
	cfg.BuildHandler = phs.pongoBuildHandler

	hs, err := New(cfg)
	if err != nil {
		return nil, err
	}

	phs.HotStatic = hs

	// Register default filters
	phs.registerDefaultFilters()

	return phs, nil
}

// RegisterPongoPage registers a pongo2-based page configuration.
func (phs *PongoHotStatic) RegisterPongoPage(name string, cfg PongoPageConfig) {
	phs.mu.Lock()
	defer phs.mu.Unlock()

	cfg.Name = name
	phs.pongoConfigs[name] = &cfg
}

// GeneratePongoPage generates a single page using pongo2 template.
func (phs *PongoHotStatic) GeneratePongoPage(ctx context.Context, page Page, data map[string]any) error {
	// Extract template name from page.Template (remove "pongo:" prefix if present)
	templateName := page.Template
	if strings.HasPrefix(templateName, "pongo:") {
		templateName = strings.TrimPrefix(templateName, "pongo:")
	}

	// Add common context
	data["_path"] = page.Path
	data["_params"] = page.Params
	data["_generated_at"] = time.Now()

	// Render and write
	result, err := phs.pongoBuilder.Build(ctx, templateName, page.Path, data)
	if err != nil {
		return fmt.Errorf("pongo build %s: %w", page.Path, err)
	}

	// Subscribe page
	err = phs.registry.Subscribe(ctx, registry.PageMeta{
		Path:          page.Path,
		Template:      "pongo:" + templateName,
		Params:        page.Params,
		Subscriptions: page.Subscriptions,
		LastBuilt:     time.Now(),
		ContentHash:   result.ContentHash,
	})
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", page.Path, err)
	}

	return nil
}

// BuildPongoPage rebuilds a single pongo2 page with the given payload.
func (phs *PongoHotStatic) BuildPongoPage(ctx context.Context, pagePath string, payload map[string]any) (*BuildResult, error) {
	meta, err := phs.registry.GetPage(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, fmt.Errorf("page not found: %s", pagePath)
	}

	// Check if it's a pongo page
	if !strings.HasPrefix(meta.Template, "pongo:") {
		return phs.Build(ctx, pagePath, payload)
	}

	templateName := strings.TrimPrefix(meta.Template, "pongo:")
	start := time.Now()

	// Add common context
	payload["_path"] = meta.Path
	payload["_params"] = meta.Params
	payload["_generated_at"] = time.Now()

	// Render
	result, err := phs.pongoBuilder.Build(ctx, templateName, meta.Path, payload)
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
	phs.registry.UpdateLastBuilt(ctx, meta.Path, time.Now(), result.ContentHash)

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

// PongoBuilder returns the underlying pongo2 builder.
func (phs *PongoHotStatic) PongoBuilder() *builder.PongoBuilder {
	return phs.pongoBuilder
}

// AddFilter adds a custom template filter.
// Usage: {{ value|myfilter }} or {{ value|myfilter:arg }}
func (phs *PongoHotStatic) AddFilter(name string, fn pongo2.FilterFunction) error {
	return phs.pongoBuilder.AddFilter(name, fn)
}

// AddGlobal adds a global variable available in all templates.
func (phs *PongoHotStatic) AddGlobal(name string, value any) {
	phs.pongoBuilder.AddGlobal(name, value)
}

// ReloadTemplates reloads all templates from disk.
func (phs *PongoHotStatic) ReloadTemplates() error {
	return phs.pongoBuilder.LoadTemplates()
}

// pongoBuildHandler handles page rebuild jobs for both pongo and standard pages.
func (phs *PongoHotStatic) pongoBuildHandler(ctx context.Context, job worker.Job) error {
	if len(job.Payload) == 0 {
		phs.logger.Warn("skipping rebuild - no payload provided",
			"path", job.Path,
		)
		return nil
	}

	meta, err := phs.registry.GetPage(ctx, job.Path)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("page not found: %s", job.Path)
	}

	// Check if it's a pongo page
	if strings.HasPrefix(meta.Template, "pongo:") {
		_, err = phs.BuildPongoPage(ctx, job.Path, job.Payload)
		return err
	}

	// Fall back to base handler for non-pongo pages
	_, err = phs.buildPageWithPayload(ctx, meta, job.Payload)
	return err
}

func (phs *PongoHotStatic) registerDefaultFilters() {
	// Format price: {{ 99.99|price }} → $99.99
	pongo2.RegisterFilter("price", func(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
		format := "$%.2f"
		if param.String() != "" {
			format = param.String()
		}
		return pongo2.AsValue(fmt.Sprintf(format, in.Float())), nil
	})

	// Truncate text: {{ text|truncate:100 }}
	pongo2.RegisterFilter("truncate", func(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
		s := in.String()
		length := param.Integer()
		if length <= 0 {
			length = 100
		}
		if len(s) > length {
			return pongo2.AsValue(s[:length] + "..."), nil
		}
		return in, nil
	})

	// Pluralize: {{ count|pluralize:"item,items" }}
	pongo2.RegisterFilter("pluralize", func(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
		count := in.Integer()
		forms := strings.Split(param.String(), ",")
		if len(forms) < 2 {
			forms = []string{"", "s"}
		}
		if count == 1 {
			return pongo2.AsValue(forms[0]), nil
		}
		return pongo2.AsValue(forms[1]), nil
	})

	// Time ago: {{ date|timeago }}
	pongo2.RegisterFilter("timeago", func(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
		t, ok := in.Interface().(time.Time)
		if !ok {
			return in, nil
		}
		diff := time.Since(t)
		switch {
		case diff < time.Minute:
			return pongo2.AsValue("just now"), nil
		case diff < time.Hour:
			return pongo2.AsValue(fmt.Sprintf("%d minutes ago", int(diff.Minutes()))), nil
		case diff < 24*time.Hour:
			return pongo2.AsValue(fmt.Sprintf("%d hours ago", int(diff.Hours()))), nil
		default:
			return pongo2.AsValue(fmt.Sprintf("%d days ago", int(diff.Hours()/24))), nil
		}
	})
}
