package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tabekg/hotstatic"
)

// Product data
type Product struct {
	ID           string
	Name         string
	Price        float64
	Description  string
	BrandID      string
	BrandName    string
	CategoryID   string
	CategoryName string
	Features     []string
	InStock      bool
}

// Category data
type Category struct {
	ID    string
	Name  string
	Count int
}

// Mock database
var products = map[string]Product{
	"1": {
		ID:           "1",
		Name:         "iPhone 15 Pro",
		Price:        999.00,
		Description:  "The most powerful iPhone ever. Featuring the A17 Pro chip, titanium design, and an advanced camera system that transforms mobile photography.",
		BrandID:      "apple",
		BrandName:    "Apple",
		CategoryID:   "phones",
		CategoryName: "Smartphones",
		Features:     []string{"A17 Pro chip", "Titanium design", "48MP camera system", "USB-C", "Action button"},
		InStock:      true,
	},
	"2": {
		ID:           "2",
		Name:         "Galaxy S24 Ultra",
		Price:        1199.00,
		Description:  "The ultimate Galaxy experience with built-in S Pen, AI-powered features, and a stunning 200MP camera.",
		BrandID:      "samsung",
		BrandName:    "Samsung",
		CategoryID:   "phones",
		CategoryName: "Smartphones",
		Features:     []string{"Snapdragon 8 Gen 3", "S Pen included", "200MP camera", "Titanium frame", "Galaxy AI"},
		InStock:      true,
	},
	"3": {
		ID:           "3",
		Name:         "MacBook Pro 16\"",
		Price:        2499.00,
		Description:  "Supercharged by M3 Pro or M3 Max chip. The most advanced Mac laptops for demanding workflows.",
		BrandID:      "apple",
		BrandName:    "Apple",
		CategoryID:   "laptops",
		CategoryName: "Laptops",
		Features:     []string{"M3 Pro/Max chip", "Up to 22hr battery", "Liquid Retina XDR", "MagSafe 3", "HDMI & SD card"},
		InStock:      true,
	},
	"4": {
		ID:           "4",
		Name:         "ThinkPad X1 Carbon",
		Price:        1849.00,
		Description:  "Ultralight business laptop with legendary ThinkPad reliability, security features, and all-day battery life.",
		BrandID:      "lenovo",
		BrandName:    "Lenovo",
		CategoryID:   "laptops",
		CategoryName: "Laptops",
		Features:     []string{"Intel Core Ultra", "Carbon fiber chassis", "ThinkShield security", "2.48 lbs", "Rapid Charge"},
		InStock:      false,
	},
	"5": {
		ID:           "5",
		Name:         "Pixel 8 Pro",
		Price:        999.00,
		Description:  "Google's most advanced phone yet with the best of Google AI, an incredible camera, and seven years of updates.",
		BrandID:      "google",
		BrandName:    "Google",
		CategoryID:   "phones",
		CategoryName: "Smartphones",
		Features:     []string{"Tensor G3", "Pro cameras", "7 years updates", "Temperature sensor", "Magic Eraser"},
		InStock:      true,
	},
}

var categories = map[string]Category{
	"phones":  {ID: "phones", Name: "Smartphones", Count: 0},
	"laptops": {ID: "laptops", Name: "Laptops", Count: 0},
}

