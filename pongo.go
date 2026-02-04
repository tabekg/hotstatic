package hotstatic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/flosch/pongo2/v6"
	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/html"
	"github.com/tdewolff/minify/v2/js"
	"github.com/tdewolff/minify/v2/json"
	"github.com/tdewolff/minify/v2/svg"
	"github.com/tdewolff/minify/v2/xml"
)

// PongoBuilder implements Builder interface using pongo2 templates.
type PongoBuilder struct {
	templateDir string
	outputDir   string
	templateSet *pongo2.TemplateSet
	globals     map[string]any
	minifier    *minify.M
	mu          sync.RWMutex
}

// NewPongoBuilder creates a new pongo2 builder.
func NewPongoBuilder(templateDir, outputDir string, enableMinify bool) (*PongoBuilder, error) {
	if templateDir == "" {
		templateDir = "./templates"
	}
	if outputDir == "" {
		outputDir = "./dist"
	}

	loader := pongo2.MustNewLocalFileSystemLoader(templateDir)
	templateSet := pongo2.NewSet("hotstatic", loader)

	pb := &PongoBuilder{
		templateDir: templateDir,
		outputDir:   outputDir,
		templateSet: templateSet,
		globals:     make(map[string]any),
	}

	if enableMinify {
		pb.minifier = minify.New()
		pb.minifier.AddFunc("text/html", html.Minify)
		pb.minifier.AddFunc("text/css", css.Minify)
		pb.minifier.AddFunc("application/javascript", js.Minify)
		pb.minifier.AddFunc("application/json", json.Minify)
		pb.minifier.AddFunc("image/svg+xml", svg.Minify)
		pb.minifier.AddFunc("text/xml", xml.Minify)
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

	// Minify if enabled
	if pb.minifier != nil {
		mediaType := pb.detectMediaType(outputPath)
		if mediaType != "" {
			minified, err := pb.minifier.String(mediaType, content)
			if err == nil {
				content = minified
			}
		}
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

func (pb *PongoBuilder) detectMediaType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".xml":
		return "text/xml"
	default:
		return ""
	}
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
	Builder *PongoBuilder
}

// NewWithPongo creates HotStatic with pongo2 support.
func NewWithPongo(cfg Config) (*PongoHotStatic, error) {
	builder, err := NewPongoBuilder(cfg.TemplateDir, cfg.OutputDir, cfg.Minify)
	if err != nil {
		return nil, err
	}

	hs := New(cfg)
	hs.SetBuilder(builder)

	return &PongoHotStatic{
		HotStatic: hs,
		Builder:   builder,
	}, nil
}

// AddGlobal adds a global variable available in all templates.
func (phs *PongoHotStatic) AddGlobal(name string, value any) {
	phs.Builder.AddGlobal(name, value)
}

// AddFilter adds a custom template filter.
func (phs *PongoHotStatic) AddFilter(name string, fn pongo2.FilterFunction) error {
	return phs.Builder.AddFilter(name, fn)
}
