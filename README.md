# HotStatic

A universal Static Site Generator (SSG) framework with reactive rebuild capabilities. When any data changes, all dependent pages are automatically rebuilt.

## Features

- **Reactive Rebuilds** — Pages subscribe to data keys; when data changes, only affected pages rebuild
- **Django/Jinja2 Templates** — Familiar syntax with `{% extends %}`, `{% block %}`, `{% include %}`, `{% for %}`, `{% if %}`
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
    // Initialize HotStatic with pongo2 (Django/Jinja2 templates)
    hs, err := hotstatic.NewWithPongo(hotstatic.Config{
        Redis:       "localhost:6379",
        TemplateDir: "./templates",
        OutputDir:   "./dist",
        Workers:     10,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer hs.Stop()

    // Register page configuration
    hs.RegisterPongoPage("product-detail", hotstatic.PongoPageConfig{
        PathPattern: "/products/{id}.html",
        Template:    "pages/product.html",
        DataLoader: func(ctx context.Context, params map[string]string) (map[string]any, []string, error) {
            product := getProduct(params["id"])

            // Template context
            data := map[string]any{
                "product": product,
                "breadcrumb": []map[string]string{
                    {"label": "Home", "url": "/"},
                    {"label": product.Category, "url": "/categories/" + product.CategoryID + ".html"},
                    {"label": product.Name, "url": ""},
                },
            }

            // Subscription keys - page depends on these
            subscriptions := []string{
                "product:" + product.ID,
                "brand:" + product.BrandID,
                "category:" + product.CategoryID,
            }

            return data, subscriptions, nil
        },
    })

    // Start processing
    hs.Start()

    // Generate initial pages
    hs.GeneratePongoPages(context.Background(), "product-detail", []string{"1", "2", "3"})

    // When data changes, emit an event
    // All pages subscribed to "product:123" will rebuild automatically
    hs.Emit("product:123", "updated")
}
```

## Template Syntax (Django/Jinja2)

HotStatic uses [pongo2](https://github.com/flosch/pongo2) — a Django/Jinja2-compatible template engine.

### Template Inheritance

**layouts/base.html:**
```html
<!DOCTYPE html>
<html>
<head>
    <title>{% block title %}My Site{% endblock %}</title>
    {% block head %}{% endblock %}
</head>
<body>
    <header>
        <nav>
            <a href="/" {% if active_nav == 'home' %}class="active"{% endif %}>Home</a>
            <a href="/products/" {% if active_nav == 'products' %}class="active"{% endif %}>Products</a>
        </nav>
    </header>

    <main>
        {% block content %}{% endblock %}
    </main>

    <footer>
        Generated: {{ _generated_at|date:"Y-m-d H:i:s" }}
    </footer>
</body>
</html>
```

**pages/product.html:**
```html
{% extends "layouts/base.html" %}

{% block title %}{{ product.Name }} - My Store{% endblock %}

{% block content %}
    {% include "components/breadcrumb.html" with items=breadcrumb %}

    <h1>{{ product.Name }}</h1>
    <p class="brand">{{ product.BrandName }}</p>
    <p class="price">{{ product.Price|price }}</p>

    <p>{{ product.Description }}</p>

    {% if product.Features %}
    <ul class="features">
        {% for feature in product.Features %}
        <li>{{ feature }}</li>
        {% endfor %}
    </ul>
    {% endif %}

    {% if product.InStock %}
        <button class="btn-primary">Add to Cart</button>
    {% else %}
        <span class="badge badge-danger">Out of Stock</span>
    {% endif %}
{% endblock %}
```

### Include Components

**components/product-card.html:**
```html
{# Usage: {% include "components/product-card.html" with product=item %} #}

<article class="product-card">
    <a href="/products/{{ product.ID }}.html">
        <h3>{{ product.Name }}</h3>
        <span class="brand">{{ product.BrandName }}</span>
        <span class="price">{{ product.Price|price }}</span>
    </a>
</article>
```

**Usage in pages:**
```html
<div class="products-grid">
    {% for product in products %}
        {% include "components/product-card.html" with product=product %}
    {% endfor %}
</div>
```

### Built-in Filters

| Filter | Example | Output |
|--------|---------|--------|
| `price` | `{{ 99.99\|price }}` | `$99.99` |
| `truncate` | `{{ text\|truncate:100 }}` | Truncated text... |
| `pluralize` | `{{ count\|pluralize:"item,items" }}` | item/items |
| `timeago` | `{{ date\|timeago }}` | 2 hours ago |
| `date` | `{{ date\|date:"Y-m-d" }}` | 2024-01-15 |
| `default` | `{{ value\|default:"N/A" }}` | N/A if empty |
| `length` | `{{ items\|length }}` | 5 |
| `first` | `{{ items\|first }}` | First item |
| `last` | `{{ items\|last }}` | Last item |
| `join` | `{{ items\|join:", " }}` | a, b, c |
| `upper` | `{{ text\|upper }}` | TEXT |
| `lower` | `{{ text\|lower }}` | text |
| `title` | `{{ text\|title }}` | Title Text |
| `safe` | `{{ html\|safe }}` | Unescaped HTML |

### Control Structures

```html
{# If/Else #}
{% if user.is_admin %}
    <a href="/admin/">Admin Panel</a>
{% elif user.is_logged_in %}
    <a href="/profile/">Profile</a>
{% else %}
    <a href="/login/">Login</a>
{% endif %}

{# For Loop #}
{% for item in items %}
    <p>{{ forloop.Counter }}. {{ item.name }}</p>
    {% if forloop.First %}(First!){% endif %}
    {% if forloop.Last %}(Last!){% endif %}
{% empty %}
    <p>No items found.</p>
{% endfor %}

{# With (set variable) #}
{% with total=cart.items|length %}
    <p>You have {{ total }} items</p>
{% endwith %}

{# Comments #}
{# This is a comment #}
```

## Core Concepts

### Events

Events represent data changes that may trigger page rebuilds:

```go
// Simple emit
hs.Emit("product:123", "updated")

// Full event with priority
hs.EmitEvent(hotstatic.Event{
    Type:     "product",
    ID:       "123",
    Action:   "updated",
    Priority: 100, // Higher = rebuild sooner
})

// Batch emit
hs.EmitMulti([]string{"product:1", "product:2", "brand:apple"}, "updated")
```

### Pages & Subscriptions

Pages subscribe to data keys. When an event matches a subscription, the page rebuilds:

```go
hs.RegisterPongoPage("product-detail", hotstatic.PongoPageConfig{
    PathPattern: "/products/{id}.html",
    Template:    "pages/product.html",
    DataLoader: func(ctx context.Context, params map[string]string) (map[string]any, []string, error) {
        product := db.GetProduct(params["id"])

        // This page depends on:
        subscriptions := []string{
            "product:" + product.ID,          // The product itself
            "brand:" + product.BrandID,       // Its brand (name/logo changes)
            "category:" + product.CategoryID, // Its category
            "currency:usd",                   // Currency rates
        }

        return map[string]any{"product": product}, subscriptions, nil
    },
})
```

### DataLoader

The `DataLoader` function is called during page generation and rebuild:

```go
DataLoader: func(ctx context.Context, params map[string]string) (data map[string]any, subscriptions []string, err error)
```

- `params` — Extracted from `PathPattern` (e.g., `{id}` → `params["id"]`)
- `data` — Template context (available as variables in template)
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
handler := hotstatic.NewHTTPHandler(hs.HotStatic)
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
webhook := hotstatic.NewWebhook(hs.HotStatic, "your-secret-token")
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
}
```

## Custom Filters

Add your own template filters:

```go
hs.AddFilter("currency", func(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
    symbol := param.String()
    if symbol == "" {
        symbol = "$"
    }
    return pongo2.AsValue(fmt.Sprintf("%s%.2f", symbol, in.Float())), nil
})
```

Usage: `{{ price|currency:"€" }}` → `€99.99`

## Global Variables

Add variables available in all templates:

```go
hs.AddGlobal("site_name", "My Store")
hs.AddGlobal("current_year", time.Now().Year())
```

Usage: `<title>{{ site_name }}</title>`

## Project Structure

```
hotstatic/
├── go.mod
├── types.go           # Core types: Event, Page, interfaces
├── hotstatic.go       # Main API (html/template)
├── pongo.go           # Pongo2 integration (Django/Jinja2)
├── http.go            # HTTP API & webhooks
├── pkg/
│   ├── registry/      # Redis subscription management
│   ├── builder/       # Template rendering (pongo2 + html/template)
│   ├── queue/         # Priority rebuild queue
│   └── worker/        # Parallel worker pool
└── examples/
    ├── basic/         # Basic example with html/template
    └── pongo/         # Full example with Django-like templates
```

## Example Project Structure

```
my-store/
├── main.go
├── templates/
│   ├── layouts/
│   │   └── base.html
│   ├── components/
│   │   ├── header.html
│   │   ├── footer.html
│   │   ├── product-card.html
│   │   └── breadcrumb.html
│   └── pages/
│       ├── home.html
│       ├── product.html
│       └── category.html
└── dist/              # Generated static files
    ├── index.html
    ├── products/
    │   ├── 1.html
    │   └── 2.html
    └── categories/
        └── phones.html
```

## Performance Tips

1. **Use appropriate worker count** — Start with `NumCPU * 2`, adjust based on I/O
2. **Set priorities** — Critical pages (homepage, popular products) get higher priority
3. **Batch events** — Use `/api/events/batch` for bulk updates
4. **Monitor queue length** — If consistently high, add workers
5. **Use Redis cluster** — For very high throughput scenarios

## Running the Example

```bash
cd examples/pongo

# Make sure Redis is running
redis-server

# Run the example
go run main.go

# Open in browser
open http://localhost:8080
```

## License

MIT License
