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

	"github.com/a-h/templ"
	"github.com/tabekg/hotstatic"
	"github.com/tabekg/hotstatic/examples/templ/templates"
)

// Mock database
var products = map[string]templates.ProductData{
	"1": {
		ID:           "1",
		Name:         "iPhone 15 Pro",
		Price:        999.00,
		Description:  "The most powerful iPhone ever. Featuring the A17 Pro chip, titanium design, and an advanced camera system.",
		BrandID:      "apple",
		BrandName:    "Apple",
		CategoryID:   "phones",
		CategoryName: "Smartphones",
		Features:     []string{"A17 Pro chip", "Titanium design", "48MP camera", "USB-C", "Action button"},
		InStock:      true,
	},
	"2": {
		ID:           "2",
		Name:         "Galaxy S24 Ultra",
		Price:        1199.00,
		Description:  "The ultimate Galaxy experience with built-in S Pen, AI features, and a stunning display.",
		BrandID:      "samsung",
		BrandName:    "Samsung",
		CategoryID:   "phones",
		CategoryName: "Smartphones",
		Features:     []string{"Snapdragon 8 Gen 3", "S Pen included", "200MP camera", "Titanium frame"},
		InStock:      true,
	},
	"3": {
		ID:           "3",
		Name:         "MacBook Pro 16\"",
		Price:        2499.00,
		Description:  "Supercharged by M3 Pro or M3 Max. The most advanced Mac laptops for demanding workflows.",
		BrandID:      "apple",
		BrandName:    "Apple",
		CategoryID:   "laptops",
		CategoryName: "Laptops",
		Features:     []string{"M3 Pro/Max chip", "Up to 22hr battery", "Liquid Retina XDR", "MagSafe 3"},
		InStock:      true,
	},
	"4": {
		ID:           "4",
		Name:         "ThinkPad X1 Carbon",
		Price:        1849.00,
		Description:  "Ultralight business laptop with legendary ThinkPad reliability and security features.",
		BrandID:      "lenovo",
		BrandName:    "Lenovo",
		CategoryID:   "laptops",
		CategoryName: "Laptops",
		Features:     []string{"Intel Core Ultra", "Carbon fiber chassis", "ThinkShield security", "2.48 lbs"},
		InStock:      false,
	},
}

var categories = map[string]string{
	"phones":  "Smartphones",
	"laptops": "Laptops",
}

