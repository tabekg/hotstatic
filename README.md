# HotStatic

A universal Static Site Generator (SSG) framework with reactive rebuild capabilities. When any data changes, all dependent pages are automatically rebuilt.

## Features

- **Reactive Rebuilds** — Pages subscribe to data keys; when data changes, only affected pages rebuild
- **Universal** — Not tied to any specific domain; works for e-commerce, news sites, travel, cinema, etc.
- **Fast** — Built with Go, Redis, and parallel worker pools
- **Simple API** — Minimal integration effort with intuitive patterns
- **Scalable** — Handles millions of pages and thousands of events per second
- **Priority Queue** — Urgent pages rebuild first
- **HTTP API** — REST endpoints for events, webhooks, and monitoring

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      HotStatic                          │
├─────────────────────────────────────────────────────────┤
│  Event          Data change notification                │
│       ↓                                                 │
│  Registry       Redis-based subscription management     │
│       ↓                                                 │
│  Queue          Priority queue for rebuild tasks        │
│       ↓                                                 │
│  Workers        Parallel page builders                  │
│       ↓                                                 │
│  Builder        Template rendering & file output        │
└─────────────────────────────────────────────────────────┘
```

## Installation

```bash
go get github.com/tabekg/hotstatic
```

**Requirements:**
- Go 1.21+
- Redis 6+

## Quick Start

```go
package main

import (
    "context"
    "log"

    "github.com/tabekg/hotstatic"
)

