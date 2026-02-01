package hotstatic

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tabekg/hotstatic/pkg/builder"
	"github.com/tabekg/hotstatic/pkg/queue"
	"github.com/tabekg/hotstatic/pkg/registry"
	"github.com/tabekg/hotstatic/pkg/worker"
)

// HotStatic is the main framework instance.
type HotStatic struct {
	config      Config
	registry    *registry.Registry
	builder     *builder.Builder
	queue       *queue.Queue
	pool        *worker.Pool
	pageConfigs map[string]*PageConfig
	logger      *slog.Logger
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	started     bool
	startTime   time.Time

	// Metrics
	eventsProcessed int64
	pagesBuilt      int64
	pagesFailed     int64
}

// BuildHandlerFunc is called for each page rebuild job.
type BuildHandlerFunc func(ctx context.Context, job worker.Job) error

// Config for HotStatic.
type Config struct {
	// Redis connection string
	Redis string

	// RedisPassword optional
	RedisPassword string

	// RedisDB number
	RedisDB int

	// RedisPrefix for key namespacing
	RedisPrefix string

	// TemplateDir for templates
	TemplateDir string

	// OutputDir for generated files
	OutputDir string

	// NotFoundPage is the path to custom 404 page relative to OutputDir (e.g., "404.html").
	// Used by StaticHandler when serving files.
	NotFoundPage string

	// CacheRules defines caching behavior for static files.
	// Rules are checked in order, first match wins.
	CacheRules []CacheRule

	// StaticPagesDir is the directory containing static page templates (relative to TemplateDir).
	// All templates in this directory are automatically built as static pages.
	// Example: "pages" → templates/pages/*.jinja2 → /about.html, /contact.html, etc.
	// Default: "" (disabled)
	StaticPagesDir string

	// DevMode enables file watching and auto-rebuild for static pages.
	DevMode bool

	// WatchDirs is a list of additional directories to watch in dev mode.
	// Example: []string{"./static", "./assets"}
	WatchDirs []string

	// OnTemplateChange is called when a template file changes (before rebuild).
	// Receives the changed file path.
	OnTemplateChange func(path string)

	// OnWatchedFileChange is called when a file in WatchDirs changes.
	// Receives the changed file path. Use this to run build tools (e.g., "yarn build").
	OnWatchedFileChange func(path string)

	// Workers count for parallel building
	Workers int

	// QueueSize buffer size
	QueueSize int

	// Logger instance
	Logger *slog.Logger

	// FuncMap custom template functions
	FuncMap template.FuncMap

	// BuildHandler custom handler for page rebuilds (optional).
	// If not set, the default handler is used.
	BuildHandler BuildHandlerFunc
}

// New creates a new HotStatic instance.
func New(cfg Config) (*HotStatic, error) {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 10000
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./dist"
	}

	// Initialize registry
	reg, err := registry.New(registry.Config{
		RedisAddr:     cfg.Redis,
		RedisPassword: cfg.RedisPassword,
		RedisDB:       cfg.RedisDB,
		Prefix:        cfg.RedisPrefix,
	})
	if err != nil {
		return nil, fmt.Errorf("init registry: %w", err)
	}

	// Initialize builder
	bld, err := builder.New(builder.Config{
		TemplateDir: cfg.TemplateDir,
		OutputDir:   cfg.OutputDir,
		FuncMap:     cfg.FuncMap,
	})
	if err != nil {
		reg.Close()
		return nil, fmt.Errorf("init builder: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	hs := &HotStatic{
		config:      cfg,
		registry:    reg,
		builder:     bld,
		queue:       queue.New(),
		pageConfigs: make(map[string]*PageConfig),
		logger:      cfg.Logger,
		ctx:         ctx,
		cancel:      cancel,
	}

	// Use custom handler if provided, otherwise use default
	handler := hs.buildHandler
	if cfg.BuildHandler != nil {
		handler = cfg.BuildHandler
	}

	// Initialize worker pool
	hs.pool = worker.NewPool(worker.Config{
		NumWorkers: cfg.Workers,
		QueueSize:  cfg.QueueSize,
		Logger:     cfg.Logger,
	}, handler)

	return hs, nil
}

