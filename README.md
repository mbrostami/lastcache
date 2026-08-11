[![Go Reference](https://pkg.go.dev/badge/github.com/mbrostami/lastcache/v2.svg)](https://pkg.go.dev/github.com/mbrostami/lastcache/v2)
[![Go Report Card](https://goreportcard.com/badge/github.com/mbrostami/lastcache)](https://goreportcard.com/report/github.com/mbrostami/lastcache)
![Coverage](https://img.shields.io/badge/Coverage-100%25-brightgreen)
# LastCache

> **v2** is a generics rewrite with built-in single-flight. On the v1 API? See [Migrating from v1](#migrating-from-v1).

LastCache is a generic, concurrency-safe in-memory cache implementing
**stale-if-error** and **stale-while-revalidate**, with built-in **single-flight**
so a burst of concurrent requests for the same key triggers at most one fetch.

```
go get github.com/mbrostami/lastcache/v2
```

### stale-if-error (`Get` / `GetStale`)
When a fetch fails and a previous value is still around, the cache serves that
stale value instead of returning the error — for at most `Config.StaleTTL`
past expiry. The cap is a hard wall-clock bound anchored at the last
*successful* fetch: failed refreshes never extend it.

### stale-while-revalidate (`GetAsync`)
An expired value is returned immediately while a single background goroutine
refreshes it.

## Usage

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mbrostami/lastcache/v2"
)

func main() {
	cache := lastcache.New[string, string](lastcache.Config{
		TTL:      time.Minute,
		StaleTTL: 10 * time.Second, // serve stale up to 10s when a refresh fails
	})

	fetch := func(ctx context.Context, key string) (string, error) {
		// load from db / upstream / etc.
		return "value-for-" + key, nil
	}

	// Common case: fresh value, or transparently a stale one on error.
	v, err := cache.Get(context.Background(), "user:42", fetch)
	fmt.Println(v, err)

	// When you need to know it was served stale:
	res, err := cache.GetStale(context.Background(), "user:42", fetch)
	fmt.Println(res.Value, res.Stale, res.Err, err)

	// stale-while-revalidate: returns immediately, refreshes in the background.
	res = cache.GetAsync(context.Background(), "user:42", fetch)
	fmt.Println(res.Value, res.Stale)
}
```

## API

```go
func New[K comparable, V any](config Config) *Cache[K, V]

func (c *Cache[K, V]) Get(ctx, key, fetch) (V, error)            // fresh or stale-on-error, single-flighted
func (c *Cache[K, V]) GetStale(ctx, key, fetch) (Result[V], error) // same, but reports staleness
func (c *Cache[K, V]) GetAsync(ctx, key, fetch) Result[V]        // serve now, refresh in background
func (c *Cache[K, V]) Set(key, value)
func (c *Cache[K, V]) Delete(key)
func (c *Cache[K, V]) TTL(key) time.Duration
func (c *Cache[K, V]) Range(func(key K, value V, ttl time.Duration) bool)
```

`fetch` is a plain `func(ctx context.Context, key K) (V, error)`.

```go
type Result[V any] struct {
	Value V
	Stale bool  // value is expired but served (error or in-progress refresh)
	Err   error // underlying fetch error when stale; nil for fresh values
}
```

## Config

| Field | Meaning |
|-------|---------|
| `TTL` | How long a fetched value stays fresh. Defaults to 1 minute. |
| `StaleTTL` | How long a stale value may be served after expiry when a refresh fails. A hard cap anchored at the last successful fetch; past it the value is treated as gone. `0` disables serving stale on error. |
| `MaxConcurrentRefresh` | Caps concurrent background refreshes (`GetAsync`) across all keys. Defaults to `1`. |
| `Capacity` | Bounds the number of entries; dead entries are evicted first, then arbitrary ones. `<= 0` means unbounded. Set it whenever keys come from user input. |
| `OnError` | Optional `func(key any, err error)` called when a background refresh fails. |
| `Context` | Base context for background refreshes (they outlive the request). Defaults to `context.Background()`. |

## Migrating from v1

v2 is a rewrite:

- Generic `Cache[K, V]` instead of `any` (no more type assertions).
- `Get` / `GetStale` / `GetAsync` replace `LoadOrStore` / `AsyncLoadOrStore` and the `WithCtx` variants; `ctx` is now a parameter.
- The fetch callback is `(ctx, key) (V, error)` - the `useStale bool` return is gone; serving stale is controlled by `StaleTTL`.
- `GetAsync` returns a `Result` instead of a `chan error`; background errors go to `Config.OnError`.
- Config: `GlobalTTL` -> `TTL`, `ExtendTTL` -> `StaleTTL`, `AsyncSemaphore` -> `MaxConcurrentRefresh`.
- Built-in single-flight: concurrent requests for the same key share one fetch.
