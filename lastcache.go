// Package lastcache implements an in-memory cache with stale-if-error and
// stale-while-revalidate strategies, plus per-key single-flight so a burst of
// concurrent requests for the same key triggers at most one fetch.
//
//	stale-if-error (Get / GetStale)
//	When a fetch fails and a previous value is still around, the cache serves
//	that stale value for up to Config.StaleTTL instead of returning the error.
//
//	stale-while-revalidate (GetAsync)
//	An expired value is returned immediately while a single background goroutine
//	refreshes it.
package lastcache

import (
	"context"
	"sync"
	"time"
)

const defaultTTL = 1 * time.Minute

// Fetch retrieves the value for a key. It is called by the cache on a miss or
// when a value has expired.
type Fetch[K comparable, V any] func(ctx context.Context, key K) (V, error)

// Config configures a Cache. The zero value is valid and uses defaults.
type Config struct {
	// TTL is how long a fetched value stays fresh. Values <= 0 use defaultTTL.
	TTL time.Duration

	// StaleTTL is how long a stale value may be served after expiry when a
	// refresh fails (stale-if-error). 0 disables serving stale: every expired
	// Get then re-runs the fetch until it succeeds.
	StaleTTL time.Duration

	// MaxConcurrentRefresh bounds the number of background refreshes running at
	// once across all keys (GetAsync). Values <= 0 use 1.
	MaxConcurrentRefresh int

	// OnError, if set, is called with the underlying error when a background
	// refresh (GetAsync) fails. Foreground errors are returned to the caller.
	OnError func(key any, err error)

	// Context is the base context used for background refreshes, which outlive
	// the request that triggered them. Defaults to context.Background().
	Context context.Context
}

// Result carries a value plus whether it was served stale.
type Result[V any] struct {
	// Value is the cached or freshly fetched value. It is the zero value of V
	// when Err is set and nothing could be served.
	Value V

	// Stale is true when Value is an expired value served because a refresh
	// failed (GetStale) or is still in progress (GetAsync).
	Stale bool

	// Err holds the underlying fetch error when a stale value was served, or
	// the fetch error on a cold miss. It is nil for fresh values.
	Err error
}

type item[V any] struct {
	value  V
	expiry time.Time
}

type call[V any] struct {
	wg  sync.WaitGroup
	val V
	err error
}

// Cache is a generic, concurrency-safe cache. Use New to construct one; it must
// not be copied after first use.
type Cache[K comparable, V any] struct {
	config     Config
	clock      func() time.Time
	baseCtx    context.Context
	store      sync.Map // K -> item[V]
	inflight   sync.Map // K -> *call[V], for single-flight foreground fetches
	refreshing sync.Map // K -> struct{}, one background refresh per key
	semaphore  chan struct{}
}

// New returns a new Cache. A zero Config is valid.
func New[K comparable, V any](config Config) *Cache[K, V] {
	if config.TTL <= 0 {
		config.TTL = defaultTTL
	}

	sem := config.MaxConcurrentRefresh
	if sem <= 0 {
		sem = 1
	}

	base := config.Context
	if base == nil {
		base = context.Background()
	}

	return &Cache[K, V]{
		config:    config,
		clock:     time.Now,
		baseCtx:   base,
		semaphore: make(chan struct{}, sem),
	}
}

// Get returns the value for key, fetching it if missing or expired. On a fetch
// error it transparently serves a stale value when one is available and
// Config.StaleTTL > 0; otherwise it returns the error. Concurrent calls for the
// same key share a single fetch.
func (c *Cache[K, V]) Get(ctx context.Context, key K, fetch Fetch[K, V]) (V, error) {
	res, err := c.GetStale(ctx, key, fetch)
	return res.Value, err
}

// GetStale is like Get but reports whether the value was served stale via the
// returned Result. The error is non-nil only when nothing could be served.
func (c *Cache[K, V]) GetStale(ctx context.Context, key K, fetch Fetch[K, V]) (Result[V], error) {
	if it, ok := c.load(key); ok && !c.expired(it) {
		return Result[V]{Value: it.value}, nil
	}

	val, err := c.fetchOnce(ctx, key, fetch)
	if err == nil {
		return Result[V]{Value: val}, nil
	}

	// Fetch failed: serve the last known value if we still have one.
	if c.config.StaleTTL > 0 {
		if it, ok := c.load(key); ok {
			// Push the expiry out so a failing upstream isn't hammered.
			c.put(key, it.value, c.config.StaleTTL)
			return Result[V]{Value: it.value, Stale: true, Err: err}, nil
		}
	}

	var zero V
	return Result[V]{Value: zero, Err: err}, err
}

