package hotstatic

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// HotStatic is the main framework instance.
type HotStatic struct {
	config    Config
	templates map[string]*TemplateDef
	builder   Builder

	// Queue and workers
	queue     chan buildJob
	pending   map[string]time.Time // for debounce
	pendingMu sync.Mutex
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	started   bool

	// Stats for progress logging
	stats struct {
		built    atomic.Int64
		errors   atomic.Int64
		drained  atomic.Bool
		startAt  time.Time
	}

	mu sync.RWMutex
}

type buildJob struct {
	template string
	id       string
}

func (j buildJob) key() string {
	if j.id == "" {
		return j.template
	}
	return j.template + ":" + j.id
}

// Builder interface for template rendering.
type Builder interface {
	Build(ctx context.Context, templateFile, outputPath string, data map[string]any) error
}

// New creates a new HotStatic instance.
func New(cfg Config) *HotStatic {
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./dist"
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = time.Second
	}
	if cfg.ProgressInterval <= 0 {
		cfg.ProgressInterval = 500
	}
	if cfg.Logger == nil {
		cfg.Logger = &slogAdapter{slog.Default()}
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &HotStatic{
		config:    cfg,
		templates: make(map[string]*TemplateDef),
		queue:     make(chan buildJob, 10000),
		pending:   make(map[string]time.Time),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// DefineTemplate registers a template definition.
func (hs *HotStatic) DefineTemplate(name string, def TemplateDef) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.templates[name] = &def
}

// SetBuilder sets the template builder.
func (hs *HotStatic) SetBuilder(builder Builder) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.builder = builder
}

// Build builds a single page synchronously.
func (hs *HotStatic) Build(ctx context.Context, template string, id string) error {
	start := time.Now()
	if err := hs.buildPage(ctx, template, id); err != nil {
		return err
	}
	hs.config.Logger.Debug("built",
		"template", template,
		"id", id,
		"duration", time.Since(start).Round(time.Millisecond),
	)
	return nil
}

// Queue adds a page to the build queue (async, with debounce).
// Call Start() first to start workers.
func (hs *HotStatic) Queue(template string, id string) {
	job := buildJob{template: template, id: id}
	key := job.key()

	hs.pendingMu.Lock()
	lastQueued, exists := hs.pending[key]
	now := time.Now()

	// Debounce: skip if same page was queued recently
	if exists && now.Sub(lastQueued) < hs.config.Debounce {
		hs.pendingMu.Unlock()
		hs.config.Logger.Debug("debounced", "template", template, "id", id)
		return
	}

	hs.pending[key] = now
	hs.pendingMu.Unlock()

	// Reset drained flag when new work arrives
	hs.stats.drained.Store(false)

	// Non-blocking send to queue
	select {
	case hs.queue <- job:
	default:
		hs.config.Logger.Warn("queue full, dropping job", "template", template, "id", id)
	}
}

// Start starts workers for processing the queue.
func (hs *HotStatic) Start() {
	hs.mu.Lock()
	if hs.started {
		hs.mu.Unlock()
		return
	}
	hs.started = true
	hs.mu.Unlock()

	hs.stats.built.Store(0)
	hs.stats.errors.Store(0)
	hs.stats.drained.Store(false)
	hs.stats.startAt = time.Now()

	for i := 0; i < hs.config.Workers; i++ {
		hs.wg.Add(1)
		go hs.worker()
	}

	hs.config.Logger.Info("queue started", "workers", hs.config.Workers)
}

// Stop stops workers gracefully.
func (hs *HotStatic) Stop() {
	hs.cancel()
	close(hs.queue)
	hs.wg.Wait()

	built := hs.stats.built.Load()
	errors := hs.stats.errors.Load()
	if built > 0 || errors > 0 {
		hs.config.Logger.Info("queue stopped",
			"built", built,
			"errors", errors,
			"duration", time.Since(hs.stats.startAt).Round(time.Millisecond),
		)
	}
}

