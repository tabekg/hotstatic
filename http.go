package hotstatic

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
)

// HTTPHandler provides HTTP endpoints for HotStatic.
type HTTPHandler struct {
	hs     *HotStatic
	logger *slog.Logger
}

// NewHTTPHandler creates an HTTP handler.
func NewHTTPHandler(hs *HotStatic) *HTTPHandler {
	return &HTTPHandler{
		hs:     hs,
		logger: hs.logger,
	}
}

// Router returns an http.Handler with all routes.
func (h *HTTPHandler) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/events", h.handleEvent)
	mux.HandleFunc("POST /api/events/batch", h.handleEventBatch)
	mux.HandleFunc("POST /api/build", h.handleBuild)
	mux.HandleFunc("POST /api/build/all", h.handleBuildAll)
	mux.HandleFunc("GET /api/stats", h.handleStats)
	mux.HandleFunc("GET /api/pages", h.handleListPages)
	mux.HandleFunc("GET /api/pages/{path...}", h.handleGetPage)
	mux.HandleFunc("DELETE /api/pages/{path...}", h.handleDeletePage)
	mux.HandleFunc("GET /api/health", h.handleHealth)

	return h.withMiddleware(mux)
}

// handleEvent processes a single event.
// POST /api/events
// Body: {"type": "product", "id": "123", "action": "updated"}
func (h *HTTPHandler) handleEvent(w http.ResponseWriter, r *http.Request) {
	var event Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		h.jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	if err := h.hs.EmitEvent(event); err != nil {
		h.logger.Error("emit event failed", slog.String("error", err.Error()))
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.json(w, map[string]any{
		"success":     true,
		"key":         event.Key(),
		"subscribers": h.getSubscriberCount(event.Key()),
	})
}

// handleEventBatch processes multiple events.
// POST /api/events/batch
// Body: {"events": [{"type": "product", "id": "123", "action": "updated"}, ...]}
func (h *HTTPHandler) handleEventBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Events []Event `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	results := make([]map[string]any, len(req.Events))
	for i, event := range req.Events {
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Now()
		}

		err := h.hs.EmitEvent(event)
		results[i] = map[string]any{
			"key":     event.Key(),
			"success": err == nil,
		}
		if err != nil {
			results[i]["error"] = err.Error()
		}
	}

	h.json(w, map[string]any{
		"success": true,
		"results": results,
	})
}

// handleBuild triggers a specific page rebuild with payload.
// POST /api/build
// Body: {"path": "/products/123.html", "payload": {"Product": {...}, ...}}
func (h *HTTPHandler) handleBuild(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string         `json:"path"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if len(req.Payload) == 0 {
		h.jsonError(w, "payload is required", http.StatusBadRequest)
		return
	}

	result, err := h.hs.Build(r.Context(), req.Path, req.Payload)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.json(w, result)
}

// handleBuildAll is deprecated - without DataLoader, bulk rebuild requires external data.
// POST /api/build/all
func (h *HTTPHandler) handleBuildAll(w http.ResponseWriter, r *http.Request) {
	h.jsonError(w, "build/all is not supported without DataLoader - emit events with payloads instead", http.StatusNotImplemented)
}

// handleStats returns current statistics.
// GET /api/stats
func (h *HTTPHandler) handleStats(w http.ResponseWriter, r *http.Request) {
	h.json(w, h.hs.Stats())
}

// handleListPages returns all registered pages.
// GET /api/pages
func (h *HTTPHandler) handleListPages(w http.ResponseWriter, r *http.Request) {
	pages, err := h.hs.Registry().ListPages(r.Context())
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.json(w, map[string]any{
		"pages": pages,
		"count": len(pages),
	})
}

// handleGetPage returns page metadata.
// GET /api/pages/{path...}
func (h *HTTPHandler) handleGetPage(w http.ResponseWriter, r *http.Request) {
	path := "/" + r.PathValue("path")

	meta, err := h.hs.Registry().GetPage(r.Context(), path)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if meta == nil {
		h.jsonError(w, "page not found", http.StatusNotFound)
		return
	}

	h.json(w, meta)
}

// handleDeletePage removes a page.
// DELETE /api/pages/{path...}
func (h *HTTPHandler) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	path := "/" + r.PathValue("path")

	if err := h.hs.Unsubscribe(r.Context(), path); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Also delete the file
	h.hs.Builder().Delete(r.Context(), path)

	h.json(w, map[string]any{
		"success": true,
		"path":    path,
	})
}

// handleHealth returns health status.
// GET /api/health
func (h *HTTPHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	stats := h.hs.Stats()
	h.json(w, map[string]any{
		"status":         "healthy",
		"uptime":         stats.Uptime.String(),
		"pages_total":    stats.PagesTotal,
		"queue_length":   stats.QueueLength,
		"workers_active": stats.WorkersActive,
	})
}

func (h *HTTPHandler) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Serve request
		next.ServeHTTP(w, r)

		h.logger.Debug("http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

func (h *HTTPHandler) json(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *HTTPHandler) jsonError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"error": message,
	})
}

func (h *HTTPHandler) getSubscriberCount(key string) int {
	subs, _ := h.hs.Registry().GetSubscribers(h.hs.ctx, key)
	return len(subs)
}

