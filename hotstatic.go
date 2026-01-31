package hotstatic

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"regexp"
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
// When payload is provided, DataLoader will NOT be called - the payload is used directly.
// This is useful when you already have the updated data and want to avoid extra DB queries.
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

// Build immediately builds a page by path.
func (hs *HotStatic) Build(ctx context.Context, pagePath string) (*BuildResult, error) {
	meta, err := hs.registry.GetPage(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, fmt.Errorf("page not found: %s", pagePath)
	}

	return hs.buildPage(ctx, meta)
}

// BuildAll rebuilds all registered pages.
func (hs *HotStatic) BuildAll(ctx context.Context) error {
	pages, err := hs.registry.ListPages(ctx)
	if err != nil {
		return err
	}

	for _, path := range pages {
		hs.queue.Push(queue.Item{
			Path:     path,
			Priority: 0,
		})
	}

	return nil
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

// GeneratePages creates pages from a page config and data source.
func (hs *HotStatic) GeneratePages(ctx context.Context, configName string, ids []string) error {
	hs.mu.RLock()
	cfg, ok := hs.pageConfigs[configName]
	hs.mu.RUnlock()

	if !ok {
		return fmt.Errorf("page config not found: %s", configName)
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
			hs.logger.Error("data loader failed",
				slog.String("config", configName),
				slog.String("id", id),
				slog.String("error", err.Error()),
			)
			continue
		}

		// Build path
		path := buildPath(cfg.PathPattern, params)

		// Subscribe page
		err = hs.Subscribe(ctx, Page{
			Path:          path,
			Template:      cfg.Template,
			Subscriptions: subs,
			Params:        params,
		})
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", path, err)
		}

		// Build immediately
		result, err := hs.builder.Build(ctx, cfg.Template, path, data)
		if err != nil {
			hs.logger.Error("build failed",
				slog.String("path", path),
				slog.String("error", err.Error()),
			)
			continue
		}

		// Update registry
		hs.registry.UpdateLastBuilt(ctx, path, time.Now(), result.ContentHash)
	}

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

func (hs *HotStatic) buildPage(ctx context.Context, meta *registry.PageMeta) (*BuildResult, error) {
	return hs.buildPageWithPayload(ctx, meta, nil)
}

func (hs *HotStatic) buildPageWithPayload(ctx context.Context, meta *registry.PageMeta, payload map[string]any) (*BuildResult, error) {
	start := time.Now()

	// Find page config
	hs.mu.RLock()
	var cfg *PageConfig
	for _, c := range hs.pageConfigs {
		if c.Template == meta.Template {
			cfg = c
			break
		}
	}
	hs.mu.RUnlock()

	if cfg == nil {
		return nil, fmt.Errorf("page config not found for template: %s", meta.Template)
	}

	var data any
	var subs []string
	var err error

	// Use payload if provided, otherwise call DataLoader
	if len(payload) > 0 {
		data = payload
		subs = meta.Subscriptions // Keep existing subscriptions
		hs.logger.Debug("using payload data",
			slog.String("path", meta.Path),
			slog.Int("payload_keys", len(payload)),
		)
	} else {
		// Load fresh data from DataLoader
		data, subs, err = cfg.DataLoader(ctx, meta.Params)
		if err != nil {
			return &BuildResult{
				Page:      Page{Path: meta.Path},
				Success:   false,
				Error:     err.Error(),
				Duration:  time.Since(start),
				Timestamp: time.Now(),
			}, err
		}
	}

	// Update subscriptions if changed
	if !equalSlices(subs, meta.Subscriptions) {
		meta.Subscriptions = subs
		hs.registry.Subscribe(ctx, *meta)
	}

	// Build
	result, err := hs.builder.Build(ctx, meta.Template, meta.Path, data)
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

var paramRegex = regexp.MustCompile(`\{(\w+)\}`)

func extractParams(pattern, id string) map[string]string {
	params := make(map[string]string)
	params["id"] = id

	matches := paramRegex.FindAllStringSubmatch(pattern, -1)
	for _, m := range matches {
		if m[1] == "id" {
			params["id"] = id
		}
	}

	return params
}

func buildPath(pattern string, params map[string]string) string {
	result := pattern
	for key, value := range params {
		result = strings.ReplaceAll(result, "{"+key+"}", value)
	}
	return result
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