func (hs *HotStatic) worker() {
	defer hs.wg.Done()

	for job := range hs.queue {
		select {
		case <-hs.ctx.Done():
			return
		default:
		}

		if err := hs.buildPage(hs.ctx, job.template, job.id); err != nil {
			hs.stats.errors.Add(1)
			hs.config.Logger.Error("build failed",
				"template", job.template,
				"id", job.id,
				"error", err.Error(),
			)
		} else {
			n := hs.stats.built.Add(1)
			if n%hs.config.ProgressInterval == 0 {
				hs.config.Logger.Info("progress",
					"built", n,
					"errors", hs.stats.errors.Load(),
					"queued", len(hs.queue),
					"elapsed", time.Since(hs.stats.startAt).Round(time.Millisecond),
				)
			}
		}

		// Log once when queue drains
		if len(hs.queue) == 0 && !hs.stats.drained.Swap(true) {
			hs.config.Logger.Info("queue drained",
				"built", hs.stats.built.Load(),
				"errors", hs.stats.errors.Load(),
				"elapsed", time.Since(hs.stats.startAt).Round(time.Millisecond),
			)
		}

		// Clear from pending
		hs.pendingMu.Lock()
		delete(hs.pending, job.key())
		hs.pendingMu.Unlock()
	}
}

// BuildAll builds all pages for all templates.
func (hs *HotStatic) BuildAll(ctx context.Context) error {
	start := time.Now()

	hs.mu.RLock()
	templates := make(map[string]*TemplateDef)
	for k, v := range hs.templates {
		templates[k] = v
	}
	hs.mu.RUnlock()

	var totalPages int

	for name, def := range templates {
		if def.LoadAll == nil {
			continue
		}

		ids, err := def.LoadAll(ctx)
		if err != nil {
			return fmt.Errorf("LoadAll for %s: %w", name, err)
		}

		hs.config.Logger.Info("building template", "template", name, "count", len(ids))

		for _, id := range ids {
			if err := hs.buildPage(ctx, name, id); err != nil {
				hs.config.Logger.Error("build failed",
					"template", name,
					"id", id,
					"error", err.Error(),
				)
			} else {
				totalPages++
			}
		}
	}

	hs.config.Logger.Info("build complete", "pages", totalPages, "duration", time.Since(start))

	return nil
}

// Delete removes a generated page by path.
func (hs *HotStatic) Delete(path string) error {
	fullPath := filepath.Join(hs.config.OutputDir, path)

	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	hs.config.Logger.Info("deleted page", "path", path)

	return nil
}

// GetTemplate returns a template definition by name.
func (hs *HotStatic) GetTemplate(name string) (*TemplateDef, bool) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	def, ok := hs.templates[name]
	return def, ok
}

func (hs *HotStatic) buildPage(ctx context.Context, template string, id string) error {
	hs.mu.RLock()
	def, ok := hs.templates[template]
	builder := hs.builder
	hs.mu.RUnlock()

	if !ok {
		return fmt.Errorf("template not found: %s", template)
	}

	if builder == nil {
		return fmt.Errorf("no builder set")
	}

	if def.Load == nil {
		return fmt.Errorf("template %s has no Load function", template)
	}

	// Load page data
	pageData, err := def.Load(ctx, id)
	if err != nil {
		return fmt.Errorf("load data: %w", err)
	}

	if pageData == nil {
		hs.config.Logger.Debug("skipped (no data)", "template", template, "id", id)
		return nil
	}

	// Build page
	if err := builder.Build(ctx, def.File, pageData.Path, pageData.Data); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	return nil
}

// slogAdapter wraps slog.Logger to implement Logger interface.
type slogAdapter struct {
	*slog.Logger
}

func (s *slogAdapter) Debug(msg string, args ...any) { s.Logger.Debug(msg, args...) }
func (s *slogAdapter) Info(msg string, args ...any)  { s.Logger.Info(msg, args...) }
func (s *slogAdapter) Warn(msg string, args ...any)  { s.Logger.Warn(msg, args...) }
func (s *slogAdapter) Error(msg string, args ...any) { s.Logger.Error(msg, args...) }
