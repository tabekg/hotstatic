package hotstatic

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flosch/pongo2/v6"
	"github.com/fsnotify/fsnotify"
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

// BuilderFunc is called to build all pages.
// It's invoked at startup and on template changes in dev mode.
type BuilderFunc func(ctx context.Context, b *PageBuilder) error

// PageBuilder provides methods to build pages.
type PageBuilder struct {
	phs   *PongoHotStatic
	ctx   context.Context
	count int
}

// Page builds a single page and returns PageBuildResult for chaining.
func (pb *PageBuilder) Page(template, output string, data map[string]any) *PageBuildResult {
	start := time.Now()

	if data == nil {
		data = make(map[string]any)
	}
	data["_path"] = output
	data["_generated_at"] = time.Now()

	result, err := pb.phs.pongoBuilder.Build(pb.ctx, template, output, data)
	duration := time.Since(start)

	pbr := &PageBuildResult{
		phs:      pb.phs,
		ctx:      pb.ctx,
		template: template,
		output:   output,
		err:      err,
	}

	if err == nil {
		pbr.contentHash = result.ContentHash
		pb.count++
		if pb.phs.config.DevMode {
			pb.phs.logger.Info("built page",
				slog.String("output", output),
				slog.Duration("time", duration),
			)
		}
	} else {
		pb.phs.logger.Error("build page failed",
			slog.String("template", template),
			slog.String("output", output),
			slog.String("error", err.Error()),
		)
	}

	return pbr
}

// PageBuildResult allows chaining Subscribe calls.
type PageBuildResult struct {
	phs         *PongoHotStatic
	ctx         context.Context
	template    string
	output      string
	contentHash string
	err         error
}

// Subscribe registers subscriptions for this page.
// When events matching these keys are emitted, the page will be rebuilt.
func (pbr *PageBuildResult) Subscribe(keys ...string) *PageBuildResult {
	if pbr.err != nil {
		return pbr
	}

	err := pbr.phs.registry.Subscribe(pbr.ctx, registry.PageMeta{
		Path:          pbr.output,
		Template:      "pongo:" + pbr.template,
		Subscriptions: keys,
		LastBuilt:     time.Now(),
		ContentHash:   pbr.contentHash,
	})
	if err != nil {
		pbr.phs.logger.Error("subscribe failed",
			slog.String("output", pbr.output),
			slog.String("error", err.Error()),
		)
		pbr.err = err
	}

	return pbr
}

// Error returns any error that occurred during build or subscribe.
func (pbr *PageBuildResult) Error() error {
	return pbr.err
}

