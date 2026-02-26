package hotstatic

import (
	"context"
	"time"
)

// PageData contains data for rendering a page and its output path.
type PageData struct {
	// Path is the output file path (e.g., "/products/123.html" or "/ru/products/my-product.html")
	Path string

	// Data is the template data
	Data map[string]any
}

// TemplateDef defines a template and how to load data for it.
type TemplateDef struct {
	// File is the template file path relative to TemplateDir (e.g., "pages/product.jinja2")
	File string

	// Load fetches data for a single page by ID.
	// Returns nil to skip (e.g., deleted or inactive entity).
	// The returned PageData contains both the output path and template data.
	Load func(ctx context.Context, id string) (*PageData, error)

	// LoadAll returns all IDs for initial BuildAll.
	// Called once at startup to build all pages of this template.
	LoadAll func(ctx context.Context) ([]string, error)
}

// Config for HotStatic.
type Config struct {
	// TemplateDir for template files
	TemplateDir string

	// OutputDir for generated HTML files
	OutputDir string

	// Workers count for parallel building (default: 4)
	Workers int

	// Debounce duration - same page won't rebuild more than once per this duration (default: 1s)
	Debounce time.Duration

	// Minify enables HTML/CSS/JS minification (default: false)
	Minify bool

	// ProgressInterval controls how often progress is logged during queue processing.
	// For example, 500 means log every 500 pages built. (default: 500)
	ProgressInterval int64

	// Logger instance (optional)
	Logger Logger
}

// Logger interface for logging.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}
