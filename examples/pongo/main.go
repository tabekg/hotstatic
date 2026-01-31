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
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Initialize HotStatic with pongo2 (Django-like templates)
	hs, err := hotstatic.NewWithPongo(hotstatic.Config{
		Redis:       "localhost:6379",
		TemplateDir: "./templates",
		OutputDir:   "./dist",
		Workers:     4,
		Logger:      logger,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer hs.Stop()

	// Register page configurations
	registerProductPage(hs)
	registerCategoryPage(hs)
	registerHomePage(hs)

	// Start processing
	hs.Start()

	ctx := context.Background()

	// Generate all pages
	fmt.Println("🚀 Generating pages...")

	// Generate product pages
	productIDs := []string{"1", "2", "3", "4", "5"}
	if err := hs.GeneratePongoPages(ctx, "product-detail", productIDs); err != nil {
		log.Printf("generate products: %v", err)
	}
	fmt.Printf("   ✓ Generated %d product pages\n", len(productIDs))

	// Generate category pages
	categoryIDs := []string{"phones", "laptops"}
	if err := hs.GeneratePongoPages(ctx, "category-list", categoryIDs); err != nil {
		log.Printf("generate categories: %v", err)
	}
	fmt.Printf("   ✓ Generated %d category pages\n", len(categoryIDs))

	// Generate home page
	if err := hs.GeneratePongoPages(ctx, "home", []string{"index"}); err != nil {
		log.Printf("generate home: %v", err)
	}
	fmt.Println("   ✓ Generated home page")

	fmt.Println("\n📊 Stats:", hs.Stats())

	// HTTP API
	handler := hotstatic.NewHTTPHandler(hs.HotStatic)

	mux := http.NewServeMux()
	mux.Handle("/api/", handler.Router())

	// Serve static assets (JS, CSS, images)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	// Serve generated pages
	mux.Handle("/", http.FileServer(http.Dir("./dist")))

	fmt.Println("\n🌐 Server running on http://localhost:8080")
	fmt.Println("\n📄 Pages:")
	fmt.Println("   http://localhost:8080/index.html")
	fmt.Println("   http://localhost:8080/products/1.html")
	fmt.Println("   http://localhost:8080/categories/phones.html")
	fmt.Println("\n🔌 API:")
	fmt.Println("   POST /api/events - emit event")
	fmt.Println("   GET  /api/stats  - statistics")
	fmt.Println("\n💡 Try updating a product:")
	fmt.Println("   curl -X POST http://localhost:8080/api/events \\")
	fmt.Println("        -H 'Content-Type: application/json' \\")
	fmt.Println("        -d '{\"type\":\"product\",\"id\":\"1\",\"action\":\"updated\"}'")

	// HTTP сервер с graceful shutdown
	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Канал для сигналов завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Demo: emit event after 5 seconds
	go func() {
		time.Sleep(5 * time.Second)
		fmt.Println("\n⚡ Auto-emitting product update event...")
		hs.Emit("product:1", "updated")
	}()

	// Запуск сервера в горутине
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Ожидание сигнала завершения
	<-quit
	fmt.Println("\n🛑 Shutting down...")

	// Контекст с таймаутом для graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Остановка HTTP сервера
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	// HotStatic останавливается через defer hs.Stop()
	fmt.Println("✅ Shutdown complete")
}

func registerProductPage(hs *hotstatic.PongoHotStatic) {
	hs.RegisterPongoPage("product-detail", hotstatic.PongoPageConfig{
		PathPattern: "/products/{id}.html",
		Template:    "pages/product.html",
		DataLoader: func(ctx context.Context, params map[string]string) (map[string]any, []string, error) {
			id := params["id"]
			product, ok := products[id]
			if !ok {
				return nil, nil, fmt.Errorf("product not found: %s", id)
			}

			// Template context
			data := map[string]any{
				"product":    product,
				"active_nav": product.CategoryID,
				"breadcrumb": []map[string]string{
					{"label": "Home", "url": "/"},
					{"label": product.CategoryName, "url": "/categories/" + product.CategoryID + ".html"},
					{"label": product.Name, "url": ""},
				},
			}

			// Subscriptions
			subscriptions := []string{
				"product:" + product.ID,
				"brand:" + product.BrandID,
				"category:" + product.CategoryID,
			}

			return data, subscriptions, nil
		},
	})
}

func registerCategoryPage(hs *hotstatic.PongoHotStatic) {
	hs.RegisterPongoPage("category-list", hotstatic.PongoPageConfig{
		PathPattern: "/categories/{id}.html",
		Template:    "pages/category.html",
		DataLoader: func(ctx context.Context, params map[string]string) (map[string]any, []string, error) {
			categoryID := params["id"]
			category, ok := categories[categoryID]
			if !ok {
				return nil, nil, fmt.Errorf("category not found: %s", categoryID)
			}

			// Collect products in this category
			var categoryProducts []Product
			for _, p := range products {
				if p.CategoryID == categoryID {
					categoryProducts = append(categoryProducts, p)
				}
			}

			// Template context
			data := map[string]any{
				"category":   category,
				"products":   categoryProducts,
				"active_nav": categoryID,
				"breadcrumb": []map[string]string{
					{"label": "Home", "url": "/"},
					{"label": category.Name, "url": ""},
				},
			}

			// Subscriptions
			subs := []string{"category:" + categoryID}
			for _, p := range categoryProducts {
				subs = append(subs, "product:"+p.ID)
			}

			return data, subs, nil
		},
	})
}

func registerHomePage(hs *hotstatic.PongoHotStatic) {
	hs.RegisterPongoPage("home", hotstatic.PongoPageConfig{
		PathPattern: "/index.html",
		Template:    "pages/home.html",
		DataLoader: func(ctx context.Context, params map[string]string) (map[string]any, []string, error) {
			// Featured products (first 4)
			var featured []Product
			for _, p := range products {
				if p.InStock {
					featured = append(featured, p)
					if len(featured) >= 4 {
						break
					}
				}
			}

			// Categories with counts
			var cats []Category
			for _, cat := range categories {
				cats = append(cats, cat)
			}

			// Template context
			data := map[string]any{
				"featured_products": featured,
				"categories":        cats,
				"active_nav":        "home",
			}

			// Subscriptions - home page depends on everything
			subs := []string{"home:index"}
			for _, p := range products {
				subs = append(subs, "product:"+p.ID)
			}
			for id := range categories {
				subs = append(subs, "category:"+id)
			}

			return data, subs, nil
		},
	})
}