// GetAsync returns the current value immediately. If it is expired, the stale
// value is returned (Result.Stale == true) and a single background goroutine
// refreshes the key. On a cold miss the fetch runs synchronously; if it fails,
// Result.Err is set. Background refresh errors are reported via Config.OnError.
func (c *Cache[K, V]) GetAsync(ctx context.Context, key K, fetch Fetch[K, V]) Result[V] {
	it, ok := c.load(key)
	if !ok {
		val, err := c.fetchOnce(ctx, key, fetch)
		if err != nil {
			c.onError(key, err)
			return Result[V]{Err: err}
		}
		return Result[V]{Value: val}
	}

	if c.expired(it) {
		c.triggerRefresh(key, fetch)
		return Result[V]{Value: it.value, Stale: true}
	}

	return Result[V]{Value: it.value}
}

// Set stores value for key with the configured TTL.
func (c *Cache[K, V]) Set(key K, value V) {
	c.put(key, value, c.config.TTL)
}

// Delete removes key from the cache.
func (c *Cache[K, V]) Delete(key K) {
	c.store.Delete(key)
}

// TTL returns the remaining time before key expires. A negative value means the
// item is expired; zero means the key is not present.
func (c *Cache[K, V]) TTL(key K) time.Duration {
	if it, ok := c.load(key); ok {
		return it.expiry.Sub(c.clock())
	}
	return 0
}

// Range calls f for each key with its value and remaining TTL. Iteration stops
// if f returns false. It follows sync.Map.Range semantics (no consistent
// snapshot).
func (c *Cache[K, V]) Range(f func(key K, value V, ttl time.Duration) bool) {
	c.store.Range(func(k, v any) bool {
		it := v.(item[V])
		return f(k.(K), it.value, it.expiry.Sub(c.clock()))
	})
}

// fetchOnce runs fetch for key, collapsing concurrent calls for the same key
// into a single fetch and caching a successful result.
func (c *Cache[K, V]) fetchOnce(ctx context.Context, key K, fetch Fetch[K, V]) (V, error) {
	cl := &call[V]{}
	cl.wg.Add(1)
	actual, loaded := c.inflight.LoadOrStore(key, cl)
	if loaded {
		existing := actual.(*call[V])
		existing.wg.Wait()
		return existing.val, existing.err
	}

	cl.val, cl.err = fetch(ctx, key)
	if cl.err == nil {
		c.Set(key, cl.val)
	}
	c.inflight.Delete(key)
	cl.wg.Done()
	return cl.val, cl.err
}

// triggerRefresh starts at most one background refresh per key, bounded across
// keys by the semaphore.
func (c *Cache[K, V]) triggerRefresh(key K, fetch Fetch[K, V]) {
	if _, busy := c.refreshing.LoadOrStore(key, struct{}{}); busy {
		return
	}

	go func() {
		defer c.refreshing.Delete(key)

		c.semaphore <- struct{}{}
		defer func() { <-c.semaphore }()

		// Another refresh may have already updated the key while we waited.
		if it, ok := c.load(key); ok && !c.expired(it) {
			return
		}

		val, err := fetch(c.baseCtx, key)
		if err != nil {
			c.onError(key, err)
			return
		}
		c.Set(key, val)
	}()
}

func (c *Cache[K, V]) load(key K) (item[V], bool) {
	v, ok := c.store.Load(key)
	if !ok {
		var zero item[V]
		return zero, false
	}
	return v.(item[V]), true
}

func (c *Cache[K, V]) put(key K, value V, ttl time.Duration) {
	c.store.Store(key, item[V]{value: value, expiry: c.clock().Add(ttl)})
}

func (c *Cache[K, V]) expired(it item[V]) bool {
	return c.clock().After(it.expiry)
}

func (c *Cache[K, V]) onError(key K, err error) {
	if c.config.OnError != nil {
		c.config.OnError(key, err)
	}
}