func init() {
	// Count products per category
	for _, p := range products {
		if cat, ok := categories[p.CategoryID]; ok {
			cat.Count++
			categories[p.CategoryID] = cat
		}
	}
}

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Check dev mode
	devMode := os.Getenv("MODE") == "dev" || os.Getenv("MODE") == "development"

	// Initialize HotStatic with pongo2 (Django-like templates)
	hs, err := hotstatic.NewWithPongo(hotstatic.Config{
		Redis:        "localhost:6379",
		TemplateDir:  "./templates",
		OutputDir:    "./dist",
		NotFoundPage: "404.html",
		DevMode:      devMode,
		CacheRules: []hotstatic.CacheRule{
			{Pattern: `\.[a-f0-9]{8}\.(css|js)$`, MaxAge: 31536000, Immutable: true},
			{Pattern: `\.(png|jpg|svg|webp|ico)$`, MaxAge: 86400},
			{Pattern: `\.html$`, MaxAge: 0, MustRevalidate: true},
		},
		Workers: 4,
		Logger:  logger,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer hs.Stop()

	// Define how to build all pages
	hs.SetBuilder(func(ctx context.Context, b *hotstatic.PageBuilder) error {
		// Static pages (no data needed)
		b.Page("pages/404.jinja2", "/404.html", nil)

		// Home page - depends on featured products
		featuredProducts := getFeaturedProducts()
		homeDeps := make([]string, len(featuredProducts))
		for i, p := range featuredProducts {
			homeDeps[i] = "product:" + p.ID
		}
		b.Page("pages/home.jinja2", "/index.html", map[string]any{
			"featured_products": featuredProducts,
			"categories":        getCategories(),
			"active_nav":        "home",
		}).DependsOn(homeDeps...)

		// Product pages
		for id, product := range products {
			b.Page("pages/product.jinja2", "/products/"+id+".html", map[string]any{
				"product":    product,
				"active_nav": product.CategoryID,
				"breadcrumb": []map[string]string{
					{"label": "Home", "url": "/"},
					{"label": product.CategoryName, "url": "/categories/" + product.CategoryID + ".html"},
					{"label": product.Name, "url": ""},
				},
			}).DependsOn("product:"+product.ID, "brand:"+product.BrandID)
		}

		// Category pages
		for id, category := range categories {
			categoryProducts := getProductsByCategory(id)
			b.Page("pages/category.jinja2", "/categories/"+id+".html", map[string]any{
				"category":   category,
				"products":   categoryProducts,
				"active_nav": id,
				"breadcrumb": []map[string]string{
					{"label": "Home", "url": "/"},
					{"label": category.Name, "url": ""},
				},
			}).DependsOn("category:" + id)
		}

		return nil
	})

	// Set up resolvers for event-driven rebuilds
	// When an event comes in, the resolver fetches fresh data and returns PageData
	hs.SetResolver("product", func(ctx context.Context, event hotstatic.Event) (*hotstatic.PageData, error) {
		product, ok := products[event.ID]
		if !ok {
			// Product deleted or not found - return nil to skip
			return nil, nil
		}

		return &hotstatic.PageData{
			Template: "pages/product.jinja2",
			Output:   "/products/" + event.ID + ".html",
			Data: map[string]any{
				"product":    product,
				"active_nav": product.CategoryID,
				"breadcrumb": []map[string]string{
					{"label": "Home", "url": "/"},
					{"label": product.CategoryName, "url": "/categories/" + product.CategoryID + ".html"},
					{"label": product.Name, "url": ""},
				},
			},
			Dependencies: []string{
				"product:" + product.ID,
				"brand:" + product.BrandID,
			},
		}, nil
	})

	hs.SetResolver("category", func(ctx context.Context, event hotstatic.Event) (*hotstatic.PageData, error) {
		category, ok := categories[event.ID]
		if !ok {
			return nil, nil
		}

		categoryProducts := getProductsByCategory(event.ID)

		return &hotstatic.PageData{
			Template: "pages/category.jinja2",
			Output:   "/categories/" + event.ID + ".html",
			Data: map[string]any{
				"category":   category,
				"products":   categoryProducts,
				"active_nav": event.ID,
				"breadcrumb": []map[string]string{
					{"label": "Home", "url": "/"},
					{"label": category.Name, "url": ""},
				},
			},
			Dependencies: []string{"category:" + event.ID},
		}, nil
	})

	// Build all pages at startup
	fmt.Println("Building pages...")
	if err := hs.BuildAll(ctx); err != nil {
		log.Fatal(err)
	}

	// Start dev mode (watch for template changes)
	if devMode {
		fmt.Println("Dev mode enabled - watching for template changes")
		if err := hs.StartDevMode(ctx); err != nil {
			log.Fatal(err)
		}
	}

	// Start workers for event-driven rebuilds
	hs.Start()

	fmt.Println("\nStats:", hs.Stats())

	// HTTP server
	mux := http.NewServeMux()

	// API endpoints
	handler := hotstatic.NewHTTPHandler(hs.HotStatic)
	mux.Handle("/api/", handler.Router())

	// Serve static assets
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	// Serve generated pages with custom 404
	mux.Handle("/", hs.StaticHandler())

	fmt.Println("\nServer running on http://localhost:8080")
	fmt.Println("\nPages:")
	fmt.Println("  http://localhost:8080/")
	fmt.Println("  http://localhost:8080/products/1.html")
	fmt.Println("  http://localhost:8080/categories/phones.html")
	fmt.Println("\nAPI:")
	fmt.Println("  POST /api/events - emit event")
	fmt.Println("  GET  /api/stats  - statistics")
	fmt.Println("\nTry updating a product:")
	fmt.Println("  curl -X POST http://localhost:8080/api/events \\")
	fmt.Println("       -H 'Content-Type: application/json' \\")
	fmt.Println("       -d '{\"type\":\"product\",\"id\":\"1\",\"action\":\"updated\"}'")

	if devMode {
		fmt.Println("\nDev mode: Edit templates and see changes automatically!")
	}

	// Graceful shutdown
	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Demo: emit event after 5 seconds
	// With resolvers, we just emit the event - no payload needed!
	// The resolver will fetch fresh data automatically
	go func() {
		time.Sleep(5 * time.Second)
		fmt.Println("\nAuto-emitting product update event...")
		hs.Emit("product:1", "updated")
	}()

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-quit
	fmt.Println("\nShutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	fmt.Println("Shutdown complete")
}

// Helper functions

func getFeaturedProducts() []Product {
	var featured []Product
	for _, p := range products {
		if p.InStock {
			featured = append(featured, p)
			if len(featured) >= 4 {
				break
			}
		}
	}
	return featured
}

func getCategories() []Category {
	var cats []Category
	for _, cat := range categories {
		cats = append(cats, cat)
	}
	return cats
}

func getProductsByCategory(categoryID string) []Product {
	var result []Product
	for _, p := range products {
		if p.CategoryID == categoryID {
			result = append(result, p)
		}
	}
	return result
}
