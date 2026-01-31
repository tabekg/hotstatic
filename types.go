package hotstatic

import (
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

	// Priority for rebuild queue (higher = more urgent)
	Priority int `json:"priority"`

	// Payload contains the entity data for template rendering.
	// This data is passed directly to the template.
	Payload map[string]any `json:"payload,omitempty"`

	// Metadata for additional context (not passed to template)
	Metadata map[string]any `json:"metadata,omitempty"`
}

// HasPayload returns true if the event contains payload data.
func (e Event) HasPayload() bool {
	return len(e.Payload) > 0
}

// Key returns the subscription key for this event (e.g., "product:123")
func (e Event) Key() string {
	return e.Type + ":" + e.ID
}

// Page represents a static page that can be rebuilt.
type Page struct {
	// Path is the output file path (e.g., "/products/123.html")
	Path string `json:"path"`

	// Template name to use for rendering
	Template string `json:"template"`

	// Subscriptions are keys this page depends on (e.g., ["product:123", "brand:apple"])
	Subscriptions []string `json:"subscriptions"`

	// Params extracted from path pattern
	Params map[string]string `json:"params"`

	// LastBuilt timestamp
	LastBuilt time.Time `json:"last_built"`

	// Hash of last build content for change detection
	ContentHash string `json:"content_hash,omitempty"`
}

// PageConfig defines how to generate pages of a certain type.
type PageConfig struct {
	// Name identifies this page config
	Name string

	// PathPattern with placeholders (e.g., "/products/{id}.html")
	PathPattern string

	// Template file or name
	Template string

	// Priority for rebuild queue (default: 0)
	Priority int
}

// BuildResult represents the outcome of building a page.
type BuildResult struct {
	Page      Page          `json:"page"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
	Duration  time.Duration `json:"duration"`
	Changed   bool          `json:"changed"`
	Timestamp time.Time     `json:"timestamp"`
}

// Stats provides runtime statistics.
type Stats struct {
	PagesTotal      int64         `json:"pages_total"`
	PagesBuilt      int64         `json:"pages_built"`
	PagesFailed     int64         `json:"pages_failed"`
	EventsProcessed int64         `json:"events_processed"`
	QueueLength     int64         `json:"queue_length"`
	WorkersActive   int           `json:"workers_active"`
	Uptime          time.Duration `json:"uptime"`
}

// Subscription links a page to an event key.
type Subscription struct {
	Key      string `json:"key"`       // e.g., "product:123"
	PagePath string `json:"page_path"` // e.g., "/products/123.html"
}