// RegisterPage registers a page configuration.
func (hs *HotStatic) RegisterPage(name string, cfg PageConfig) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	cfg.Name = name
	hs.pageConfigs[name] = &cfg
}

// RegisterTemplate adds a template from string.
func (hs *HotStatic) RegisterTemplate(name, content string) error {
	return hs.builder.RegisterTemplate(name, content)
}

// Start begins processing events.
func (hs *HotStatic) Start() {
	hs.mu.Lock()
	if hs.started {
		hs.mu.Unlock()
		return
	}
	hs.started = true
	hs.startTime = time.Now()
	hs.mu.Unlock()

	hs.pool.Start()

	// Start queue processor
	go hs.processQueue()

	// Start results processor
	go hs.processResults()

	hs.logger.Info("HotStatic started",
		slog.Int("workers", hs.config.Workers),
		slog.String("output", hs.config.OutputDir),
	)
}

// Stop shuts down HotStatic.
func (hs *HotStatic) Stop() error {
	hs.cancel()
	hs.queue.Close()
	hs.pool.Stop()
	return hs.registry.Close()
}

// Emit triggers a rebuild for all pages subscribed to the key.
func (hs *HotStatic) Emit(key, action string) error {
	return hs.EmitEvent(Event{
		Type:      strings.Split(key, ":")[0],
		ID:        strings.TrimPrefix(key, strings.Split(key, ":")[0]+":"),
		Action:    action,
		Timestamp: time.Now(),
	})
}

// EmitWithPayload triggers a rebuild with entity data.
// The payload is passed directly to the template for rendering.
func (hs *HotStatic) EmitWithPayload(key, action string, payload map[string]any) error {
	return hs.EmitEvent(Event{
		Type:      strings.Split(key, ":")[0],
		ID:        strings.TrimPrefix(key, strings.Split(key, ":")[0]+":"),
		Action:    action,
		Payload:   payload,
		Timestamp: time.Now(),
	})
}

// EmitEvent triggers rebuilds for an event.
func (hs *HotStatic) EmitEvent(event Event) error {
	atomic.AddInt64(&hs.eventsProcessed, 1)

	key := event.Key()
	pages, err := hs.registry.GetSubscribers(hs.ctx, key)
	if err != nil {
		return fmt.Errorf("get subscribers for %s: %w", key, err)
	}

	hs.logger.Debug("event received",
		slog.String("key", key),
		slog.String("action", event.Action),
		slog.Int("subscribers", len(pages)),
		slog.Bool("has_payload", event.HasPayload()),
	)

	for _, pagePath := range pages {
		hs.queue.Push(queue.Item{
			Path:       pagePath,
			Priority:   event.Priority,
			TriggerKey: key,
			Payload:    event.Payload,
		})
	}

	return nil
}

// EmitMulti triggers rebuilds for multiple keys.
func (hs *HotStatic) EmitMulti(keys []string, action string) error {
	pages, err := hs.registry.GetSubscribersMulti(hs.ctx, keys)
	if err != nil {
		return err
	}

	for _, pagePath := range pages {
		hs.queue.Push(queue.Item{
			Path:     pagePath,
			Priority: 0,
		})
	}

	return nil
}

// Build immediately builds a page by path with the given payload.
func (hs *HotStatic) Build(ctx context.Context, pagePath string, payload map[string]any) (*BuildResult, error) {
	meta, err := hs.registry.GetPage(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, fmt.Errorf("page not found: %s", pagePath)
	}

	return hs.buildPageWithPayload(ctx, meta, payload)
}

// ListPages returns all registered page paths.
func (hs *HotStatic) ListPages(ctx context.Context) ([]string, error) {
	return hs.registry.ListPages(ctx)
}

