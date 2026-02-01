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
	config       Config
	templates    map[string]*TemplateDef
	eventHandler EventHandler
	builder      Builder

	// Queue and workers
	queue     chan BuildJob
	pending   map[string]time.Time // for debounce: key -> last queued time
	pendingMu sync.Mutex
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	started   bool
	startTime time.Time

	// Metrics
	pagesBuilt      int64
	pagesFailed     int64
	eventsProcessed int64

	mu sync.RWMutex
}

// Builder interface for template rendering.
type Builder interface {
	Build(ctx context.Context, templateFile, outputPath string, data map[string]any) error
	Delete(outputPath string) error
}

// New creates a new HotStatic instance.
func New(cfg Config) *HotStatic {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = time.Second
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./dist"
	}
	if cfg.Logger == nil {
		cfg.Logger = &slogAdapter{slog.Default()}
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &HotStatic{
		config:    cfg,
		templates: make(map[string]*TemplateDef),
		queue:     make(chan BuildJob, 10000),
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

// OnEvent sets the event handler.
func (hs *HotStatic) OnEvent(handler EventHandler) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.eventHandler = handler
}

// SetBuilder sets the template builder.
func (hs *HotStatic) SetBuilder(builder Builder) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.builder = builder
}

// Build queues a page for building.
func (hs *HotStatic) Build(template string, id string) {
	hs.queueBuild(BuildJob{
		Template:  template,
		ID:        id,
		CreatedAt: time.Now(),
	})
}

// BuildWithPriority queues a page for building with priority.
func (hs *HotStatic) BuildWithPriority(template string, id string, priority int) {
	hs.queueBuild(BuildJob{
		Template:  template,
		ID:        id,
		Priority:  priority,
		CreatedAt: time.Now(),
	})
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

		hs.config.Logger.Info("building template",
			"template", name,
			"count", len(ids),
		)

		for _, id := range ids {
			if err := hs.buildPage(ctx, name, id); err != nil {
				hs.config.Logger.Error("build failed",
					"template", name,
					"id", id,
					"error", err.Error(),
				)
				atomic.AddInt64(&hs.pagesFailed, 1)
			} else {
				atomic.AddInt64(&hs.pagesBuilt, 1)
				totalPages++
			}
		}
	}

	hs.config.Logger.Info("build complete",
		"pages", totalPages,
		"duration", time.Since(start),
	)

	return nil
}

// Emit sends an event to be processed by the event handler.
func (hs *HotStatic) Emit(eventType, id, action string) error {
	return hs.EmitEvent(Event{
		Type:      eventType,
		ID:        id,
		Action:    action,
		Timestamp: time.Now(),
	})
}

// EmitEvent sends an event to be processed by the event handler.
func (hs *HotStatic) EmitEvent(event Event) error {
	atomic.AddInt64(&hs.eventsProcessed, 1)

	hs.mu.RLock()
	handler := hs.eventHandler
	hs.mu.RUnlock()

	if handler == nil {
		hs.config.Logger.Warn("no event handler set, ignoring event",
			"type", event.Type,
			"id", event.ID,
			"action", event.Action,
		)
		return nil
	}

	hs.config.Logger.Debug("event received",
		"type", event.Type,
		"id", event.ID,
		"action", event.Action,
	)

	return handler(hs.ctx, event)
}

// Start begins processing the build queue with workers.
func (hs *HotStatic) Start() {
	hs.mu.Lock()
	if hs.started {
		hs.mu.Unlock()
		return
	}
	hs.started = true
	hs.startTime = time.Now()
	hs.mu.Unlock()

	// Start workers
	for i := 0; i < hs.config.Workers; i++ {
		hs.wg.Add(1)
		go hs.worker(i)
	}

	hs.config.Logger.Info("started",
		"workers", hs.config.Workers,
		"debounce", hs.config.Debounce,
	)
}