var brands = map[string]string{
	"apple":   "Apple",
	"samsung": "Samsung",
	"lenovo":  "Lenovo",
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Initialize HotStatic with templ support
	hs, err := hotstatic.NewWithTempl(hotstatic.Config{
		Redis:     "localhost:6379",
		OutputDir: "./dist",
		Workers:   4,
		Logger:    logger,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer hs.Stop()

	// Register templ page configurations with Component factories
	registerTemplConfigs(hs)

	// Start processing
	hs.Start()

	ctx := context.Background()

	// Generate all pages
	fmt.Println("Generating pages...")

	// Generate product pages
	for id, product := range products {
		deps := []string{
			"product:" + product.ID,
			"brand:" + product.BrandID,
			"category:" + product.CategoryID,
		}
		err := hs.GenerateTemplPage(ctx, "product-detail", hotstatic.Page{
			Path:         "/products/" + id + ".html",
			Dependencies: deps,
			Params:       map[string]string{"id": id},
		}, product)
		if err != nil {
			log.Printf("generate product %s: %v", id, err)
		}
	}

	// Generate category pages
	for categoryID, categoryName := range categories {
		var categoryProducts []templates.ProductData
		for _, p := range products {
			if p.CategoryID == categoryID {
				categoryProducts = append(categoryProducts, p)
			}
		}
		data := templates.CategoryData{
			ID:       categoryID,
			Name:     categoryName,
			Products: categoryProducts,
		}
		deps := []string{"category:" + categoryID}
		for _, p := range categoryProducts {
			deps = append(deps, "product:"+p.ID)
		}
		err := hs.GenerateTemplPage(ctx, "category-list", hotstatic.Page{
			Path:         "/categories/" + categoryID + ".html",
			Dependencies: deps,
			Params:       map[string]string{"id": categoryID},
		}, data)
		if err != nil {
			log.Printf("generate category %s: %v", categoryID, err)
		}
	}

	// Generate home page
	homeData, homeDeps := getHomeData()
	err = hs.GenerateTemplPage(ctx, "home", hotstatic.Page{
		Path:         "/index.html",
		Dependencies: homeDeps,
		Params:       map[string]string{},
	}, homeData)
	if err != nil {
		log.Printf("generate home: %v", err)
	}

	fmt.Println("Pages generated!")
	fmt.Println("Stats:", hs.Stats())

	// HTTP API
	handler := hotstatic.NewHTTPHandler(hs.HotStatic)

	mux := http.NewServeMux()
	mux.Handle("/api/", handler.Router())

	// Serve static files
	mux.Handle("/", http.FileServer(http.Dir("./dist")))

	fmt.Println("\nServer running on http://localhost:8080")
	fmt.Println("API endpoints:")
	fmt.Println("  POST /api/events - emit event")
	fmt.Println("  GET  /api/stats  - statistics")
	fmt.Println("\nTry:")
	fmt.Println("  curl http://localhost:8080/index.html")
	fmt.Println("  curl http://localhost:8080/products/1.html")
	fmt.Println("  curl -X POST http://localhost:8080/api/events -d '{\"type\":\"product\",\"id\":\"1\",\"action\":\"updated\"}'")

	// HTTP сервер с graceful shutdown
	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Канал для сигналов завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Demo: emit event after 5 seconds (with payload)
	go func() {
		time.Sleep(5 * time.Second)
		fmt.Println("\n--- Emitting product update event ---")
		// For templ, emit doesn't work without proper handler for payload
		// This is just a demo - in real app you'd emit with proper data
		product := products["1"]
		subs := []string{"product:" + product.ID, "brand:" + product.BrandID, "category:" + product.CategoryID}
		_ = subs // subscriptions would be used in real scenario
		fmt.Println("Product update would trigger rebuild with payload")
	}()

	// Запуск сервера в горутине
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Ожидание сигнала завершения
	<-quit
	fmt.Println("\nShutting down...")

	// Контекст с таймаутом для graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Остановка HTTP сервера
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	// HotStatic останавливается через defer hs.Stop()
	fmt.Println("Shutdown complete")
}

func registerTemplConfigs(hs *hotstatic.TemplHotStatic) {
	// Product page - converts ProductData to component
	hs.RegisterTemplPage("product-detail", hotstatic.TemplPageConfig{
		PathPattern: "/products/{id}.html",
		Component: func(ctx context.Context, params map[string]string, data any) templ.Component {
			product := data.(templates.ProductData)
			return templates.ProductPage(product)
		},
	})

	// Category page - converts CategoryData to component
	hs.RegisterTemplPage("category-list", hotstatic.TemplPageConfig{
		PathPattern: "/categories/{id}.html",
		Component: func(ctx context.Context, params map[string]string, data any) templ.Component {
			category := data.(templates.CategoryData)
			return templates.CategoryPage(category)
		},
	})

	// Home page - converts HomeData to component
	hs.RegisterTemplPage("home", hotstatic.TemplPageConfig{
		PathPattern: "/index.html",
		Component: func(ctx context.Context, params map[string]string, data any) templ.Component {
			homeData := data.(templates.HomeData)
			return templates.HomePage(homeData)
		},
	})
}

func getHomeData() (templates.HomeData, []string) {
	var featured []templates.ProductData
	for _, p := range products {
		featured = append(featured, p)
		if len(featured) >= 3 {
			break
		}
	}

	var cats []templates.CategoryInfo
	for id, name := range categories {
		count := 0
		for _, p := range products {
			if p.CategoryID == id {
				count++
			}
		}
		cats = append(cats, templates.CategoryInfo{
			ID:    id,
			Name:  name,
			Count: count,
		})
	}

	data := templates.HomeData{
		FeaturedProducts: featured,
		Categories:       cats,
	}

	// Home page depends on all products and categories
	deps := []string{}
	for _, p := range products {
		deps = append(deps, "product:"+p.ID)
	}
	for id := range categories {
		deps = append(deps, "category:"+id)
	}

	return data, deps
}
