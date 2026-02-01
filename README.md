# HotStatic

A static site generator framework with event-driven page rebuilds. When data changes, only affected pages are rebuilt.

## Use Cases

- Classifieds / Marketplaces
- E-commerce catalogs
- News sites
- Any content that changes rarely but needs to load fast

## Benefits

- **Fast** — Browser receives ready HTML (5-10ms instead of 100-500ms)
- **SEO** — Search engines see full content immediately
- **Cheap** — Static files can be served from CDN
- **Event-driven** — Product changed → rebuild only affected pages
- **Simple** — You control what rebuilds via event handler

## Installation

```bash
go get github.com/tabekg/hotstatic
```

## Quick Start

```go
package main

import (
    "context"
    "github.com/tabekg/hotstatic"
)

func main() {
    ctx := context.Background()

    // Initialize
    hs, _ := hotstatic.NewWithPongo(hotstatic.Config{
        TemplateDir: "./templates",
        OutputDir:   "./dist",
        Workers:     4,
        DevMode:     true,
    })

    // Define templates
    hs.DefineTemplate("product", hotstatic.TemplateDef{
        File: "pages/product.jinja2",

        Load: func(ctx context.Context, id string) (*hotstatic.PageData, error) {
            product := db.GetProduct(id)
            if product == nil {
                return nil, nil // skip
            }
            return &hotstatic.PageData{
                Path: fmt.Sprintf("/products/%s.html", id),
                Data: map[string]any{
                    "product":  product,
                    "category": db.GetCategory(product.CategoryID),
                },
            }, nil
        },

        LoadAll: func(ctx context.Context) ([]string, error) {
            return db.GetAllProductIDs(), nil
        },
    })

    hs.DefineTemplate("home", hotstatic.TemplateDef{
        File: "pages/home.jinja2",

        Load: func(ctx context.Context, id string) (*hotstatic.PageData, error) {
            return &hotstatic.PageData{
                Path: "/index.html",
                Data: map[string]any{
                    "featured": db.GetFeaturedProducts(),
                },
            }, nil
        },

        LoadAll: func(ctx context.Context) ([]string, error) {
            return []string{""}, nil // single page
        },
    })

    // Event handler — you decide what rebuilds
    hs.OnEvent(func(ctx context.Context, event hotstatic.Event) error {
        switch event.Type {
        case "product":
            switch event.Action {
            case "created":
                hs.Build("product", event.ID)
                hs.Build("home", "")
            case "updated":
                hs.Build("product", event.ID)
            case "deleted":
                hs.Delete(fmt.Sprintf("/products/%s.html", event.ID))
                hs.Build("home", "")
            }
        }
        return nil
    })

    // Build all at startup
    hs.BuildAll(ctx)

    // Start workers
    hs.Start()

    // Emit events when data changes
    hs.Emit("product", "123", "updated")
}
```

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      HotStatic                          │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  DefineTemplate("product", ...)                         │
│  DefineTemplate("category", ...)                        │
│  DefineTemplate("home", ...)                            │
│                                                         │
│  OnEvent(handler) ─────────────────────┐                │
│                                        │                │
│  BuildAll() ──► LoadAll() ──► Load() ──► Build pages    │
│                                        │                │
│  Emit(event) ──► handler ──► Build() ──► Workers ──► Build pages
│                                                         │
└─────────────────────────────────────────────────────────┘
```

## Configuration

```go
hotstatic.Config{
    // Template directory
    TemplateDir: "./templates",

    // Output directory for generated HTML
    OutputDir: "./dist",

    // Parallel workers for building (default: 4)
    Workers: 4,

    // Debounce - same page won't rebuild more than once per duration (default: 1s)
    Debounce: time.Second,

    // Development mode - enables file watching
    DevMode: true,

    // Logger (optional)
    Logger: myLogger,
}
```

## API

### DefineTemplate

Defines a template with data loading functions:

```go
hs.DefineTemplate("product", hotstatic.TemplateDef{
    // Template file (relative to TemplateDir)
    File: "pages/product.jinja2",

    // Load data for a single page
    // Returns *PageData with Path and Data, or nil to skip
    Load: func(ctx context.Context, id string) (*hotstatic.PageData, error) {
        product := db.GetProduct(id)
        if product == nil {
            return nil, nil // skip deleted/inactive
        }
        return &hotstatic.PageData{
            Path: fmt.Sprintf("/products/%s.html", id),
            Data: map[string]any{"product": product},
        }, nil
    },

    // Load all IDs for BuildAll
    LoadAll: func(ctx context.Context) ([]string, error) {
        return db.GetAllProductIDs(), nil
    },
})
```

### OnEvent

Sets the event handler. You control what pages rebuild:

```go
hs.OnEvent(func(ctx context.Context, event hotstatic.Event) error {
    // event.Type   = "product"
    // event.ID     = "123"
    // event.Action = "updated"

    switch event.Type {
    case "product":
        switch event.Action {
        case "created":
            // New product: build page, update lists
            hs.Build("product", event.ID)
            hs.Build("home", "")
            product := db.GetProduct(event.ID)
            hs.Build("category", product.CategoryID)

        case "updated":
            // Product changed: rebuild its page
            hs.Build("product", event.ID)

        case "deleted":
            // Product removed: delete page, update lists
            hs.Delete(fmt.Sprintf("/products/%s.html", event.ID))
            hs.Build("home", "")
            hs.Build("category", event.Metadata["category_id"].(string))
        }

    case "category":
        switch event.Action {
        case "updated":
            // Category name changed: rebuild category and all its products
            hs.Build("category", event.ID)
            for _, p := range db.GetProductsByCategory(event.ID) {
                hs.Build("product", p.ID)
            }
        }

    case "brand":
        switch event.Action {
        case "updated":
            // Brand changed: rebuild all its products
            for _, p := range db.GetProductsByBrand(event.ID) {
                hs.Build("product", p.ID)
            }
        }
    }

    return nil
})
```

### BuildAll

Builds all pages at startup:

```go
err := hs.BuildAll(ctx)
```

Calls `LoadAll()` for each template, then `Load()` for each ID.

### Build

Queues a page for building:

```go
hs.Build("product", "123")
```

Workers process the queue, call `Load()`, render template, write file.

### Delete

Removes a generated page by path:

```go
hs.Delete("/products/123.html")
```

### Emit

Sends an event to the event handler:

```go
// Simple
hs.Emit("product", "123", "updated")