// Stop gracefully shuts down HotStatic.
func (hs *HotStatic) Stop() {
	hs.cancel()
	close(hs.queue)
	hs.wg.Wait()

	hs.config.Logger.Info("stopped")
}

// Stats returns current statistics.
func (hs *HotStatic) Stats() Stats {
	hs.mu.RLock()
	templatesCount := len(hs.templates)
	hs.mu.RUnlock()

	var uptime time.Duration
	if !hs.startTime.IsZero() {
		uptime = time.Since(hs.startTime)
	}

	return Stats{
		TemplatesCount:  templatesCount,
		PagesBuilt:      atomic.LoadInt64(&hs.pagesBuilt),
		PagesFailed:     atomic.LoadInt64(&hs.pagesFailed),
		EventsProcessed: atomic.LoadInt64(&hs.eventsProcessed),
		QueueLength:     len(hs.queue),
		WorkersActive:   hs.config.Workers,
		Uptime:          uptime,
	}
}

// GetTemplate returns a template definition by name.
func (hs *HotStatic) GetTemplate(name string) (*TemplateDef, bool) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	def, ok := hs.templates[name]
	return def, ok
}

// queueBuild adds a job to the queue with debouncing.
func (hs *HotStatic) queueBuild(job BuildJob) {
	key := job.Key()

	hs.pendingMu.Lock()
	lastQueued, exists := hs.pending[key]
	now := time.Now()

	// Debounce: skip if same page was queued recently
	if exists && now.Sub(lastQueued) < hs.config.Debounce {
		hs.pendingMu.Unlock()
		hs.config.Logger.Debug("debounced",
			"template", job.Template,
			"id", job.ID,
		)
		return
	}

	hs.pending[key] = now
	hs.pendingMu.Unlock()

	// Non-blocking send to queue
	select {
	case hs.queue <- job:
	default:
		hs.config.Logger.Warn("queue full, dropping job",
			"template", job.Template,
			"id", job.ID,
		)
	}
}

// worker processes jobs from the queue.
func (hs *HotStatic) worker(id int) {
	defer hs.wg.Done()

	for job := range hs.queue {
		select {
		case <-hs.ctx.Done():
			return
		default:
		}

		if err := hs.buildPage(hs.ctx, job.Template, job.ID); err != nil {
			hs.config.Logger.Error("build failed",
				"worker", id,
				"template", job.Template,
				"id", job.ID,
				"error", err.Error(),
			)
			atomic.AddInt64(&hs.pagesFailed, 1)
		} else {
			atomic.AddInt64(&hs.pagesBuilt, 1)
		}

		// Clear from pending after build
		hs.pendingMu.Lock()
		delete(hs.pending, job.Key())
		hs.pendingMu.Unlock()
	}
}

// buildPage builds a single page.
func (hs *HotStatic) buildPage(ctx context.Context, template string, id string) error {
	start := time.Now()

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
		// nil means skip (e.g., deleted or inactive entity)
		hs.config.Logger.Debug("skipped (no data)",
			"template", template,
			"id", id,
		)
		return nil
	}

	// Build page
	if err := builder.Build(ctx, def.File, pageData.Path, pageData.Data); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	hs.config.Logger.Debug("built page",
		"template", template,
		"id", id,
		"output", pageData.Path,
		"duration", time.Since(start),
	)

	return nil
}

// slogAdapter wraps slog.Logger to implement Logger interface.
type slogAdapter struct {
	*slog.Logger
}

func (s *slogAdapter) Debug(msg string, args ...any) {
	s.Logger.Debug(msg, args...)
}

func (s *slogAdapter) Info(msg string, args ...any) {
	s.Logger.Info(msg, args...)
}

func (s *slogAdapter) Warn(msg string, args ...any) {
	s.Logger.Warn(msg, args...)
}

func (s *slogAdapter) Error(msg string, args ...any) {
	s.Logger.Error(msg, args...)
}
