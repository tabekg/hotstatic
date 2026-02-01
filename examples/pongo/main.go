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

// ========== DATA MODELS ==========

type Product struct {
	ID           string
	Name         string
	Price        float64
	Description  string
	CategoryID   string
	CategoryName string
	BrandID      string
	BrandName    string
	Features     []string
	InStock      bool
	IsFeatured   bool
}

type Category struct {
	ID   string
	Name string
}

type Brand struct {
	ID   string
	Name string
}

// ========== MOCK DATABASE ==========

var products = map[string]*Product{
	"1": {
		ID:           "1",
		Name:         "iPhone 15 Pro",
		Price:        999.00,
		Description:  "The most powerful iPhone ever.",
		CategoryID:   "phones",
		CategoryName: "Smartphones",
		BrandID:      "apple",
		BrandName:    "Apple",
		Features:     []string{"A17 Pro chip", "Titanium design", "48MP camera"},
		InStock:      true,
		IsFeatured:   true,
	},
	"2": {
		ID:           "2",
		Name:         "Galaxy S24 Ultra",
		Price:        1199.00,
		Description:  "The ultimate Galaxy experience.",
		CategoryID:   "phones",
		CategoryName: "Smartphones",
		BrandID:      "samsung",
		BrandName:    "Samsung",
		Features:     []string{"Snapdragon 8 Gen 3", "S Pen", "200MP camera"},
		InStock:      true,
		IsFeatured:   true,
	},
	"3": {
		ID:           "3",
		Name:         "MacBook Pro 16\"",
		Price:        2499.00,
		Description:  "Supercharged by M3 Pro chip.",
		CategoryID:   "laptops",
		CategoryName: "Laptops",
		BrandID:      "apple",
		BrandName:    "Apple",
		Features:     []string{"M3 Pro chip", "22hr battery", "Liquid Retina XDR"},
		InStock:      true,
		IsFeatured:   true,
	},
	"4": {
		ID:           "4",
		Name:         "ThinkPad X1 Carbon",
		Price:        1849.00,
		Description:  "Ultralight business laptop.",
		CategoryID:   "laptops",
		CategoryName: "Laptops",
		BrandID:      "lenovo",
		BrandName:    "Lenovo",
		Features:     []string{"Intel Core Ultra", "Carbon fiber", "2.48 lbs"},
		InStock:      false,
		IsFeatured:   false,
	},
}

var categories = map[string]*Category{
	"phones":  {ID: "phones", Name: "Smartphones"},
	"laptops": {ID: "laptops", Name: "Laptops"},
}

var brands = map[string]*Brand{
	"apple":   {ID: "apple", Name: "Apple"},
	"samsung": {ID: "samsung", Name: "Samsung"},
	"lenovo":  {ID: "lenovo", Name: "Lenovo"},
}

// ========== DATA LOADER FUNCTIONS ==========

func getProduct(id string) *Product {
	return products[id]
}

func getAllProductIDs() []string {
	ids := make([]string, 0, len(products))
	for id := range products {
		ids = append(ids, id)
	}
	return ids
}

func getProductsByCategory(categoryID string) []*Product {
	var result []*Product
	for _, p := range products {
		if p.CategoryID == categoryID {
			result = append(result, p)
		}
	}
	return result
}

func getProductsByBrand(brandID string) []*Product {
	var result []*Product
	for _, p := range products {
		if p.BrandID == brandID {
			result = append(result, p)
		}
	}
	return result
}

func getFeaturedProducts() []*Product {
	var result []*Product
	for _, p := range products {
		if p.IsFeatured && p.InStock {
			result = append(result, p)
		}
	}
	return result
}

func getCategory(id string) *Category {
	return categories[id]
}

func getAllCategoryIDs() []string {
	ids := make([]string, 0, len(categories))
	for id := range categories {
		ids = append(ids, id)
	}
	return ids
}

func getAllCategories() []*Category {
	result := make([]*Category, 0, len(categories))
	for _, c := range categories {
		result = append(result, c)
	}
	return result
}

func getBrand(id string) *Brand {
	return brands[id]
}

