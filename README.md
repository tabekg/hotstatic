# HotStatic

A static site generator framework with reactive page rebuilds. When data changes, only affected pages are rebuilt.

## Use Cases

- Classifieds / Marketplaces
- E-commerce catalogs
- News sites
- Any content that changes rarely but needs to load fast

## Benefits

- **Fast** — Browser receives ready HTML (5-10ms instead of 100-500ms)
- **SEO** — Search engines see full content immediately
- **Cheap** — Static files can be served from CDN
- **Reactive** — Product changed → only its page rebuilds

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
    ctx := context.Background()

    // Initialize
    hs, err := hotstatic.NewWithPongo(hotstatic.Config{
        Redis:       "localhost:6379",
        TemplateDir: "./templates",
        OutputDir:   "./dist",
        DevMode:     true,  // enable file watching
    })
    if err != nil {
        log.Fatal(err)
    }
    defer hs.Stop()

    // Define how to build all pages
    hs.SetBuilder(func(ctx context.Context, b *hotstatic.PageBuilder) error {
        // Static pages (no data needed)
        b.Page("pages/about.jinja2", "/about.html", nil)
        b.Page("pages/404.jinja2", "/404.html", nil)

        // Pages with data
        products := getProducts()
        for _, p := range products {
            b.Page("pages/product.jinja2", "/products/"+p.ID+".html", map[string]any{
                "product": p,
            }).DependsOn("product:"+p.ID, "brand:"+p.BrandID)
        }

        categories := getCategories()
        for _, c := range categories {
            b.Page("pages/category.jinja2", "/categories/"+c.ID+".html", map[string]any{
                "category": c,
                "products": getProductsByCategory(c.ID),
            }).DependsOn("category:"+c.ID)
        }

        return nil
    })

    // Build all pages at startup
    if err := hs.BuildAll(ctx); err != nil {
        log.Fatal(err)
    }

    // Start file watcher (in dev mode, rebuilds on template changes)
    hs.StartDevMode(ctx)

    // Start workers for event-driven rebuilds
    hs.Start()

    // When data changes — emit event with new data
    hs.EmitWithPayload("product:1", "updated", map[string]any{
        "product": updatedProduct,
    })
    // All pages subscribed to "product:1" will rebuild automatically
}
```

## Configuration

```go
hotstatic.Config{
    // Redis (required)
    Redis:         "localhost:6379",
    RedisPassword: "",                // optional
    RedisDB:       0,                 // database number
    RedisPrefix:   "hs",              // key prefix

    // Templates
    TemplateDir:   "./templates",

    // Output directory for HTML
    OutputDir:     "./dist",

    // Custom 404 page (relative to OutputDir)
    NotFoundPage:  "404.html",

    // Development mode (auto-rebuild on template changes)
    DevMode:       true,

    // Static files directory to watch (for triggering asset builds)
    StaticDir:     "./src",

    // Callback when template changes (before rebuild)
    OnTemplateChange: func(path string) {
        log.Println("Template changed:", path)
    },

    // Callback when static file changes
    OnStaticChange: func(path string) {
        log.Println("Static changed:", path)
        exec.Command("yarn", "build").Run()
    },

    // Cache rules for static files
    CacheRules: []hotstatic.CacheRule{
        {Pattern: `\.[a-f0-9]{8}\.(css|js)$`, MaxAge: 31536000, Immutable: true},
        {Pattern: `\.(png|jpg|svg|webp)$`, MaxAge: 86400},
        {Pattern: `\.html$`, MaxAge: 0, MustRevalidate: true},
    },

    // Performance
    Workers:       4,                 // parallel workers
    QueueSize:     10000,             // queue size

    // Logging
    Logger:        slog.Default(),
}
```

## API

### SetBuilder — Define How Pages Are Built

```go
hs.SetBuilder(func(ctx context.Context, b *hotstatic.PageBuilder) error {
    // Static page (no data)
    b.Page("pages/about.jinja2", "/about.html", nil)

    // Page with data
    b.Page("pages/product.jinja2", "/products/1.html", map[string]any{
        "product": product,
    })

    // Page with data and dependencies (for event-driven rebuilds)
    b.Page("pages/product.jinja2", "/products/1.html", map[string]any{
        "product": product,
    }).DependsOn("product:1", "brand:apple")

    return nil
})
```

### BuildAll — Build All Pages

```go
// Called at startup and automatically in dev mode on template changes
err := hs.BuildAll(ctx)
```

### StartDevMode — Watch Templates for Changes

```go
// Watches template directory and WatchDirs, calls BuildAll on template change
err := hs.StartDevMode(ctx)
```

### Dev Mode Callbacks

```go
hotstatic.Config{
    DevMode:   true,
    StaticDir: "./src",  // watch this directory for static file changes
    
    // Called when template changes (before rebuild)
    OnTemplateChange: func(path string) {
        log.Println("Template changed:", path)
    },
    
    // Called when static file changes
    // Use this to trigger asset builds (e.g., yarn, webpack, esbuild)
    OnStaticChange: func(path string) {
        log.Println("Static changed:", path)
        cmd := exec.Command("yarn", "build")
        cmd.Stdout = os.Stdout
        cmd.Stderr = os.Stderr
        cmd.Run()
    },
}
```

### Emit Event (Trigger Rebuild)

```go
// Product updated — send new data
hs.EmitWithPayload("product:123", "updated", map[string]any{
    "product": updatedProduct,
    "brand":   brand,
})

