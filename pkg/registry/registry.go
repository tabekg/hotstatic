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
	// Redis key prefixes
	prefixSubscription = "hs:sub:"  // hs:sub:{key} -> set of page paths
	prefixPage         = "hs:page:" // hs:page:{path} -> page metadata JSON
	prefixPageSubs     = "hs:psub:" // hs:psub:{path} -> set of subscription keys
)

// PageMeta stores page metadata in Redis.
type PageMeta struct {
	Path          string            `json:"path"`
	Template      string            `json:"template"`
	Params        map[string]string `json:"params"`
	Subscriptions []string          `json:"subscriptions"`
	LastBuilt     time.Time         `json:"last_built"`
	ContentHash   string            `json:"content_hash,omitempty"`
}

// Registry manages page subscriptions in Redis.
type Registry struct {
	client *redis.Client
	prefix string
}

// Config for Registry.
type Config struct {
	// Redis connection string (e.g., "localhost:6379")
	RedisAddr string

	// Redis password (optional)
	RedisPassword string

	// Redis DB number
	RedisDB int

	// Key prefix for namespacing (default: "")
	Prefix string
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

// Subscribe registers a page for given subscription keys.
func (r *Registry) Subscribe(ctx context.Context, meta PageMeta) error {
	pipe := r.client.Pipeline()

	// Store page metadata
	pageKey := r.key(prefixPage, meta.Path)
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal page meta: %w", err)
	}
	pipe.Set(ctx, pageKey, metaJSON, 0)

	// Clear old subscriptions for this page
	oldSubsKey := r.key(prefixPageSubs, meta.Path)
	pipe.Del(ctx, oldSubsKey)

	// Add new subscriptions
	for _, subKey := range meta.Subscriptions {
		// Add page to subscription set
		pipe.SAdd(ctx, r.key(prefixSubscription, subKey), meta.Path)
		// Track which keys this page subscribes to
		pipe.SAdd(ctx, oldSubsKey, subKey)
	}

	_, err = pipe.Exec(ctx)
	return err
}

// Unsubscribe removes a page and all its subscriptions.
func (r *Registry) Unsubscribe(ctx context.Context, pagePath string) error {
	// Get current subscriptions for this page
	subsKey := r.key(prefixPageSubs, pagePath)
	subs, err := r.client.SMembers(ctx, subsKey).Result()
	if err != nil && err != redis.Nil {
		return err
	}

	pipe := r.client.Pipeline()

	// Remove page from each subscription set
	for _, subKey := range subs {
		pipe.SRem(ctx, r.key(prefixSubscription, subKey), pagePath)
	}

	// Delete page metadata and subscription tracking
	pipe.Del(ctx, r.key(prefixPage, pagePath))
	pipe.Del(ctx, subsKey)

	_, err = pipe.Exec(ctx)
	return err
}

// GetSubscribers returns all page paths subscribed to a key.
func (r *Registry) GetSubscribers(ctx context.Context, key string) ([]string, error) {
	return r.client.SMembers(ctx, r.key(prefixSubscription, key)).Result()
}

// GetSubscribersMulti returns pages subscribed to any of the given keys.
func (r *Registry) GetSubscribersMulti(ctx context.Context, keys []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	redisKeys := make([]string, len(keys))
	for i, k := range keys {
		redisKeys[i] = r.key(prefixSubscription, k)
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
		// Extract path from key
		path := strings.TrimPrefix(key, r.key(prefixPage, ""))
		pages = append(pages, path)
	}

	return pages, iter.Err()
}

// ListSubscriptionKeys returns all active subscription keys.
func (r *Registry) ListSubscriptionKeys(ctx context.Context) ([]string, error) {
	pattern := r.key(prefixSubscription, "*")
	var keys []string

	iter := r.client.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		subKey := strings.TrimPrefix(key, r.key(prefixSubscription, ""))
		keys = append(keys, subKey)
	}

	return keys, iter.Err()
}

// Stats returns registry statistics.
func (r *Registry) Stats(ctx context.Context) (map[string]int64, error) {
	stats := make(map[string]int64)

	// Count pages
	pagePattern := r.key(prefixPage, "*")
	pageKeys, err := r.scanCount(ctx, pagePattern)
	if err != nil {
		return nil, err
	}
	stats["pages"] = int64(pageKeys)

	// Count subscription keys
	subPattern := r.key(prefixSubscription, "*")
	subKeys, err := r.scanCount(ctx, subPattern)
	if err != nil {
		return nil, err
	}
	stats["subscription_keys"] = int64(subKeys)

	return stats, nil
}

// Clear removes all registry data (use with caution).
func (r *Registry) Clear(ctx context.Context) error {
	patterns := []string{
		r.key(prefixSubscription, "*"),
		r.key(prefixPage, "*"),
		r.key(prefixPageSubs, "*"),
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