// ========== MAIN ==========

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	devMode := os.Getenv("MODE") == "dev"

	// Initialize HotStatic with pongo2
	hs, err := hotstatic.NewWithPongo(hotstatic.Config{
		TemplateDir: "./templates",
		OutputDir:   "./dist",
		Workers:     4,
		Debounce:    time.Second,
		DevMode:     devMode,
		Logger:      &slogLogger{logger},
	})
	if err != nil {
		log.Fatal(err)
	}

	// ========== DEFINE TEMPLATES ==========

	// Product pages
	hs.DefineTemplate("product", hotstatic.TemplateDef{
		File:   "pages/product.jinja2",
		Output: "/products/{id}.html",

		Load: func(ctx context.Context, id string) (map[string]any, error) {
			product := getProduct(id)
			if product == nil || !product.InStock {
				return nil, nil // skip inactive products
			}

			return map[string]any{
				"product":  product,
				"category": getCategory(product.CategoryID),
				"brand":    getBrand(product.BrandID),
				"breadcrumb": []map[string]string{
					{"label": "Home", "url": "/"},
					{"label": product.CategoryName, "url": "/categories/" + product.CategoryID + ".html"},
					{"label": product.Name, "url": ""},
				},
			}, nil
		},

		LoadAll: func(ctx context.Context) ([]string, error) {
			return getAllProductIDs(), nil
		},
	})

	// Category pages
	hs.DefineTemplate("category", hotstatic.TemplateDef{
		File:   "pages/category.jinja2",
		Output: "/categories/{id}.html",

		Load: func(ctx context.Context, id string) (map[string]any, error) {
			category := getCategory(id)
			if category == nil {
				return nil, nil
			}

			return map[string]any{
				"category": category,
				"products": getProductsByCategory(id),
				"breadcrumb": []map[string]string{
					{"label": "Home", "url": "/"},
					{"label": category.Name, "url": ""},
				},
			}, nil
		},

		LoadAll: func(ctx context.Context) ([]string, error) {
			return getAllCategoryIDs(), nil
		},
	})

	// Home page
	hs.DefineTemplate("home", hotstatic.TemplateDef{
		File:   "pages/home.jinja2",
		Output: "/index.html",

		Load: func(ctx context.Context, id string) (map[string]any, error) {
			return map[string]any{
				"featured":   getFeaturedProducts(),
				"categories": getAllCategories(),
			}, nil
		},

		LoadAll: func(ctx context.Context) ([]string, error) {
			return []string{""}, nil // single home page, empty id
		},
	})

	// 404 page
	hs.DefineTemplate("404", hotstatic.TemplateDef{
		File:   "pages/404.jinja2",
		Output: "/404.html",

		Load: func(ctx context.Context, id string) (map[string]any, error) {
			return map[string]any{}, nil
		},

		LoadAll: func(ctx context.Context) ([]string, error) {
			return []string{""}, nil
		},
	})

	// ========== EVENT HANDLER ==========

	hs.OnEvent(func(ctx context.Context, event hotstatic.Event) error {
		switch event.Type {
		case "product":
			switch event.Action {
			case "created":
				// New product: build product page, update home and category
				hs.Build("product", event.ID)
				hs.Build("home", "")
				if product := getProduct(event.ID); product != nil {
					hs.Build("category", product.CategoryID)
				}

			case "updated":
				// Product updated: rebuild product page
				hs.Build("product", event.ID)
				// Optionally update category/home if price or featured status changed
				if product := getProduct(event.ID); product != nil {
					hs.Build("category", product.CategoryID)
					if product.IsFeatured {
						hs.Build("home", "")
					}
				}

			case "deleted":
				// Product deleted: delete page, update home and category
				categoryID := ""
				if catID, ok := event.Metadata["category_id"].(string); ok {
					categoryID = catID
				}
				hs.Delete("product", event.ID)
				hs.Build("home", "")
				if categoryID != "" {
					hs.Build("category", categoryID)
				}
			}

		case "category":
			switch event.Action {
			case "updated":
				// Category name changed: rebuild category page and all its products
				hs.Build("category", event.ID)
				for _, product := range getProductsByCategory(event.ID) {
					hs.Build("product", product.ID)
				}
				hs.Build("home", "")
			}

		case "brand":
			switch event.Action {
			case "updated":
				// Brand name changed: rebuild all products of this brand
				for _, product := range getProductsByBrand(event.ID) {
					hs.Build("product", product.ID)
				}
			}
		}

		return nil
	})

	// ========== BUILD ALL AT STARTUP ==========

	fmt.Println("Building all pages...")
	if err := hs.BuildAll(ctx); err != nil {
		log.Fatal(err)
	}

	// ========== START DEV MODE ==========

	if devMode {
		fmt.Println("Dev mode: watching for template changes...")
		hs.StartDevMode(ctx, func() {
			fmt.Println("Templates changed, rebuilding...")
			hs.BuildAll(ctx)
		})
	}

	// ========== START WORKERS ==========

	hs.Start()

	fmt.Println("\nStats:", hs.Stats())

	// ========== HTTP SERVER ==========

	mux := http.NewServeMux()

	// API endpoints
	handler := hotstatic.NewHTTPHandler(hs.HotStatic)
	mux.Handle("/api/", handler.Router())

	// Serve static files with caching
	staticHandler := hotstatic.NewStaticHandlerWithCache("./dist", "404.html", []hotstatic.CacheRule{
		{Pattern: `\.[a-f0-9]{8}\.(css|js)$`, MaxAge: 31536000, Immutable: true},
		{Pattern: `\.(png|jpg|svg|webp|ico)$`, MaxAge: 86400},
		{Pattern: `\.html$`, MaxAge: 0, MustRevalidate: true},
	})
	mux.Handle("/", staticHandler)

	fmt.Println("\nServer running on http://localhost:8080")
	fmt.Println("\nPages:")
	fmt.Println("  http://localhost:8080/")
	fmt.Println("  http://localhost:8080/products/1.html")
	fmt.Println("  http://localhost:8080/categories/phones.html")
	fmt.Println("\nAPI:")
	fmt.Println("  POST /api/events - emit event")
	fmt.Println("  POST /api/build  - build single page")
	fmt.Println("  GET  /api/stats  - statistics")
	fmt.Println("\nExample events:")
	fmt.Println("  curl -X POST http://localhost:8080/api/events \\")
	fmt.Println("       -H 'Content-Type: application/json' \\")
	fmt.Println("       -d '{\"type\":\"product\",\"id\":\"1\",\"action\":\"updated\"}'")

	// Graceful shutdown
	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-quit
	fmt.Println("\nShutting down...")

	hs.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	fmt.Println("Shutdown complete")
}

// slogLogger adapts slog.Logger to hotstatic.Logger interface
type slogLogger struct {
	*slog.Logger
}

func (l *slogLogger) Debug(msg string, args ...any) { l.Logger.Debug(msg, args...) }
func (l *slogLogger) Info(msg string, args ...any)  { l.Logger.Info(msg, args...) }
func (l *slogLogger) Warn(msg string, args ...any)  { l.Logger.Warn(msg, args...) }
func (l *slogLogger) Error(msg string, args ...any) { l.Logger.Error(msg, args...) }
