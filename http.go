package hotstatic

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
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

// handleBuild triggers a specific page rebuild.
// POST /api/build
// Body: {"path": "/products/123.html"}
func (h *HTTPHandler) handleBuild(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	result, err := h.hs.Build(r.Context(), req.Path)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.json(w, result)
}

// handleBuildAll triggers rebuild of all pages.
// POST /api/build/all
func (h *HTTPHandler) handleBuildAll(w http.ResponseWriter, r *http.Request) {
	if err := h.hs.BuildAll(r.Context()); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.json(w, map[string]any{
		"success": true,
		"message": "rebuild queued",
	})
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
		"status":        "healthy",
		"uptime":        stats.Uptime.String(),
		"pages_total":   stats.PagesTotal,
		"queue_length":  stats.QueueLength,
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