// All pages that depend on "product:123" will rebuild
```

### Event with Priority

```go
hs.EmitEvent(hotstatic.Event{
    Type:     "product",
    ID:       "123",
    Action:   "updated",
    Priority: 100,  // higher = more urgent
    Payload: map[string]any{
        "product": product,
    },
})
```

### List Pages

```go
pages, err := hs.ListPages(ctx)
// ["/products/1.html", "/products/2.html", "/categories/phones.html"]
```

### Delete Page

```go
err := hs.RemoveDependencies(ctx, "/products/123.html")
```

## Templates (Pongo2 / Django / Jinja2)

### Base Layout

**templates/layouts/base.html:**
```html
<!DOCTYPE html>
<html>
<head>
    <title>{% block title %}My Site{% endblock %}</title>
</head>
<body>
    <header>{% include "components/header.html" %}</header>
    
    <main>
        {% block content %}{% endblock %}
    </main>
    
    <footer>Generated: {{ _generated_at|date:"Y-m-d H:i:s" }}</footer>
</body>
</html>
```

### Product Page

**templates/pages/product.html:**
```html
{% extends "layouts/base.html" %}

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
    
    {% if product.Features %}
    <ul>
        {% for feature in product.Features %}
        <li>{{ feature }}</li>
        {% endfor %}
    </ul>
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
| `date` | `{{ date\|date:"Y-m-d" }}` | 2024-01-15 |
| `default` | `{{ value\|default:"N/A" }}` | N/A if empty |
| `length` | `{{ items\|length }}` | 5 |
| `join` | `{{ items\|join:", " }}` | a, b, c |
| `upper` | `{{ text\|upper }}` | TEXT |
| `lower` | `{{ text\|lower }}` | text |
| `safe` | `{{ html\|safe }}` | Unescaped HTML |

### Custom Filter

```go
hs.AddFilter("currency", func(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
    symbol := param.String()
    if symbol == "" {
        symbol = "$"
    }
    return pongo2.AsValue(fmt.Sprintf("%s%.2f", symbol, in.Float())), nil
})
```

Usage: `{{ price|currency:"EUR " }}` → `EUR 99.99`

### Global Variables

```go
hs.AddGlobal("site_name", "My Store")
hs.AddGlobal("current_year", time.Now().Year())
```

Usage: `<title>{{ site_name }}</title>`

## Serving Static Files

```go
mux := http.NewServeMux()

// API endpoints
handler := hotstatic.NewHTTPHandler(hs.HotStatic)
mux.Handle("/api/", handler.Router())

// Serve generated pages with custom 404
mux.Handle("/", hs.StaticHandler())

http.ListenAndServe(":8080", mux)
```

### Custom 404 Page

1. Create template `templates/pages/404.jinja2`:

