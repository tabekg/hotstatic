package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fmt"

	"github.com/tabekg/hotstatic"
)

// Пример: интернет-магазин
// Сущности: product, brand, category

// Product - товар
type Product struct {
	ID          string
	Name        string
	Price       float64
	BrandID     string
	CategoryID  string
	Description string
}

// Mock database
var products = map[string]Product{
	"1": {ID: "1", Name: "iPhone 15", Price: 999, BrandID: "apple", CategoryID: "phones", Description: "Latest iPhone"},
	"2": {ID: "2", Name: "Galaxy S24", Price: 899, BrandID: "samsung", CategoryID: "phones", Description: "Samsung flagship"},
	"3": {ID: "3", Name: "MacBook Pro", Price: 1999, BrandID: "apple", CategoryID: "laptops", Description: "Pro laptop"},
}

var brands = map[string]string{
	"apple":   "Apple Inc.",
	"samsung": "Samsung Electronics",
}

var categories = map[string]string{
	"phones":  "Смартфоны",
	"laptops": "Ноутбуки",
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Инициализация HotStatic
	hs, err := hotstatic.New(hotstatic.Config{
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

	// Регистрация inline шаблона для примера
	hs.RegisterTemplate("product-detail", productTemplate)
	hs.RegisterTemplate("category-list", categoryTemplate)

	// Регистрация конфигурации страниц (для будущего использования)
	hs.RegisterPage("product-detail", hotstatic.PageConfig{
		PathPattern: "/products/{id}.html",
		Template:    "product-detail",
	})

	hs.RegisterPage("category-list", hotstatic.PageConfig{
		PathPattern: "/categories/{id}.html",
		Template:    "category-list",
	})

	// Запуск
	hs.Start()

	// Генерация начальных страниц
	ctx := context.Background()

	// Генерируем страницы товаров
	for id, product := range products {
		data := map[string]any{
			"Product":     product,
			"Brand":       brands[product.BrandID],
			"Category":    categories[product.CategoryID],
			"GeneratedAt": time.Now().Format(time.RFC3339),
		}
		dependencies := []string{
			"product:" + product.ID,
			"brand:" + product.BrandID,
			"category:" + product.CategoryID,
		}
		err := hs.GeneratePage(ctx, hotstatic.Page{
			Path:         "/products/" + id + ".html",
			Template:     "product-detail",
			Dependencies: dependencies,
			Params:       map[string]string{"id": id},
		}, data)
		if err != nil {
			log.Printf("generate product %s: %v", id, err)
		}
	}

	// Генерируем страницы категорий
	for categoryID, categoryName := range categories {
		var categoryProducts []Product
		for _, p := range products {
			if p.CategoryID == categoryID {
				categoryProducts = append(categoryProducts, p)
			}
		}
		data := map[string]any{
			"CategoryID":   categoryID,
			"CategoryName": categoryName,
			"Products":     categoryProducts,
			"GeneratedAt":  time.Now().Format(time.RFC3339),
		}
		deps := []string{"category:" + categoryID}
		for _, p := range categoryProducts {
			deps = append(deps, "product:"+p.ID)
		}
		err := hs.GeneratePage(ctx, hotstatic.Page{
			Path:         "/categories/" + categoryID + ".html",
			Template:     "category-list",
			Dependencies: deps,
			Params:       map[string]string{"id": categoryID},
		}, data)
		if err != nil {
			log.Printf("generate category %s: %v", categoryID, err)
		}
	}

	fmt.Println("Initial pages generated!")
	fmt.Println("Stats:", hs.Stats())

	// HTTP API
	handler := hotstatic.NewHTTPHandler(hs)

	// Webhook для внешних систем
	webhook := hotstatic.NewWebhook(hs, "secret-token")

	mux := http.NewServeMux()
	mux.Handle("/", handler.Router())
	mux.Handle("/webhook", webhook.Handler())

	fmt.Println("\nHTTP API running on :8080")
	fmt.Println("Endpoints:")
	fmt.Println("  POST /api/events       - emit single event")
	fmt.Println("  POST /api/events/batch - emit multiple events")
	fmt.Println("  GET  /api/stats        - get statistics")
	fmt.Println("  GET  /api/pages        - list all pages")
	fmt.Println("  POST /api/build/all    - rebuild all pages")
	fmt.Println("  POST /webhook          - webhook endpoint")

	// Пример: эмитируем событие обновления товара с данными
	fmt.Println("\n--- Emitting product update event ---")
	product := products["1"]
	hs.EmitWithPayload("product:1", "updated", map[string]any{
		"Product":     product,
		"Brand":       brands[product.BrandID],
		"Category":    categories[product.CategoryID],
		"GeneratedAt": time.Now().Format(time.RFC3339),
	})

	// Ждём обработки
	time.Sleep(100 * time.Millisecond)
	fmt.Println("Stats after event:", hs.Stats())

	// HTTP сервер с graceful shutdown
	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Канал для сигналов завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

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

const productTemplate = `<!DOCTYPE html>
<html>
<head>
    <title>{{.Product.Name}}</title>
</head>
<body>
    <h1>{{.Product.Name}}</h1>
    <p>Brand: {{.Brand}}</p>
    <p>Category: {{.Category}}</p>
    <p>Price: ${{.Product.Price}}</p>
    <p>{{.Product.Description}}</p>
    <footer>Generated at: {{.GeneratedAt}}</footer>
</body>
</html>`

const categoryTemplate = `<!DOCTYPE html>
<html>
<head>
    <title>{{.CategoryName}}</title>
</head>
<body>
    <h1>{{.CategoryName}}</h1>
    <ul>
    {{range .Products}}
        <li><a href="/products/{{.ID}}.html">{{.Name}} - ${{.Price}}</a></li>
    {{end}}
    </ul>
    <footer>Generated at: {{.GeneratedAt}}</footer>
</body>
</html>`
