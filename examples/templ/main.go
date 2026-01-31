package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
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

	// Register templ page configurations
	registerProductPage(hs)
	registerCategoryPage(hs)
	registerHomePage(hs)

	// Start processing
	hs.Start()

	ctx := context.Background()

	// Generate all pages
	fmt.Println("Generating pages...")

	// Generate product pages
	productIDs := []string{"1", "2", "3", "4"}
	if err := hs.GenerateTemplPages(ctx, "product-detail", productIDs); err != nil {
		log.Printf("generate products: %v", err)
	}

	// Generate category pages
	categoryIDs := []string{"phones", "laptops"}
	if err := hs.GenerateTemplPages(ctx, "category-list", categoryIDs); err != nil {
		log.Printf("generate categories: %v", err)
	}

	// Generate home page
	if err := hs.GenerateTemplPages(ctx, "home", []string{"index"}); err != nil {
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

	// Demo: emit event after 5 seconds
	go func() {
		time.Sleep(5 * time.Second)
		fmt.Println("\n--- Emitting product update event ---")
		hs.Emit("product:1", "updated")
	}()

	log.Fatal(http.ListenAndServe(":8080", mux))
}

func registerProductPage(hs *hotstatic.TemplHotStatic) {
	hs.RegisterTemplPage("product-detail", hotstatic.TemplPageConfig{
		PathPattern: "/products/{id}.html",
		DataLoader: func(ctx context.Context, params map[string]string) (any, []string, error) {
			id := params["id"]
			product, ok := products[id]
			if !ok {
				return nil, nil, fmt.Errorf("product not found: %s", id)
			}

			subscriptions := []string{
				"product:" + product.ID,
				"brand:" + product.BrandID,
				"category:" + product.CategoryID,
			}

			return product, subscriptions, nil
		},
		Component: func(ctx context.Context, params map[string]string, data any) templ.Component {
			product := data.(templates.ProductData)
			return templates.ProductPage(product)
		},
	})
}

func registerCategoryPage(hs *hotstatic.TemplHotStatic) {
	hs.RegisterTemplPage("category-list", hotstatic.TemplPageConfig{
		PathPattern: "/categories/{id}.html",
		DataLoader: func(ctx context.Context, params map[string]string) (any, []string, error) {
			categoryID := params["id"]
			categoryName, ok := categories[categoryID]
			if !ok {
				return nil, nil, fmt.Errorf("category not found: %s", categoryID)
			}

			// Collect products in this category
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

			// Subscribe to category and all its products
			subs := []string{"category:" + categoryID}
			for _, p := range categoryProducts {
				subs = append(subs, "product:"+p.ID)
			}

			return data, subs, nil
		},
		Component: func(ctx context.Context, params map[string]string, data any) templ.Component {
			category := data.(templates.CategoryData)
			return templates.CategoryPage(category)
		},
	})
}

func registerHomePage(hs *hotstatic.TemplHotStatic) {
	hs.RegisterTemplPage("home", hotstatic.TemplPageConfig{
		PathPattern: "/index.html",
		DataLoader: func(ctx context.Context, params map[string]string) (any, []string, error) {
			// Collect featured products (first 3)
			var featured []templates.ProductData
			for _, p := range products {
				featured = append(featured, p)
				if len(featured) >= 3 {
					break
				}
			}

			// Collect category info
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

			// Subscribe to all products and categories
			subs := []string{"home:index"}
			for _, p := range products {
				subs = append(subs, "product:"+p.ID)
			}
			for id := range categories {
				subs = append(subs, "category:"+id)
			}

			return data, subs, nil
		},
		Component: func(ctx context.Context, params map[string]string, data any) templ.Component {
			homeData := data.(templates.HomeData)
			return templates.HomePage(homeData)
		},
	})
}