```html
{% extends "layouts/base.html" %}

{% block title %}Page Not Found{% endblock %}

{% block content %}
<div class="error-page">
    <h1>404</h1>
    <p>Page not found</p>
    <a href="/">Go Home</a>
</div>
{% endblock %}
```

2. Add to your builder:

```go
hs.SetBuilder(func(ctx context.Context, b *hotstatic.PageBuilder) error {
    b.Page("pages/404.jinja2", "/404.html", nil)
    // ... other pages
    return nil
})
```

3. Set in config:

```go
hotstatic.Config{
    // ...
    NotFoundPage: "404.html",
}
```

Now any non-existent URL will show your custom 404 page with HTTP status 404.

### Cache Rules

Configure caching behavior per file type using regex patterns:

```go
hotstatic.Config{
    CacheRules: []hotstatic.CacheRule{
        {
            Pattern:   `\.[a-f0-9]{8}\.(css|js)$`,  // hashed assets
            MaxAge:    31536000,                     // 1 year
            Immutable: true,
        },
        {
            Pattern: `\.(png|jpg|jpeg|gif|svg|webp|ico)$`,  // images
            MaxAge:  86400,                                  // 1 day
        },
        {
            Pattern:        `\.html$`,  // HTML pages
            MaxAge:         0,          // no-cache
            MustRevalidate: true,
        },
    },
}
```

**CacheRule fields:**

| Field | Description |
|-------|-------------|
| `Pattern` | Regex to match URL path |
| `MaxAge` | Cache duration in seconds (0 = no-cache) |
| `Immutable` | Add `immutable` directive |
| `MustRevalidate` | Add `must-revalidate` directive |
| `Private` | Use `private` instead of `public` |

**Result headers:**
```
style.a1b2c3d4.css → Cache-Control: public, max-age=31536000, immutable
logo.png           → Cache-Control: public, max-age=86400
index.html         → Cache-Control: public, no-cache, must-revalidate
```

Rules are checked in order — first match wins. ETag is generated automatically for all files using xxHash.

## HTTP API

```go
handler := hotstatic.NewHTTPHandler(hs.HotStatic)
http.ListenAndServe(":8080", handler.Router())
```

### Endpoints

| Method | URL | Description |
|--------|-----|-------------|
| POST | `/api/events` | Emit event |
| POST | `/api/events/batch` | Emit multiple events |
| POST | `/api/build` | Rebuild a page |
| GET | `/api/stats` | Statistics |
| GET | `/api/pages` | List pages |
| GET | `/api/pages/{path}` | Page info |
| DELETE | `/api/pages/{path}` | Delete page |
| GET | `/api/health` | Health check |

### Examples

**Emit event:**
```bash
curl -X POST http://localhost:8080/api/events \
  -H "Content-Type: application/json" \
  -d '{
    "type": "product",
    "id": "123",
    "action": "updated",
    "payload": {
      "product": {"id": "123", "name": "iPhone", "price": 999}
    }
  }'
```

**Rebuild page:**
```bash
curl -X POST http://localhost:8080/api/build \
  -H "Content-Type: application/json" \
  -d '{
    "path": "/products/123.html",
    "payload": {
      "product": {"id": "123", "name": "iPhone", "price": 999}
    }
  }'
```

**Statistics:**
```bash
curl http://localhost:8080/api/stats
```

```json
{
  "pages_total": 15420,
  "pages_built": 342,
  "pages_failed": 2,
  "events_processed": 1893,
  "queue_length": 12,
  "workers_active": 4,
  "uptime": "2h15m30s"
}
```

## Webhook

```go
webhook := hotstatic.NewWebhook(hs.HotStatic, "secret-token")
http.Handle("/webhook", webhook.Handler())
```

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Authorization: Bearer secret-token" \
  -H "Content-Type: application/json" \
  -d '{"type": "product", "id": "123", "action": "updated"}'
