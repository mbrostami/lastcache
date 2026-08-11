// Package lastcache implements an in-memory cache with stale-if-error and
// stale-while-revalidate strategies, plus per-key single-flight so a burst of
// concurrent requests for the same key triggers at most one fetch.
//
//	stale-if-error (Get / GetStale)
//	When a fetch fails and a previous value is still around, the cache serves
//	that stale value instead of returning the error, for at most
//	Config.StaleTTL past expiry. The cap is anchored at the last successful
//	fetch: no amount of failed refreshes extends it.
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
	// refresh fails (stale-if-error). It is a hard wall-clock cap anchored at
	// the last successful fetch: past fetchedAt+TTL+StaleTTL the value is
	// treated as gone, however many refreshes failed in between, and the next
	// lookup fetches synchronously (GetAsync included).
	//
	// 0 disables serving stale on error: every expired Get re-runs the fetch
	// until it succeeds. GetAsync then keeps its serve-stale-while-refreshing
	// behavior without a staleness bound.
	StaleTTL time.Duration

	// MaxConcurrentRefresh bounds the number of background refreshes running at
	// once across all keys (GetAsync). Values <= 0 use 1.
	MaxConcurrentRefresh int

	// Capacity bounds the number of entries; values <= 0 mean unbounded.
	// When the cache is full, dead entries (past the staleness cap) are
	// evicted first, then arbitrary ones. Set a capacity whenever keys come
	// from outside (request paths, API keys, user input) so the cache cannot
	// grow without limit.
	Capacity int

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
	value     V
	fetchedAt time.Time // when the value was last fetched successfully (or Set)
}

type call[V any] struct {
	done chan struct{}
	val  V
	err  error
}

// Cache is a generic, concurrency-safe cache. Use New to construct one; it must
// not be copied after first use.
type Cache[K comparable, V any] struct {
	config     Config
	clock      func() time.Time
	baseCtx    context.Context
	mu         sync.RWMutex
	entries    map[K]item[V]
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
		entries:   make(map[K]item[V]),
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

	// Fetch failed: serve the last known value while it is within the hard
	// staleness cap. The item is left untouched, so the cap never moves and a
	// concurrent successful refresh is never overwritten with a stale copy.
	if c.config.StaleTTL > 0 {
		if it, ok := c.load(key); ok && !c.dead(it) {
			return Result[V]{Value: it.value, Stale: true, Err: err}, nil
		}
	}

	var zero V
	return Result[V]{Value: zero, Err: err}, err
}

// GetAsync returns the current value immediately. If it is expired, the stale
// value is returned (Result.Stale == true) and a single background goroutine
// refreshes the key. On a cold miss — or once a value is past the hard
// staleness cap — the fetch runs synchronously; if it fails, Result.Err is
// set. Background refresh errors are reported via Config.OnError.
func (c *Cache[K, V]) GetAsync(ctx context.Context, key K, fetch Fetch[K, V]) Result[V] {
	it, ok := c.load(key)
	if !ok || c.dead(it) {
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
	c.put(key, value)
}

// Delete removes key from the cache.
func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// TTL returns the remaining time before key expires. A negative value means the
// item is expired; zero means the key is not present.
func (c *Cache[K, V]) TTL(key K) time.Duration {
	if it, ok := c.load(key); ok {
		return c.expiry(it).Sub(c.clock())
	}
	return 0
}

// Range calls f for each key with its value and remaining TTL. Iteration stops
// if f returns false. It iterates over a snapshot taken when Range is called,
// so f may safely mutate the cache; mutations are not reflected in the
// iteration.
func (c *Cache[K, V]) Range(f func(key K, value V, ttl time.Duration) bool) {
	c.mu.RLock()
	snapshot := make(map[K]item[V], len(c.entries))
	for k, it := range c.entries {
		snapshot[k] = it
	}
	c.mu.RUnlock()

	now := c.clock()
	for k, it := range snapshot {
		if !f(k, it.value, c.expiry(it).Sub(now)) {
			return
		}
	}
}

// fetchOnce runs fetch for key, collapsing concurrent calls for the same key
// into a single fetch and caching a successful result.
//
// The shared fetch runs on a cancel-free copy of the initiating caller's
// context, so one caller giving up does not abort the fetch for the others.
// Each caller (the initiator included) waits with its own context and returns
// ctx.Err() if that is done first; the fetch still completes and is cached.
func (c *Cache[K, V]) fetchOnce(ctx context.Context, key K, fetch Fetch[K, V]) (V, error) {
	cl := &call[V]{done: make(chan struct{})}
	if actual, loaded := c.inflight.LoadOrStore(key, cl); loaded {
		cl = actual.(*call[V])
	} else {
		go func() {
			cl.val, cl.err = fetch(context.WithoutCancel(ctx), key)
			if cl.err == nil {
				c.Set(key, cl.val)
			}
			c.inflight.Delete(key)
			close(cl.done)
		}()
	}

	select {
	case <-cl.done:
		return cl.val, cl.err
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}
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
	c.mu.RLock()
	it, ok := c.entries[key]
	c.mu.RUnlock()
	return it, ok
}

func (c *Cache[K, V]) put(key K, value V) {
	now := c.clock()
	c.mu.Lock()
	c.entries[key] = item[V]{value: value, fetchedAt: now}
	c.evictLocked()
	c.mu.Unlock()
}

// evictLocked bounds the cache to Capacity, dropping dead entries first and
// then arbitrary ones. Must hold c.mu.
func (c *Cache[K, V]) evictLocked() {
	if c.config.Capacity <= 0 || len(c.entries) <= c.config.Capacity {
		return
	}
	for k, it := range c.entries {
		if len(c.entries) <= c.config.Capacity {
			return
		}
		if c.dead(it) {
			delete(c.entries, k)
		}
	}
	for k := range c.entries {
		if len(c.entries) <= c.config.Capacity {
			return
		}
		delete(c.entries, k)
	}
}

func (c *Cache[K, V]) expiry(it item[V]) time.Time {
	return it.fetchedAt.Add(c.config.TTL)
}

func (c *Cache[K, V]) expired(it item[V]) bool {
	return c.clock().After(c.expiry(it))
}

// dead reports whether it is past the hard staleness cap and may no longer be
// served, even stale. With StaleTTL == 0 nothing is ever dead: Get already
// refuses to serve stale, and GetAsync's staleness is deliberately unbounded.
func (c *Cache[K, V]) dead(it item[V]) bool {
	return c.config.StaleTTL > 0 && c.clock().After(c.expiry(it).Add(c.config.StaleTTL))
}

func (c *Cache[K, V]) onError(key K, err error) {
	if c.config.OnError != nil {
		c.config.OnError(key, err)
	}
}
