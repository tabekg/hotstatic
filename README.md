# HotStatic

A simple static site generator for Go. Define templates, load data, build HTML.

## Installation

```bash
go get github.com/tabekg/hotstatic
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "github.com/tabekg/hotstatic"
)

func main() {
    ctx := context.Background()

    // Initialize with pongo2 templates
    hs, _ := hotstatic.NewWithPongo(hotstatic.Config{
        TemplateDir: "./templates",
        OutputDir:   "./dist",
    })

    // Define a template
    hs.DefineTemplate("product", hotstatic.TemplateDef{
        File: "pages/product.jinja2",

        Load: func(ctx context.Context, id string) (*hotstatic.PageData, error) {
            product := db.GetProduct(id)
            if product == nil {
                return nil, nil // skip
            }
            return &hotstatic.PageData{
                Path: fmt.Sprintf("/products/%s.html", id),
                Data: map[string]any{"product": product},
            }, nil
        },

        LoadAll: func(ctx context.Context) ([]string, error) {
            return db.GetAllProductIDs(), nil
        },
    })

    // Build all pages
    hs.BuildAll(ctx)

    // Or build a single page
    hs.Build(ctx, "product", "123")

    // Delete a page
    hs.Delete("/products/123.html")
}
```

## API

### NewWithPongo

Creates HotStatic with pongo2/jinja2 template support:

```go
hs, err := hotstatic.NewWithPongo(hotstatic.Config{
    TemplateDir: "./templates",
    OutputDir:   "./dist",
    Workers:     4,              // parallel workers (default: 4)
    Debounce:    time.Second,    // debounce duration (default: 1s)
    Minify:      true,           // minify HTML/CSS/JS (default: false)
    Logger:      myLogger,       // optional
})
```

### Minification

When `Minify: true` is set, output files are automatically minified based on file extension:

| Extension | Content Type |
|-----------|--------------|
| `.html`, `.htm` | HTML |
| `.css` | CSS |
| `.js` | JavaScript |
| `.json` | JSON |
| `.svg` | SVG |
| `.xml` | XML |

Uses [tdewolff/minify](https://github.com/tdewolff/minify) for minification.

### DefineTemplate

Defines a template with data loading:

```go
hs.DefineTemplate("product", hotstatic.TemplateDef{
    // Template file (relative to TemplateDir)
    File: "pages/product.jinja2",

    // Load data for a single page
    // Return nil to skip (deleted/inactive entity)
    Load: func(ctx context.Context, id string) (*hotstatic.PageData, error) {
        product := db.GetProduct(id)
        if product == nil {
            return nil, nil
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

### Build vs Queue

Two ways to build pages:

| | Build | Queue |
|---|-------|-------|
| **Type** | Sync (blocks) | Async (returns immediately) |
| **Debounce** | No | Yes |
| **Error handling** | Returns error | Logs error |
| **Use case** | Single page, need result | Mass updates, webhooks |

**Build** - synchronous, waits for completion:

```go
err := hs.Build(ctx, "product", "123") // blocks until done
if err != nil {
    // handle error
}
```

**Queue** - asynchronous, adds to queue with debounce:

```go
hs.Start() // start workers first
defer hs.Stop()

hs.Queue("product", "123") // returns immediately
hs.Queue("product", "456") // workers process in parallel
hs.Queue("product", "123") // debounced - skipped if queued recently
```

### BuildAll

Build all pages for all templates:

```go
err := hs.BuildAll(ctx)
```

### Delete

Delete a generated page:

```go
err := hs.Delete("/products/123.html")
```

### AddGlobal

Add global variables available in all templates:

```go
hs.AddGlobal("site", map[string]any{
    "name": "My Store",
    "url":  "https://mystore.com",
})
```

In template:
```html
<title>{{ site.name }}</title>
```

### AddFilter

Add custom template filter:

```go
hs.AddFilter("currency", func(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
    return pongo2.AsValue(fmt.Sprintf("$%.2f", in.Float())), nil
})
```

## Built-in Filters

| Filter | Example | Result |
|--------|---------|--------|
| `price` | `{{ 99.99\|price }}` | `$99.99` |
| `truncate` | `{{ text\|truncate:100 }}` | Truncated... |
| `pluralize` | `{{ count\|pluralize:"item,items" }}` | item/items |
| `timeago` | `{{ date\|timeago }}` | 2 hours ago |

## Built-in Variables

Available in all templates:

| Variable | Description |
|----------|-------------|
| `_generated_at` | Build timestamp |
| `_template` | Template file path |
| `_output` | Output file path |

## Templates (Pongo2/Jinja2)

**templates/layouts/base.jinja2:**
```html
<!DOCTYPE html>
<html>
<head>
    <title>{% block title %}{{ site.name }}{% endblock %}</title>
</head>
<body>
    {% block content %}{% endblock %}
</body>
</html>
```

**templates/pages/product.jinja2:**
```html
{% extends "layouts/base.jinja2" %}

{% block title %}{{ product.Name }} - {{ site.name }}{% endblock %}

{% block content %}
<h1>{{ product.Name }}</h1>
<p class="price">{{ product.Price|price }}</p>
<p>{{ product.Description }}</p>
{% endblock %}
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
└── dist/                  # generated HTML
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
