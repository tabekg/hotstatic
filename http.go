package hotstatic

import (
	"encoding/json"
	"fmt"
	"io"
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
	hs *HotStatic
}

// NewHTTPHandler creates an HTTP handler.
func NewHTTPHandler(hs *HotStatic) *HTTPHandler {
	return &HTTPHandler{hs: hs}
}

// Router returns an http.Handler with all routes.
func (h *HTTPHandler) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/events", h.handleEvent)
	mux.HandleFunc("POST /api/build", h.handleBuild)
	mux.HandleFunc("POST /api/build/all", h.handleBuildAll)
	mux.HandleFunc("GET /api/stats", h.handleStats)
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
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.json(w, map[string]any{
		"success": true,
		"event":   event.Key(),
	})
}

// handleBuild triggers a single page build.
// POST /api/build
// Body: {"template": "product", "id": "123"}
func (h *HTTPHandler) handleBuild(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Template string `json:"template"`
		ID       string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Template == "" {
		h.jsonError(w, "template is required", http.StatusBadRequest)
		return
	}

	h.hs.Build(req.Template, req.ID)

	h.json(w, map[string]any{
		"success":  true,
		"template": req.Template,
		"id":       req.ID,
	})
}

// handleBuildAll triggers a full rebuild.
// POST /api/build/all
func (h *HTTPHandler) handleBuildAll(w http.ResponseWriter, r *http.Request) {
	if err := h.hs.BuildAll(r.Context()); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.json(w, map[string]any{
		"success": true,
	})
}

// handleStats returns current statistics.
// GET /api/stats
func (h *HTTPHandler) handleStats(w http.ResponseWriter, r *http.Request) {
	h.json(w, h.hs.Stats())
}

// handleHealth returns health status.
// GET /api/health
func (h *HTTPHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	stats := h.hs.Stats()
	h.json(w, map[string]any{
		"status":          "healthy",
		"uptime":          stats.Uptime.String(),
		"templates_count": stats.TemplatesCount,
		"pages_built":     stats.PagesBuilt,
		"queue_length":    stats.QueueLength,
	})
}

func (h *HTTPHandler) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
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
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			io.Copy(w, content)
			return
		}
	}

	http.NotFound(w, r)
}

// StaticHandlerConfig for creating static handlers.
type StaticHandlerConfig struct {
	OutputDir    string
	NotFoundPage string
	CacheRules   []CacheRule
}

// StaticHandler creates a static file handler for HotStatic.
func (hs *HotStatic) StaticHandler(notFoundPage string, cacheRules []CacheRule) *StaticHandler {
	return NewStaticHandlerWithCache(hs.config.OutputDir, notFoundPage, cacheRules)
}