// With metadata
hs.EmitEvent(hotstatic.Event{
    Type:     "product",
    ID:       "123",
    Action:   "deleted",
    Metadata: map[string]any{"category_id": "phones"},
})
```

### Start / Stop

Start and stop workers:

```go
hs.Start()
defer hs.Stop()
```

## HTTP API

```go
handler := hotstatic.NewHTTPHandler(hs.HotStatic)
http.Handle("/api/", handler.Router())
```

### Endpoints

| Method | URL | Description |
|--------|-----|-------------|
| POST | `/api/events` | Emit event |
| POST | `/api/build` | Build single page |
| POST | `/api/build/all` | Rebuild all pages |
| GET | `/api/stats` | Statistics |
| GET | `/api/health` | Health check |

### Examples

**Emit event:**
```bash
curl -X POST http://localhost:8080/api/events \
  -H "Content-Type: application/json" \
  -d '{"type": "product", "id": "123", "action": "updated"}'
```

**Build single page:**
```bash
curl -X POST http://localhost:8080/api/build \
  -H "Content-Type: application/json" \
  -d '{"template": "product", "id": "123"}'
```

**Statistics:**
```bash
curl http://localhost:8080/api/stats
```

## Static File Server

Serve generated files with caching:

```go
staticHandler := hotstatic.NewStaticHandlerWithCache("./dist", "404.html", []hotstatic.CacheRule{
    {Pattern: `\.[a-f0-9]{8}\.(css|js)$`, MaxAge: 31536000, Immutable: true},
    {Pattern: `\.(png|jpg|svg|webp)$`, MaxAge: 86400},
    {Pattern: `\.html$`, MaxAge: 0, MustRevalidate: true},
})
http.Handle("/", staticHandler)
```

## Templates (Pongo2 / Jinja2)

### Base Layout

**templates/layouts/base.jinja2:**
```html
<!DOCTYPE html>
<html>
<head>
    <title>{% block title %}My Site{% endblock %}</title>
</head>
<body>
    <main>{% block content %}{% endblock %}</main>
    <footer>Generated: {{ _generated_at|date:"Y-m-d H:i:s" }}</footer>
</body>
</html>
```

### Product Page

**templates/pages/product.jinja2:**
```html
{% extends "layouts/base.jinja2" %}

{% block title %}{{ product.Name }}{% endblock %}

{% block content %}
<h1>{{ product.Name }}</h1>
<p class="price">{{ product.Price|price }}</p>
<p>{{ product.Description }}</p>

{% if product.InStock %}
    <button>Buy Now</button>
{% else %}
    <span>Out of Stock</span>
{% endif %}
{% endblock %}
```

### Built-in Filters

| Filter | Example | Result |
|--------|---------|--------|
| `price` | `{{ 99.99\|price }}` | `$99.99` |
| `truncate` | `{{ text\|truncate:100 }}` | Truncated text... |
| `pluralize` | `{{ count\|pluralize:"item,items" }}` | item/items |
| `timeago` | `{{ date\|timeago }}` | 2 hours ago |

### Custom Filter

```go
hs.AddFilter("currency", func(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
    return pongo2.AsValue(fmt.Sprintf("$%.2f", in.Float())), nil
})
```

### Global Variables

```go
hs.AddGlobal("site_name", "My Store")
```

## Dev Mode

Watch templates and rebuild on changes:

```go
hs.StartDevMode(ctx, func() {
    hs.BuildAll(ctx)
})
```

## Project Structure

```
my-site/
├── main.go
├── templates/
│   ├── layouts/
│   │   └── base.jinja2
│   └── pages/
│       ├── home.jinja2
│       ├── product.jinja2
│       └── category.jinja2
└── dist/                  # generated HTML files
    ├── index.html
    ├── products/
    │   ├── 1.html
    │   └── 2.html
    └── categories/
        └── phones.html
```

## Example

See [examples/pongo](examples/pongo) for a complete example.

```bash
cd examples/pongo
go run main.go
```

## License

MIT