```

## Client-side Router (SPA-like Navigation)

HotStatic includes a JavaScript router for smooth transitions between static pages.

### Setup

```html
<script>
window.HotStaticConfig = {
    contentSelector: 'main',
    progressBar: {
        enabled: true,
        color: '#3b82f6',
        height: '3px',
        position: 'top',
    },
    prefetch: {
        enabled: true,
        on: 'hover',  // 'hover' | 'visible' | 'both'
        delay: 100,
    },
    cache: {
        enabled: true,
        maxPages: 20,
        ttl: 300,  // seconds
    },
    navigation: {
        transition: 'fade',  // 'fade' | 'slide' | 'none'
        duration: 150,
    },
};
</script>
<script src="/static/js/router.js"></script>
```

### Events

```js
// Before navigation (cancelable)
document.addEventListener('hs:beforeNavigate', (e) => {
    console.log('Leaving:', e.detail.from);
    console.log('Going to:', e.detail.to);
    // e.preventDefault(); // cancel navigation
});

// After navigation
document.addEventListener('hs:afterNavigate', (e) => {
    console.log('Navigated to:', e.detail.to);
    window.scrollTo(0, 0); // scroll to top if needed
});

// Page prefetched
document.addEventListener('hs:prefetch', (e) => {
    console.log('Prefetched:', e.detail.url);
});
```

### JavaScript API

```js
HotStatic.navigate('/products/1.html');  // navigate
HotStatic.prefetch('/about.html');        // prefetch
HotStatic.clearCache();                   // clear cache
HotStatic.getCachedUrls();                // get cached URLs
HotStatic.getConfig();                    // get current config
```

### Ignore Link

```html
<a href="/external" data-hs-ignore>Normal navigation</a>
```

## Project Structure

```
my-site/
├── main.go
├── templates/
│   ├── layouts/
│   │   └── base.html
│   ├── components/
│   │   ├── header.html
│   │   ├── product-card.html
│   │   └── breadcrumb.html
│   └── pages/
│       ├── home.html
│       ├── product.html
│       └── category.html
├── static/
│   ├── js/
│   │   └── router.js
│   └── css/
│       └── style.css
└── dist/                  # generated HTML files
    ├── index.html
    ├── products/
    │   ├── 1.html
    │   └── 2.html
    └── categories/
        └── phones.html
```

## Example: Classifieds Site

```go
// Define builder
hs.SetBuilder(func(ctx context.Context, b *hotstatic.PageBuilder) error {
    // Static pages
    b.Page("pages/404.jinja2", "/404.html", nil)
    b.Page("pages/about.jinja2", "/about.html", nil)

    // All ads
    ads := getAds()
    for _, ad := range ads {
        b.Page("pages/ad.jinja2", "/ads/"+ad.ID+".html", map[string]any{
            "ad":       ad,
            "seller":   getSeller(ad.SellerID),
            "category": getCategory(ad.CategoryID),
        }).DependsOn("ad:"+ad.ID, "seller:"+ad.SellerID)
    }

    // Categories
    categories := getCategories()
    for _, cat := range categories {
        b.Page("pages/category.jinja2", "/categories/"+cat.ID+".html", map[string]any{
            "category": cat,
            "ads":      getAdsByCategory(cat.ID),
        }).DependsOn("category:"+cat.ID)
    }

    return nil
})

// Build at startup
hs.BuildAll(ctx)

// Start dev mode (watch for template changes)
hs.StartDevMode(ctx)

// Start workers
hs.Start()

// When ad is updated
func onAdUpdated(ad Ad) {
    hs.EmitWithPayload("ad:"+ad.ID, "updated", map[string]any{
        "ad":       ad,
        "seller":   getSeller(ad.SellerID),
        "category": getCategory(ad.CategoryID),
    })
}

// When seller changes name → all their ads rebuild
func onSellerUpdated(seller Seller) {
    ads := getAdsBySeller(seller.ID)
    for _, ad := range ads {
        hs.EmitWithPayload("ad:"+ad.ID, "updated", map[string]any{
            "ad":       ad,
            "seller":   seller,
            "category": getCategory(ad.CategoryID),
        })
    }
}
```

## Running the Example

```bash
cd examples/pongo

# Start Redis
redis-server

# Run example
go run main.go

# Open in browser
open http://localhost:8080
```

## License

MIT
