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
    // Initialize
    hs, err := hotstatic.NewWithPongo(hotstatic.Config{
        Redis:       "localhost:6379",
        TemplateDir: "./templates",
        OutputDir:   "./dist",
        Workers:     4,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer hs.Stop()

    // Start workers
    hs.Start()

    ctx := context.Background()

    // Generate a page with data
    data := map[string]any{
        "product": Product{ID: "1", Name: "iPhone 15", Price: 999},
        "brand":   "Apple",
    }
    
    subscriptions := []string{
        "product:1",    // page depends on this product
        "brand:apple",  // and this brand
    }

    err = hs.GeneratePongoPage(ctx, hotstatic.Page{
        Path:          "/products/1.html",
        Template:      "pages/product.html",
        Subscriptions: subscriptions,
        Params:        map[string]string{"id": "1"},
    }, data)

    // When data changes — emit event with new data
    hs.EmitWithPayload("product:1", "updated", map[string]any{
        "product": Product{ID: "1", Name: "iPhone 15 Pro", Price: 1099},
        "brand":   "Apple",
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

    // Performance
    Workers:       4,                 // parallel workers
    QueueSize:     10000,             // queue size

    // Logging
    Logger:        slog.Default(),
}
```

## API

### Generate a Page

```go
// Data for template
data := map[string]any{
    "product": product,
    "brand":   brand,
}

// Subscriptions (page rebuilds when these keys change)
subscriptions := []string{"product:123", "brand:apple"}

// Generate
err := hs.GeneratePongoPage(ctx, hotstatic.Page{
    Path:          "/products/123.html",
    Template:      "pages/product.html",
    Subscriptions: subscriptions,
    Params:        map[string]string{"id": "123"},
}, data)
```

### Emit Event (Trigger Rebuild)

```go
// Product updated — send new data
hs.EmitWithPayload("product:123", "updated", map[string]any{
    "product": updatedProduct,
    "brand":   brand,
})

// All pages subscribed to "product:123" will rebuild
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
err := hs.Unsubscribe(ctx, "/products/123.html")
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
// Generate ad page
func generateAdPage(ctx context.Context, ad Ad) error {
    data := map[string]any{
        "ad":       ad,
        "seller":   getSeller(ad.SellerID),
        "category": getCategory(ad.CategoryID),
    }
    
    subscriptions := []string{
        "ad:" + ad.ID,
        "seller:" + ad.SellerID,
        "category:" + ad.CategoryID,
    }
    
    return hs.GeneratePongoPage(ctx, hotstatic.Page{
        Path:          "/ads/" + ad.ID + ".html",
        Template:      "pages/ad.html",
        Subscriptions: subscriptions,
        Params:        map[string]string{"id": ad.ID},
    }, data)
}

// Ad updated
func onAdUpdated(ad Ad) {
    data := map[string]any{
        "ad":       ad,
        "seller":   getSeller(ad.SellerID),
        "category": getCategory(ad.CategoryID),
    }
    hs.EmitWithPayload("ad:"+ad.ID, "updated", data)
}

// Seller changed name → all their ads rebuild
func onSellerUpdated(seller Seller) {
    ads := getAdsBySeller(seller.ID)
    for _, ad := range ads {
        data := map[string]any{
            "ad":       ad,
            "seller":   seller,
            "category": getCategory(ad.CategoryID),
        }
        hs.EmitWithPayload("ad:"+ad.ID, "updated", data)
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
