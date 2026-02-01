package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

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

// ========== MAIN ==========

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Initialize HotStatic with pongo2
	hs, err := hotstatic.NewWithPongo(hotstatic.Config{
		TemplateDir: "./templates",
		OutputDir:   "./dist",
		Logger:      &slogLogger{logger},
	})
	if err != nil {
		log.Fatal(err)
	}

	// Add global variables available in all templates
	hs.AddGlobal("site", map[string]any{
		"name": "TechStore",
		"url":  "https://techstore.example.com",
	})

	// ========== DEFINE TEMPLATES ==========

	// Product pages
	hs.DefineTemplate("product", hotstatic.TemplateDef{
		File: "pages/product.jinja2",

		Load: func(ctx context.Context, id string) (*hotstatic.PageData, error) {
			product := getProduct(id)
			if product == nil || !product.InStock {
				return nil, nil // skip inactive products
			}

			return &hotstatic.PageData{
				Path: fmt.Sprintf("/products/%s.html", id),
				Data: map[string]any{
					"product":  product,
					"category": getCategory(product.CategoryID),
					"related":  getProductsByCategory(product.CategoryID),
				},
			}, nil
		},

		LoadAll: func(ctx context.Context) ([]string, error) {
			return getAllProductIDs(), nil
		},
	})

	// Category pages
	hs.DefineTemplate("category", hotstatic.TemplateDef{
		File: "pages/category.jinja2",

		Load: func(ctx context.Context, id string) (*hotstatic.PageData, error) {
			category := getCategory(id)
			if category == nil {
				return nil, nil
			}

			return &hotstatic.PageData{
				Path: fmt.Sprintf("/categories/%s.html", id),
				Data: map[string]any{
					"category": category,
					"products": getProductsByCategory(id),
				},
			}, nil
		},

		LoadAll: func(ctx context.Context) ([]string, error) {
			return getAllCategoryIDs(), nil
		},
	})

	// Home page
	hs.DefineTemplate("home", hotstatic.TemplateDef{
		File: "pages/home.jinja2",

		Load: func(ctx context.Context, id string) (*hotstatic.PageData, error) {
			return &hotstatic.PageData{
				Path: "/index.html",
				Data: map[string]any{
					"featured":   getFeaturedProducts(),
					"categories": getAllCategories(),
				},
			}, nil
		},

		LoadAll: func(ctx context.Context) ([]string, error) {
			return []string{""}, nil
		},
	})

	// ========== BUILD ALL ==========

	fmt.Println("Building all pages...")
	if err := hs.BuildAll(ctx); err != nil {
		log.Fatal(err)
	}

	fmt.Println("\nDone! Generated files in ./dist/")
	fmt.Println("  ./dist/index.html")
	fmt.Println("  ./dist/products/1.html")
	fmt.Println("  ./dist/products/2.html")
	fmt.Println("  ./dist/products/3.html")
	fmt.Println("  ./dist/categories/phones.html")
	fmt.Println("  ./dist/categories/laptops.html")
}

// slogLogger adapts slog.Logger to hotstatic.Logger interface
type slogLogger struct {
	*slog.Logger
}

func (l *slogLogger) Debug(msg string, args ...any) { l.Logger.Debug(msg, args...) }
func (l *slogLogger) Info(msg string, args ...any)  { l.Logger.Info(msg, args...) }
func (l *slogLogger) Warn(msg string, args ...any)  { l.Logger.Warn(msg, args...) }
func (l *slogLogger) Error(msg string, args ...any) { l.Logger.Error(msg, args...) }
