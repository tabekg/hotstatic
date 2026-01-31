package worker

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Job represents a unit of work.
type Job struct {
	// ID unique identifier
	ID string

	// Path of page to rebuild
	Path string

	// Priority level
	Priority int

	// TriggerKey that caused this job
	TriggerKey string

	// Data to pass to handler
	Data any
}

// Result represents the outcome of a job.
type Result struct {
	Job       Job
	Success   bool
	Error     error
	Duration  time.Duration
	Timestamp time.Time
}

// Handler processes jobs.
type Handler func(ctx context.Context, job Job) error

// Pool manages a pool of workers.
type Pool struct {
	numWorkers  int
	handler     Handler
	jobs        chan Job
	results     chan Result
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *slog.Logger
	activeCount int64
	totalJobs   int64
	failedJobs  int64
}

// Config for worker pool.
type Config struct {
	// NumWorkers is the number of concurrent workers
	NumWorkers int

	// QueueSize is the job queue buffer size
	QueueSize int

	// Logger for worker output
	Logger *slog.Logger
}

// NewPool creates a new worker pool.
func NewPool(cfg Config, handler Handler) *Pool {
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = 1
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1000
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Pool{
		numWorkers: cfg.NumWorkers,
		handler:    handler,
		jobs:       make(chan Job, cfg.QueueSize),
		results:    make(chan Result, cfg.QueueSize),
		ctx:        ctx,
		cancel:     cancel,
		logger:     cfg.Logger,
	}

	return p
}

// Start launches all workers.
func (p *Pool) Start() {
	for i := 0; i < p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// Submit adds a job to the queue.
// Returns false if the pool is shutting down.
func (p *Pool) Submit(job Job) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.jobs <- job:
		return true
	}
}

// SubmitWithTimeout adds a job with timeout.
func (p *Pool) SubmitWithTimeout(job Job, timeout time.Duration) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.jobs <- job:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Results returns the results channel.
func (p *Pool) Results() <-chan Result {
	return p.results
}

// Stop gracefully shuts down the pool.
func (p *Pool) Stop() {
	p.cancel()
	close(p.jobs)
	p.wg.Wait()
	close(p.results)
}

// StopWait stops and waits for all jobs to complete.
func (p *Pool) StopWait(timeout time.Duration) bool {
	p.cancel()
	close(p.jobs)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		close(p.results)
		return true
	case <-time.After(timeout):
		return false
	}
}

// Stats returns pool statistics.
func (p *Pool) Stats() Stats {
	return Stats{
		NumWorkers:  p.numWorkers,
		ActiveCount: int(atomic.LoadInt64(&p.activeCount)),
		TotalJobs:   atomic.LoadInt64(&p.totalJobs),
		FailedJobs:  atomic.LoadInt64(&p.failedJobs),
		QueueLength: len(p.jobs),
	}
}

// Stats contains pool statistics.
type Stats struct {
	NumWorkers  int
	ActiveCount int
	TotalJobs   int64
	FailedJobs  int64
	QueueLength int
}

func (p *Pool) worker(id int) {
	defer p.wg.Done()

	for job := range p.jobs {
		atomic.AddInt64(&p.activeCount, 1)
		atomic.AddInt64(&p.totalJobs, 1)

		start := time.Now()
		err := p.handler(p.ctx, job)
		duration := time.Since(start)

		if err != nil {
			atomic.AddInt64(&p.failedJobs, 1)
			p.logger.Error("job failed",
				slog.Int("worker", id),
				slog.String("path", job.Path),
				slog.String("error", err.Error()),
				slog.Duration("duration", duration),
			)
		} else {
			p.logger.Debug("job completed",
				slog.Int("worker", id),
				slog.String("path", job.Path),
				slog.Duration("duration", duration),
			)
		}

		result := Result{
			Job:       job,
			Success:   err == nil,
			Error:     err,
			Duration:  duration,
			Timestamp: time.Now(),
		}

		select {
		case p.results <- result:
		default:
			// Results channel full, skip
		}

		atomic.AddInt64(&p.activeCount, -1)
	}
}

// Simple is a simplified worker pool for quick usage.
type Simple struct {
	pool *Pool
}

// NewSimple creates a simple worker pool.
func NewSimple(workers int, handler Handler) *Simple {
	s := &Simple{
		pool: NewPool(Config{
			NumWorkers: workers,
			QueueSize:  10000,
		}, handler),
	}
	s.pool.Start()
	return s
}

// Do submits a job and waits for completion.
func (s *Simple) Do(ctx context.Context, job Job) error {
	done := make(chan error, 1)

	// Wrap handler to capture result
	go func() {
		s.pool.Submit(job)
		for result := range s.pool.Results() {
			if result.Job.ID == job.ID {
				done <- result.Error
				return
			}
		}
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Submit adds a job asynchronously.
func (s *Simple) Submit(job Job) {
	s.pool.Submit(job)
}

// Stop shuts down the pool.
func (s *Simple) Stop() {
	s.pool.Stop()
}
