package hotstatic

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flosch/pongo2/v6"
	"github.com/fsnotify/fsnotify"
)

// PongoBuilder implements Builder interface using pongo2 templates.
type PongoBuilder struct {
	templateDir string
	outputDir   string
	templateSet *pongo2.TemplateSet
	globals     map[string]any
	mu          sync.RWMutex
}

// PongoConfig for PongoBuilder.
type PongoConfig struct {
	TemplateDir string
	OutputDir   string
}

// NewPongoBuilder creates a new pongo2 builder.
func NewPongoBuilder(cfg PongoConfig) (*PongoBuilder, error) {
	if cfg.TemplateDir == "" {
		cfg.TemplateDir = "./templates"
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./dist"
	}

	loader := pongo2.MustNewLocalFileSystemLoader(cfg.TemplateDir)
	templateSet := pongo2.NewSet("hotstatic", loader)

	pb := &PongoBuilder{
		templateDir: cfg.TemplateDir,
		outputDir:   cfg.OutputDir,
		templateSet: templateSet,
		globals:     make(map[string]any),
	}

	// Register default filters
	pb.registerDefaultFilters()

	return pb, nil
}

// Build renders a template and writes to output.
func (pb *PongoBuilder) Build(ctx context.Context, templateFile, outputPath string, data map[string]any) error {
	pb.mu.RLock()
	globals := pb.globals
	pb.mu.RUnlock()

	// Load template
	tpl, err := pb.templateSet.FromFile(templateFile)
	if err != nil {
		return fmt.Errorf("load template %s: %w", templateFile, err)
	}

	// Merge globals with data
	context := pongo2.Context{}
	for k, v := range globals {
		context[k] = v
	}
	for k, v := range data {
		context[k] = v
	}

	// Add built-in variables
	context["_generated_at"] = time.Now()
	context["_template"] = templateFile
	context["_output"] = outputPath

	// Render
	content, err := tpl.Execute(context)
	if err != nil {
		return fmt.Errorf("render template %s: %w", templateFile, err)
	}

	// Write to file
	fullPath := filepath.Join(pb.outputDir, outputPath)

	// Ensure directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write file %s: %w", fullPath, err)
	}

	return nil
}

// Delete removes a file from output directory.
func (pb *PongoBuilder) Delete(outputPath string) error {
	fullPath := filepath.Join(pb.outputDir, outputPath)
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// AddGlobal adds a global variable available in all templates.
func (pb *PongoBuilder) AddGlobal(name string, value any) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.globals[name] = value
}

// AddFilter adds a custom template filter.
func (pb *PongoBuilder) AddFilter(name string, fn pongo2.FilterFunction) error {
	return pongo2.RegisterFilter(name, fn)
}

// Reload reloads all templates (clears cache).
func (pb *PongoBuilder) Reload() error {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	loader := pongo2.MustNewLocalFileSystemLoader(pb.templateDir)
	pb.templateSet = pongo2.NewSet("hotstatic", loader)

	return nil
}

// TemplateDir returns the template directory.
func (pb *PongoBuilder) TemplateDir() string {
	return pb.templateDir
}

// OutputDir returns the output directory.
func (pb *PongoBuilder) OutputDir() string {
	return pb.outputDir
}

func (pb *PongoBuilder) registerDefaultFilters() {
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

// PongoHotStatic is HotStatic with pongo2 support.
type PongoHotStatic struct {
	*HotStatic
	builder *PongoBuilder
}

// NewWithPongo creates HotStatic with pongo2 support.
func NewWithPongo(cfg Config) (*PongoHotStatic, error) {
	builder, err := NewPongoBuilder(PongoConfig{
		TemplateDir: cfg.TemplateDir,
		OutputDir:   cfg.OutputDir,
	})
	if err != nil {
		return nil, err
	}

	hs := New(cfg)
	hs.SetBuilder(builder)

	return &PongoHotStatic{
		HotStatic: hs,
		builder:   builder,
	}, nil
}

// Builder returns the pongo2 builder.
func (phs *PongoHotStatic) Builder() *PongoBuilder {
	return phs.builder
}

// AddGlobal adds a global variable available in all templates.
func (phs *PongoHotStatic) AddGlobal(name string, value any) {
	phs.builder.AddGlobal(name, value)
}

// AddFilter adds a custom template filter.
func (phs *PongoHotStatic) AddFilter(name string, fn pongo2.FilterFunction) error {
	return phs.builder.AddFilter(name, fn)
}

// StartDevMode starts file watcher for templates.
// On template change, reloads templates and calls onChange callback.
func (phs *PongoHotStatic) StartDevMode(ctx context.Context, onChange func()) error {
	if !phs.config.DevMode {
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}

	// Watch template directory recursively
	err = filepath.WalkDir(phs.builder.templateDir, func(path string, d fs.DirEntry, err error) error {
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

	phs.config.Logger.Info("dev mode started",
		"templates", phs.builder.templateDir,
	)

	go phs.watchLoop(ctx, watcher, onChange)

	return nil
}

func (phs *PongoHotStatic) watchLoop(ctx context.Context, watcher *fsnotify.Watcher, onChange func()) {
	defer watcher.Close()

	var pending bool
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

			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) == 0 {
				continue
			}

			mu.Lock()
			pending = true
			lastEvent = time.Now()
			mu.Unlock()

		case <-ticker.C:
			mu.Lock()
			if pending && time.Since(lastEvent) >= debounceDelay {
				pending = false
				mu.Unlock()

				phs.config.Logger.Info("template changed, reloading")

				if err := phs.builder.Reload(); err != nil {
					phs.config.Logger.Error("reload templates failed",
						"error", err.Error(),
					)
				} else if onChange != nil {
					onChange()
				}
			} else {
				mu.Unlock()
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			phs.config.Logger.Error("watcher error",
				"error", err.Error(),
			)
		}
	}
}
