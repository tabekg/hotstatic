package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	prefixDependency = "hs:dep:"  // hs:dep:{key} -> set of page paths
	prefixPage       = "hs:page:" // hs:page:{path} -> page metadata JSON
	prefixPageDeps   = "hs:pdep:" // hs:pdep:{path} -> set of dependency keys
)

// PageMeta stores page metadata.
type PageMeta struct {
	Path         string            `json:"path"`
	Template     string            `json:"template"`
	Params       map[string]string `json:"params"`
	Dependencies []string          `json:"dependencies"`
	LastBuilt    time.Time         `json:"last_built"`
	ContentHash  string            `json:"content_hash,omitempty"`
}

// Registry manages page dependencies in Redis.
type Registry struct {
	client *redis.Client
	prefix string
}

// Config for Registry.
type Config struct {
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	Prefix        string
}

// New creates a new Registry.
func New(cfg Config) (*Registry, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}

	return &Registry{
		client: client,
		prefix: cfg.Prefix,
	}, nil
}

// NewWithClient creates Registry with existing Redis client.
func NewWithClient(client *redis.Client, prefix string) *Registry {
	return &Registry{
		client: client,
		prefix: prefix,
	}
}

// AddDependencies registers a page with its dependency keys.
func (r *Registry) AddDependencies(ctx context.Context, meta PageMeta) error {
	pipe := r.client.Pipeline()

	pageKey := r.key(prefixPage, meta.Path)
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal page meta: %w", err)
	}
	pipe.Set(ctx, pageKey, metaJSON, 0)

	oldDepsKey := r.key(prefixPageDeps, meta.Path)
	pipe.Del(ctx, oldDepsKey)

	for _, depKey := range meta.Dependencies {
		pipe.SAdd(ctx, r.key(prefixDependency, depKey), meta.Path)
		pipe.SAdd(ctx, oldDepsKey, depKey)
	}

	_, err = pipe.Exec(ctx)
	return err
}

// RemoveDependencies removes a page and all its dependencies.
func (r *Registry) RemoveDependencies(ctx context.Context, pagePath string) error {
	depsKey := r.key(prefixPageDeps, pagePath)
	deps, err := r.client.SMembers(ctx, depsKey).Result()
	if err != nil && err != redis.Nil {
		return err
	}

	pipe := r.client.Pipeline()

	for _, depKey := range deps {
		pipe.SRem(ctx, r.key(prefixDependency, depKey), pagePath)
	}

	pipe.Del(ctx, r.key(prefixPage, pagePath))
	pipe.Del(ctx, depsKey)

	_, err = pipe.Exec(ctx)
	return err
}

// GetDependents returns all page paths that depend on a key.
func (r *Registry) GetDependents(ctx context.Context, key string) ([]string, error) {
	return r.client.SMembers(ctx, r.key(prefixDependency, key)).Result()
}

// GetDependentsMulti returns pages that depend on any of the given keys.
func (r *Registry) GetDependentsMulti(ctx context.Context, keys []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	redisKeys := make([]string, len(keys))
	for i, k := range keys {
		redisKeys[i] = r.key(prefixDependency, k)
	}

	return r.client.SUnion(ctx, redisKeys...).Result()
}

// GetPage retrieves page metadata.
func (r *Registry) GetPage(ctx context.Context, path string) (*PageMeta, error) {
	data, err := r.client.Get(ctx, r.key(prefixPage, path)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var meta PageMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// UpdateLastBuilt updates the last build timestamp and hash.
func (r *Registry) UpdateLastBuilt(ctx context.Context, path string, timestamp time.Time, hash string) error {
	meta, err := r.GetPage(ctx, path)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("page not found: %s", path)
	}

	meta.LastBuilt = timestamp
	meta.ContentHash = hash

	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	return r.client.Set(ctx, r.key(prefixPage, path), metaJSON, 0).Err()
}

// ListPages returns all registered page paths.
func (r *Registry) ListPages(ctx context.Context) ([]string, error) {
	pattern := r.key(prefixPage, "*")
	var pages []string

	iter := r.client.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		path := strings.TrimPrefix(key, r.key(prefixPage, ""))
		pages = append(pages, path)
	}

	return pages, iter.Err()
}

// ListDependencyKeys returns all active dependency keys.
func (r *Registry) ListDependencyKeys(ctx context.Context) ([]string, error) {
	pattern := r.key(prefixDependency, "*")
	var keys []string

	iter := r.client.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		depKey := strings.TrimPrefix(key, r.key(prefixDependency, ""))
		keys = append(keys, depKey)
	}

	return keys, iter.Err()
}

// Stats returns registry statistics.
func (r *Registry) Stats(ctx context.Context) (map[string]int64, error) {
	stats := make(map[string]int64)

	pagePattern := r.key(prefixPage, "*")
	pageKeys, err := r.scanCount(ctx, pagePattern)
	if err != nil {
		return nil, err
	}
	stats["pages"] = int64(pageKeys)

	depPattern := r.key(prefixDependency, "*")
	depKeys, err := r.scanCount(ctx, depPattern)
	if err != nil {
		return nil, err
	}
	stats["dependency_keys"] = int64(depKeys)

	return stats, nil
}

// Clear removes all registry data.
func (r *Registry) Clear(ctx context.Context) error {
	patterns := []string{
		r.key(prefixDependency, "*"),
		r.key(prefixPage, "*"),
		r.key(prefixPageDeps, "*"),
	}

	for _, pattern := range patterns {
		iter := r.client.Scan(ctx, 0, pattern, 100).Iterator()
		var keys []string
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := r.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
	}

	return nil
}

// Close closes the Redis connection.
func (r *Registry) Close() error {
	return r.client.Close()
}

// Client returns the underlying Redis client.
func (r *Registry) Client() *redis.Client {
	return r.client
}

func (r *Registry) key(prefix, suffix string) string {
	if r.prefix != "" {
		return r.prefix + ":" + prefix + suffix
	}
	return prefix + suffix
}

func (r *Registry) scanCount(ctx context.Context, pattern string) (int, error) {
	count := 0
	iter := r.client.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		count++
	}
	return count, iter.Err()
}