func main() {
    // Initialize HotStatic
    hs, err := hotstatic.New(hotstatic.Config{
        Redis:     "localhost:6379",
        OutputDir: "./dist",
        Workers:   10,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer hs.Stop()

    // Register a page template
    hs.RegisterTemplate("product", `
        <html>
        <head><title>{{.Name}}</title></head>
        <body>
            <h1>{{.Name}}</h1>
            <p>Price: ${{.Price}}</p>
        </body>
        </html>
    `)

    // Register page configuration
    hs.RegisterPage("product-detail", hotstatic.PageConfig{
        PathPattern: "/products/{id}.html",
        Template:    "product",
        DataLoader: func(ctx context.Context, params map[string]string) (any, []string, error) {
            // Fetch your data from any source (DB, API, etc.)
            product := getProduct(params["id"])

            // Return data and subscription keys
            subscriptions := []string{
                "product:" + product.ID,
                "brand:" + product.BrandID,
                "category:" + product.CategoryID,
            }

            return product, subscriptions, nil
        },
    })

    // Start processing
    hs.Start()

    // Generate initial pages
    hs.GeneratePages(context.Background(), "product-detail", []string{"1", "2", "3"})

    // When data changes, emit an event
    // All pages subscribed to "product:123" will rebuild automatically
    hs.Emit("product:123", "updated")
}
```

## Core Concepts

### Events

Events represent data changes that may trigger page rebuilds:

```go
type Event struct {
    Type      string         // Entity type: "product", "article", "ad"
    ID        string         // Entity identifier
    Action    string         // "created", "updated", "deleted"
    Priority  int            // Higher = more urgent
    Metadata  map[string]any // Additional context
}

// Emit using key format
hs.Emit("product:123", "updated")

// Or emit full event
hs.EmitEvent(hotstatic.Event{
    Type:     "product",
    ID:       "123",
    Action:   "updated",
    Priority: 10,
})
```

### Pages & Subscriptions

Pages subscribe to data keys. When an event matches a subscription, the page rebuilds:

```go
hs.RegisterPage("product-detail", hotstatic.PageConfig{
    PathPattern: "/products/{id}.html",
    Template:    "product.html",
    DataLoader: func(ctx context.Context, params map[string]string) (any, []string, error) {
        product := db.GetProduct(params["id"])

        // This page depends on:
        subscriptions := []string{
            "product:" + product.ID,       // The product itself
            "brand:" + product.BrandID,    // Its brand (name/logo changes)
            "category:" + product.CategoryID, // Its category
            "currency:usd",                // Currency rates
        }

        return product, subscriptions, nil
    },
})
```

### DataLoader

The `DataLoader` function is called during page generation and rebuild:

```go
DataLoader: func(ctx context.Context, params map[string]string) (data any, subscriptions []string, err error)
```

- `params` — Extracted from `PathPattern` (e.g., `{id}` → `params["id"]`)
- `data` — Passed to the template for rendering
- `subscriptions` — Keys this page depends on
- Returns fresh data on every rebuild

## Use Cases

| Project | Entities |
|---------|----------|
| E-commerce | product, brand, category, stock, price |
| Classifieds | ad, seller, category, city, price |
| News Site | article, author, tag, section |
| Travel | tour, hotel, destination, guide |
| Cinema | movie, cinema, showtime, actor |

## HTTP API

Start the HTTP server:

```go
handler := hotstatic.NewHTTPHandler(hs)
http.ListenAndServe(":8080", handler.Router())
```

### Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/events` | Emit single event |
| POST | `/api/events/batch` | Emit multiple events |
| POST | `/api/build` | Rebuild specific page |
| POST | `/api/build/all` | Rebuild all pages |
| GET | `/api/stats` | Get statistics |
| GET | `/api/pages` | List all pages |
| GET | `/api/pages/{path}` | Get page metadata |
| DELETE | `/api/pages/{path}` | Delete a page |
| GET | `/api/health` | Health check |

### Examples

**Emit an event:**
```bash
curl -X POST http://localhost:8080/api/events \
  -H "Content-Type: application/json" \
  -d '{"type": "product", "id": "123", "action": "updated"}'
```

**Batch events:**
```bash
curl -X POST http://localhost:8080/api/events/batch \
  -H "Content-Type: application/json" \
  -d '{
    "events": [
      {"type": "product", "id": "1", "action": "updated"},
      {"type": "product", "id": "2", "action": "updated"},
      {"type": "brand", "id": "apple", "action": "updated"}
    ]
  }'
```

**Get statistics:**
```bash
curl http://localhost:8080/api/stats
```

Response:
```json
{
  "pages_total": 15420,
  "pages_built": 342,
  "pages_failed": 2,
  "events_processed": 1893,
  "queue_length": 12,
  "workers_active": 8,
  "uptime": "2h15m30s"
}
```

## Webhooks

Integrate with external systems:

```go
webhook := hotstatic.NewWebhook(hs, "your-secret-token")
http.Handle("/webhook", webhook.Handler())
```

Send events from external services:
```bash
curl -X POST http://localhost:8080/webhook \
  -H "Authorization: Bearer your-secret-token" \
  -H "Content-Type: application/json" \
  -d '{"type": "product", "id": "123", "action": "updated"}'
```

## Configuration

```go
hotstatic.Config{
    // Redis connection
    Redis:         "localhost:6379",
    RedisPassword: "",
    RedisDB:       0,
    RedisPrefix:   "myapp", // Key namespace

    // Templates
    TemplateDir:   "./templates",

    // Output
    OutputDir:     "./dist",

    // Performance
    Workers:       10,      // Parallel workers
    QueueSize:     10000,   // Queue buffer

    // Logging
    Logger:        slog.Default(),

    // Custom template functions
    FuncMap:       template.FuncMap{
        "formatPrice": formatPrice,
    },
}
```

## Template Functions

Built-in template functions:

| Function | Description | Example |
|----------|-------------|---------|
| `safe` | Mark HTML as safe | `{{.HTML \| safe}}` |
| `join` | Join strings | `{{join .Tags ", "}}` |
| `split` | Split string | `{{split .Tags ","}}` |
| `lower` | Lowercase | `{{.Name \| lower}}` |
| `upper` | Uppercase | `{{.Name \| upper}}` |
| `title` | Title case | `{{.Name \| title}}` |
| `trim` | Trim whitespace | `{{.Name \| trim}}` |
| `default` | Default value | `{{default "N/A" .Value}}` |
| `dict` | Create map | `{{dict "key" "value"}}` |
| `list` | Create list | `{{list 1 2 3}}` |

Add custom functions:

```go
hs.Builder().SetFuncMap(template.FuncMap{
    "formatPrice": func(price float64) string {
        return fmt.Sprintf("$%.2f", price)
    },
    "formatDate": func(t time.Time) string {
        return t.Format("Jan 2, 2006")
    },
})
```

## Advanced Usage

### Conditional Page Generation

```go
hs.RegisterPage("product-detail", hotstatic.PageConfig{
    PathPattern: "/products/{id}.html",
    Template:    "product.html",
    Condition: func(params map[string]string) bool {
        // Skip draft products
        product := db.GetProduct(params["id"])
        return product.Status == "published"
    },
    DataLoader: func(ctx context.Context, params map[string]string) (any, []string, error) {
        // ...
    },
})
```

### Priority Rebuilds

```go
// High-priority event (e.g., price change)
hs.EmitEvent(hotstatic.Event{
    Type:     "product",
    ID:       "123",
    Action:   "price_changed",
    Priority: 100, // Higher = rebuild sooner
})

// Normal priority
hs.Emit("product:456", "updated") // Priority: 0
```

### Manual Page Management

```go
ctx := context.Background()

// Subscribe a page manually
hs.Subscribe(ctx, hotstatic.Page{
    Path:          "/custom/page.html",
    Template:      "custom.html",
    Params:        map[string]string{"id": "123"},
    Subscriptions: []string{"data:123", "config:global"},
})

// Unsubscribe (remove page)
hs.Unsubscribe(ctx, "/custom/page.html")

// Build specific page
result, err := hs.Build(ctx, "/products/123.html")

// Rebuild all pages
hs.BuildAll(ctx)
```

### Access Internal Components

```go
// Get registry for direct Redis operations
registry := hs.Registry()
subscribers, _ := registry.GetSubscribers(ctx, "brand:apple")

// Get builder for direct template operations
builder := hs.Builder()
content, _ := builder.Render(ctx, "product.html", data)
```

## Project Structure

```
hotstatic/
├── go.mod
├── types.go           # Core types: Event, Page, interfaces
├── hotstatic.go       # Main API
├── http.go            # HTTP API & webhooks
├── pkg/
│   ├── registry/      # Redis subscription management
│   ├── builder/       # Template rendering
│   ├── queue/         # Priority rebuild queue
│   └── worker/        # Parallel worker pool
└── examples/
    └── basic/         # Example application
```

## Performance Tips

1. **Use appropriate worker count** — Start with `NumCPU * 2`, adjust based on I/O
2. **Set priorities** — Critical pages (homepage, popular products) get higher priority
3. **Batch events** — Use `/api/events/batch` for bulk updates
4. **Monitor queue length** — If consistently high, add workers
5. **Use Redis cluster** — For very high throughput scenarios

## License

MIT License
