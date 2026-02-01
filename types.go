package hotstatic

import (
	"context"
	"time"
)

// Event represents a change in data that may trigger page rebuilds.
type Event struct {
	// Type is the entity type (e.g., "product", "article", "ad")
	Type string `json:"type"`

	// ID is the entity identifier
	ID string `json:"id"`

	// Action describes what happened: created, updated, deleted
	Action string `json:"action"`

	// Timestamp when the event occurred
	Timestamp time.Time `json:"timestamp"`

	// Metadata for additional context
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Key returns the event key (e.g., "product:123")
func (e Event) Key() string {
	if e.ID == "" {
		return e.Type
	}
	return e.Type + ":" + e.ID
}

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

// EventHandler is called when an event is received.
// The handler decides what pages to build/delete based on the event.
type EventHandler func(ctx context.Context, event Event) error

// BuildJob represents a page build task in the queue.
type BuildJob struct {
	// Template name (e.g., "product")
	Template string

	// ID of the entity (e.g., "123")
	ID string

	// Priority for queue ordering (higher = more urgent)
	Priority int

	// Timestamp when job was created
	CreatedAt time.Time
}

// Key returns unique key for this job (for deduplication).
func (j BuildJob) Key() string {
	if j.ID == "" {
		return j.Template
	}
	return j.Template + ":" + j.ID
}

// BuildResult represents the outcome of building a page.
type BuildResult struct {
	// Template name
	Template string `json:"template"`

	// ID of the entity
	ID string `json:"id"`

	// Output path
	Output string `json:"output"`

	// Success indicates if build succeeded
	Success bool `json:"success"`

	// Error message if failed
	Error string `json:"error,omitempty"`

	// Duration of the build
	Duration time.Duration `json:"duration"`

	// Changed indicates if content changed from previous build
	Changed bool `json:"changed"`

	// Timestamp when build completed
	Timestamp time.Time `json:"timestamp"`
}

// Stats provides runtime statistics.
type Stats struct {
	// TemplatesCount is the number of defined templates
	TemplatesCount int `json:"templates_count"`

	// PagesBuilt is total pages built since startup
	PagesBuilt int64 `json:"pages_built"`

	// PagesFailed is total failed builds since startup
	PagesFailed int64 `json:"pages_failed"`

	// EventsProcessed is total events processed since startup
	EventsProcessed int64 `json:"events_processed"`

	// QueueLength is current number of jobs in queue
	QueueLength int `json:"queue_length"`

	// WorkersActive is number of currently active workers
	WorkersActive int `json:"workers_active"`

	// Uptime since Start() was called
	Uptime time.Duration `json:"uptime"`
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

	// DevMode enables file watching and auto-rebuild
	DevMode bool

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