// CacheRule defines caching behavior for files matching a pattern.
type CacheRule struct {
	Pattern        string // regex for URL/path
	MaxAge         int    // seconds (0 = no-cache)
	Immutable      bool   // add immutable directive
	MustRevalidate bool   // add must-revalidate directive
	Private        bool   // use private instead of public

	compiled *regexp.Regexp
}

// StaticHandler serves generated static files with custom 404 page and caching support.
type StaticHandler struct {
	outputDir    string
	notFoundPage string
	cacheRules   []CacheRule
}

// NewStaticHandler creates a static file server.
// notFoundPage is the path to custom 404 page relative to outputDir (e.g., "404.html").
// If empty, standard 404 response is returned.
func NewStaticHandler(outputDir string, notFoundPage string) *StaticHandler {
	return &StaticHandler{
		outputDir:    outputDir,
		notFoundPage: notFoundPage,
	}
}

// NewStaticHandlerWithCache creates a static file server with cache rules.
func NewStaticHandlerWithCache(outputDir string, notFoundPage string, cacheRules []CacheRule) *StaticHandler {
	// Compile regex patterns
	compiled := make([]CacheRule, len(cacheRules))
	for i, rule := range cacheRules {
		compiled[i] = rule
		if rule.Pattern != "" {
			compiled[i].compiled, _ = regexp.Compile(rule.Pattern)
		}
	}

	return &StaticHandler{
		outputDir:    outputDir,
		notFoundPage: notFoundPage,
		cacheRules:   compiled,
	}
}

// StaticHandler returns a static file handler using config from HotStatic.
func (hs *HotStatic) StaticHandler() *StaticHandler {
	return NewStaticHandlerWithCache(hs.config.OutputDir, hs.config.NotFoundPage, hs.config.CacheRules)
}

// FileServerWithCache returns a file server handler with cache rules applied.
// Use this for serving static assets (JS, CSS, images) with proper caching.
// Example: mux.Handle("/static/", http.StripPrefix("/static/", hs.FileServerWithCache("./dist/static")))
func (hs *HotStatic) FileServerWithCache(dir string) http.Handler {
	return NewStaticHandlerWithCache(dir, "", hs.config.CacheRules)
}

// ServeHTTP implements http.Handler.
func (s *StaticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	fullPath := s.outputDir + path

	// Check if file exists
	file, err := os.Open(fullPath)
	if err != nil {
		s.serve404(w, r)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		s.serve404(w, r)
		return
	}

	// Generate ETag based on file size and mod time
	etag := s.generateETag(stat)
	w.Header().Set("ETag", etag)

	// Check If-None-Match
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// Apply cache rules
	s.applyCacheHeaders(w, path)

	http.ServeFile(w, r, fullPath)
}

func (s *StaticHandler) generateETag(stat os.FileInfo) string {
	hash := xxhash.Sum64String(fmt.Sprintf("%d-%d", stat.Size(), stat.ModTime().UnixNano()))
	return fmt.Sprintf(`"%x"`, hash)
}

func (s *StaticHandler) applyCacheHeaders(w http.ResponseWriter, path string) {
	for _, rule := range s.cacheRules {
		if rule.compiled != nil && rule.compiled.MatchString(path) {
			var parts []string

			if rule.Private {
				parts = append(parts, "private")
			} else {
				parts = append(parts, "public")
			}

			if rule.MaxAge > 0 {
				parts = append(parts, "max-age="+strconv.Itoa(rule.MaxAge))
			} else {
				parts = append(parts, "no-cache")
			}

			if rule.Immutable {
				parts = append(parts, "immutable")
			}

			if rule.MustRevalidate {
				parts = append(parts, "must-revalidate")
			}

			w.Header().Set("Cache-Control", strings.Join(parts, ", "))
			return
		}
	}
}

func (s *StaticHandler) serve404(w http.ResponseWriter, r *http.Request) {
	if s.notFoundPage != "" {
		notFoundPath := s.outputDir + "/" + s.notFoundPage
		if content, err := os.Open(notFoundPath); err == nil {
			defer content.Close()
			stat, err := content.Stat()
			if err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusNotFound)
				io.Copy(w, content)
				_ = stat // used for potential future Content-Length
				return
			}
		}
	}

	http.NotFound(w, r)
}

// Webhook provides webhook integration.
type Webhook struct {
	hs     *HotStatic
	secret string
}

// NewWebhook creates a webhook handler.
func NewWebhook(hs *HotStatic, secret string) *Webhook {
	return &Webhook{
		hs:     hs,
		secret: secret,
	}
}

// Handler returns the webhook HTTP handler.
func (wh *Webhook) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Verify secret if configured
		if wh.secret != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+wh.secret {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		var payload struct {
			Type   string         `json:"type"`
			ID     string         `json:"id"`
			Action string         `json:"action"`
			Data   map[string]any `json:"data"`
		}

		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}

		event := Event{
			Type:      payload.Type,
			ID:        payload.ID,
			Action:    payload.Action,
			Timestamp: time.Now(),
			Metadata:  payload.Data,
		}

		if err := wh.hs.EmitEvent(event); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}