// Subscribe registers a page with subscriptions.
func (hs *HotStatic) Subscribe(ctx context.Context, page Page) error {
	return hs.registry.Subscribe(ctx, registry.PageMeta{
		Path:          page.Path,
		Template:      page.Template,
		Params:        page.Params,
		Subscriptions: page.Subscriptions,
		LastBuilt:     page.LastBuilt,
		ContentHash:   page.ContentHash,
	})
}

// Unsubscribe removes a page.
func (hs *HotStatic) Unsubscribe(ctx context.Context, pagePath string) error {
	return hs.registry.Unsubscribe(ctx, pagePath)
}

// GeneratePage creates a single page with the given data and subscriptions.
func (hs *HotStatic) GeneratePage(ctx context.Context, page Page, data map[string]any) error {
	// Subscribe page
	err := hs.Subscribe(ctx, page)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", page.Path, err)
	}

	// Build immediately
	result, err := hs.builder.Build(ctx, page.Template, page.Path, data)
	if err != nil {
		return fmt.Errorf("build %s: %w", page.Path, err)
	}

	// Update registry
	hs.registry.UpdateLastBuilt(ctx, page.Path, time.Now(), result.ContentHash)
	return nil
}

// Stats returns current statistics.
func (hs *HotStatic) Stats() Stats {
	poolStats := hs.pool.Stats()
	regStats, _ := hs.registry.Stats(hs.ctx)

	return Stats{
		PagesTotal:      regStats["pages"],
		PagesBuilt:      atomic.LoadInt64(&hs.pagesBuilt),
		PagesFailed:     atomic.LoadInt64(&hs.pagesFailed),
		EventsProcessed: atomic.LoadInt64(&hs.eventsProcessed),
		QueueLength:     int64(hs.queue.Len()),
		WorkersActive:   poolStats.ActiveCount,
		Uptime:          time.Since(hs.startTime),
	}
}

// Registry returns the underlying registry.
func (hs *HotStatic) Registry() *registry.Registry {
	return hs.registry
}

// Builder returns the underlying builder.
func (hs *HotStatic) Builder() *builder.Builder {
	return hs.builder
}

func (hs *HotStatic) processQueue() {
	for {
		select {
		case <-hs.ctx.Done():
			return
		default:
			item := hs.queue.PopWait(hs.ctx)
			if item == nil {
				continue
			}

			hs.pool.Submit(worker.Job{
				ID:         item.Path,
				Path:       item.Path,
				Priority:   item.Priority,
				TriggerKey: item.TriggerKey,
				Payload:    item.Payload,
			})
		}
	}
}

func (hs *HotStatic) processResults() {
	for result := range hs.pool.Results() {
		if result.Success {
			atomic.AddInt64(&hs.pagesBuilt, 1)
		} else {
			atomic.AddInt64(&hs.pagesFailed, 1)
		}
	}
}

func (hs *HotStatic) buildHandler(ctx context.Context, job worker.Job) error {
	if len(job.Payload) == 0 {
		hs.logger.Warn("skipping rebuild - no payload provided",
			slog.String("path", job.Path),
		)
		return nil
	}

	meta, err := hs.registry.GetPage(ctx, job.Path)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("page not found: %s", job.Path)
	}

	_, err = hs.buildPageWithPayload(ctx, meta, job.Payload)
	return err
}

func (hs *HotStatic) buildPageWithPayload(ctx context.Context, meta *registry.PageMeta, payload map[string]any) (*BuildResult, error) {
	start := time.Now()

	hs.logger.Debug("building page",
		slog.String("path", meta.Path),
		slog.Int("payload_keys", len(payload)),
	)

	// Build
	result, err := hs.builder.Build(ctx, meta.Template, meta.Path, payload)
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
	hs.registry.UpdateLastBuilt(ctx, meta.Path, time.Now(), result.ContentHash)

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

// Helper functions

func buildPath(pattern string, params map[string]string) string {
	result := pattern
	for key, value := range params {
		result = strings.ReplaceAll(result, "{"+key+"}", value)
	}
	return result
}