// PongoHotStatic extends HotStatic with pongo2 support.
type PongoHotStatic struct {
	*HotStatic
	pongoBuilder *builder.PongoBuilder
	pongoConfigs map[string]*PongoPageConfig
	builderFunc  BuilderFunc
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
	templateName := strings.TrimPrefix(page.Template, "pongo:")

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

// SetBuilder sets the builder function that defines how pages are built.
// This function is called at startup (BuildAll) and on template changes in dev mode.
func (phs *PongoHotStatic) SetBuilder(fn BuilderFunc) {
	phs.mu.Lock()
	defer phs.mu.Unlock()
	phs.builderFunc = fn
}

// BuildAll executes the builder function to build all pages.
// Call this at startup after SetBuilder.
func (phs *PongoHotStatic) BuildAll(ctx context.Context) error {
	start := time.Now()

	phs.mu.RLock()
	fn := phs.builderFunc
	phs.mu.RUnlock()

	if fn == nil {
		return fmt.Errorf("builder function not set, call SetBuilder first")
	}

	pb := &PageBuilder{
		phs:   phs,
		ctx:   ctx,
		count: 0,
	}

	err := fn(ctx, pb)
	duration := time.Since(start)

	if err == nil && phs.config.DevMode {
		phs.logger.Info("build complete",
			slog.Int("pages", pb.count),
			slog.Duration("total", duration),
		)
	}

	return err
}

// BuildStaticPages builds all static pages from StaticPagesDir.
// Scans the directory for templates and builds each one.
// Example: templates/pages/about.jinja2 → /about.html
func (phs *PongoHotStatic) BuildStaticPages(ctx context.Context) error {
	if phs.config.StaticPagesDir == "" {
		return nil
	}

	templateDir := phs.pongoBuilder.TemplateDir()
	pagesDir := filepath.Join(templateDir, phs.config.StaticPagesDir)

	count := 0
	err := filepath.WalkDir(pagesDir, func(path string, d fs.DirEntry, err error) error {
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

		// Get relative path from template dir (e.g., "pages/about.jinja2")
		relPath, err := filepath.Rel(templateDir, path)
		if err != nil {
			return err
		}

		// Get relative path from pages dir (e.g., "about.jinja2" or "sub/page.jinja2")
		relFromPages, err := filepath.Rel(pagesDir, path)
		if err != nil {
			return err
		}

		// Convert to output path: about.jinja2 → /about.html
		outputPath := "/" + strings.TrimSuffix(relFromPages, ext) + ".html"

		data := map[string]any{
			"_path":         outputPath,
			"_generated_at": time.Now(),
		}

		_, err = phs.pongoBuilder.Build(ctx, relPath, outputPath, data)
		if err != nil {
			return fmt.Errorf("build static page %s: %w", outputPath, err)
		}

		phs.logger.Debug("built static page",
			slog.String("output", outputPath),
			slog.String("template", relPath),
		)
		count++

		return nil
	})

	if err != nil {
		return err
	}

	phs.logger.Info("built static pages", slog.Int("count", count))
	return nil
}

// StartDevMode starts file watcher for templates and additional directories.
// On template change, all pages are rebuilt via BuildAll.
// On watched dir change, OnWatchedFileChange callback is called.
func (phs *PongoHotStatic) StartDevMode(ctx context.Context) error {
	if !phs.config.DevMode {
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}

	templateDir := phs.pongoBuilder.TemplateDir()

	// Watch template directory recursively
	err = filepath.WalkDir(templateDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		watcher.Close()
		return fmt.Errorf("watch template dir: %w", err)
	}

	// Watch static directory if configured
	if phs.config.StaticDir != "" {
		err = filepath.WalkDir(phs.config.StaticDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return watcher.Add(path)
			}
			return nil
		})
		if err != nil {
			phs.logger.Warn("could not watch static directory",
				slog.String("dir", phs.config.StaticDir),
				slog.String("error", err.Error()),
			)
		}
	}

	phs.logger.Info("dev mode started",
		slog.String("templates", templateDir),
		slog.String("static", phs.config.StaticDir),
	)

	go phs.watchLoop(ctx, watcher, templateDir)

	return nil
}

func (phs *PongoHotStatic) watchLoop(ctx context.Context, watcher *fsnotify.Watcher, templateDir string) {
	defer watcher.Close()

	// Debounce state
	var pendingTemplate bool
	var pendingStatic bool
	var lastTemplatePath string
	var lastStaticPath string
	var lastEvent time.Time
	var mu sync.Mutex
	debounceDelay := 100 * time.Millisecond

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Handle write, create, remove events
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) == 0 {
				continue
			}

			mu.Lock()
			// Check if it's a template file or static file
			if strings.HasPrefix(event.Name, templateDir) {
				pendingTemplate = true
				lastTemplatePath = event.Name
			} else {
				pendingStatic = true
				lastStaticPath = event.Name
			}
			lastEvent = time.Now()
			mu.Unlock()

		case <-ticker.C:
			mu.Lock()
			if time.Since(lastEvent) >= debounceDelay {
				if pendingTemplate {
					pendingTemplate = false
					path := lastTemplatePath
					mu.Unlock()

					phs.logger.Info("template changed", slog.String("path", path))
					if phs.config.OnTemplateChange != nil {
						phs.config.OnTemplateChange(path)
					}
					phs.rebuildAll(ctx)
				} else if pendingStatic {
					pendingStatic = false
					path := lastStaticPath
					mu.Unlock()

					phs.logger.Info("static file changed", slog.String("path", path))
					if phs.config.OnStaticChange != nil {
						phs.config.OnStaticChange(path)
					}
				} else {
					mu.Unlock()
				}
			} else {
				mu.Unlock()
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			phs.logger.Error("watcher error", slog.String("error", err.Error()))
		}
	}
}

func (phs *PongoHotStatic) rebuildAll(ctx context.Context) {
	phs.logger.Info("file changed, rebuilding pages")

	// Reload templates to pick up changes
	if err := phs.ReloadTemplates(); err != nil {
		phs.logger.Error("reload templates failed", slog.String("error", err.Error()))
		return
	}

	// Rebuild all pages using builder function
	if phs.builderFunc != nil {
		if err := phs.BuildAll(ctx); err != nil {
			phs.logger.Error("rebuild pages failed", slog.String("error", err.Error()))
		}
	}
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
