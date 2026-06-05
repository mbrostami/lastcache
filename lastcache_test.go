package lastcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var fixedTime = time.Unix(1000, 0)

// withClock pins the cache to a controllable clock for deterministic tests.
func withClock[K comparable, V any](c *Cache[K, V], t *time.Time) {
	c.clock = func() time.Time { return *t }
}

func TestNew_Defaults(t *testing.T) {
	c := New[string, string](Config{})
	if c.config.TTL != defaultTTL {
		t.Errorf("TTL = %v, want %v", c.config.TTL, defaultTTL)
	}
	if cap(c.semaphore) != 1 {
		t.Errorf("semaphore cap = %d, want 1", cap(c.semaphore))
	}
	if c.baseCtx == nil {
		t.Error("baseCtx must not be nil")
	}

	neg := New[string, string](Config{TTL: -5 * time.Second})
	if neg.config.TTL != defaultTTL {
		t.Errorf("negative TTL should fall back to default, got %v", neg.config.TTL)
	}
}

func TestGet_FreshHit_NoFetch(t *testing.T) {
	now := fixedTime
	c := New[string, int](Config{TTL: time.Minute})
	withClock(c, &now)
	c.Set("k", 1)

	now = now.Add(10 * time.Second) // still fresh
	calls := 0
	v, err := c.Get(context.Background(), "k", func(context.Context, string) (int, error) {
		calls++
		return 2, nil
	})
	if err != nil || v != 1 {
		t.Fatalf("got (%v,%v), want (1,nil)", v, err)
	}
	if calls != 0 {
		t.Fatalf("fetch should not run on a fresh hit, ran %d", calls)
	}
}

func TestGet_Expired_Refetches(t *testing.T) {
	now := fixedTime
	c := New[string, string](Config{TTL: time.Millisecond})
	withClock(c, &now)
	c.Set("k", "old")

	now = now.Add(time.Second) // expired
	v, err := c.Get(context.Background(), "k", func(context.Context, string) (string, error) {
		return "new", nil
	})
	if err != nil || v != "new" {
		t.Fatalf("got (%v,%v), want (new,nil)", v, err)
	}
}

func TestGet_ColdMiss_Error(t *testing.T) {
	c := New[string, string](Config{})
	_, err := c.Get(context.Background(), "k", func(context.Context, string) (string, error) {
		return "", errors.New("boom")
	})
	if err == nil {
		t.Fatal("want error on cold miss with failing fetch")
	}
}

func TestGetStale_ServesStaleOnError(t *testing.T) {
	now := fixedTime
	c := New[string, string](Config{TTL: time.Millisecond, StaleTTL: time.Minute})
	withClock(c, &now)
	c.Set("k", "stored")

	now = now.Add(time.Second) // expired
	res, err := c.GetStale(context.Background(), "k", func(context.Context, string) (string, error) {
		return "", errors.New("upstream down")
	})
	if err != nil {
		t.Fatalf("serving stale should not return an error, got %v", err)
	}
	if res.Value != "stored" || !res.Stale || res.Err == nil {
		t.Fatalf("got %+v, want stale 'stored' with underlying err", res)
	}
}

func TestGetStale_NoStaleTTL_ReturnsError(t *testing.T) {
	now := fixedTime
	c := New[string, string](Config{TTL: time.Millisecond}) // StaleTTL = 0
	withClock(c, &now)
	c.Set("k", "stored")

	now = now.Add(time.Second)
	_, err := c.GetStale(context.Background(), "k", func(context.Context, string) (string, error) {
		return "", errors.New("upstream down")
	})
	if err == nil {
		t.Fatal("with StaleTTL=0 a failing fetch must return the error")
	}
}

// The core fix: concurrent requests for the same expired key share one fetch.
func TestGet_SingleFlight(t *testing.T) {
	now := fixedTime
	c := New[string, string](Config{TTL: time.Millisecond})
	withClock(c, &now)
	c.Set("k", "old")
	now = now.Add(time.Second) // expired

	var calls int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Get(context.Background(), "k", func(context.Context, string) (string, error) {
				atomic.AddInt64(&calls, 1)
				time.Sleep(5 * time.Millisecond)
				return "new", nil
			})
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt64(&calls); n != 1 {
		t.Fatalf("single-flight failed: fetch ran %d times, want 1", n)
	}
}

func TestGetAsync_ColdMiss_Sync(t *testing.T) {
	c := New[string, string](Config{TTL: time.Minute})
	res := c.GetAsync(context.Background(), "k", func(context.Context, string) (string, error) {
		return "v", nil
	})
	if res.Value != "v" || res.Stale {
		t.Fatalf("got %+v, want fresh 'v'", res)
	}
}

func TestGetAsync_ServesStaleAndRefreshes(t *testing.T) {
	now := fixedTime
	c := New[string, string](Config{TTL: time.Millisecond, MaxConcurrentRefresh: 1})
	withClock(c, &now)
	c.Set("k", "old")
	now = now.Add(time.Second) // expired

	res := c.GetAsync(context.Background(), "k", func(context.Context, string) (string, error) {
		return "new", nil
	})
	if res.Value != "old" || !res.Stale {
		t.Fatalf("got %+v, want stale 'old'", res)
	}

	// Wait for the background refresh to land. We poll the store (concurrency-safe)
	// rather than writing the clock, which the refresh goroutine reads.
	deadline := time.Now().Add(time.Second)
	for {
		if v, ok := c.load("k"); ok && v.value == "new" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not update value within 1s")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGetAsync_OnErrorHook(t *testing.T) {
	now := fixedTime
	var gotErr error
	var mu sync.Mutex
	fired := make(chan struct{})
	c := New[string, string](Config{
		TTL: time.Millisecond,
		OnError: func(key any, err error) {
			mu.Lock()
			gotErr = err
			mu.Unlock()
			close(fired)
		},
	})
	withClock(c, &now)
	c.Set("k", "old")
	now = now.Add(time.Second)

	c.GetAsync(context.Background(), "k", func(context.Context, string) (string, error) {
		return "", errors.New("bg boom")
	})
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("OnError was not called")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotErr == nil {
		t.Fatal("OnError received nil error")
	}
}

func TestTTL_And_Delete(t *testing.T) {
	now := fixedTime
	c := New[string, string](Config{TTL: time.Second})
	withClock(c, &now)
	c.Set("k", "v")

	now = now.Add(100 * time.Millisecond)
	if got := c.TTL("k"); got != 900*time.Millisecond {
		t.Errorf("TTL = %v, want 900ms", got)
	}
	if got := c.TTL("missing"); got != 0 {
		t.Errorf("TTL(missing) = %v, want 0", got)
	}

	c.Delete("k")
	if _, ok := c.load("k"); ok {
		t.Error("key should be gone after Delete")
	}
}

func TestRange(t *testing.T) {
	now := fixedTime
	c := New[string, int](Config{TTL: time.Minute})
	withClock(c, &now)
	c.Set("a", 1)
	c.Set("b", 2)

	got := map[string]int{}
	c.Range(func(key string, value int, ttl time.Duration) bool {
		got[key] = value
		if ttl <= 0 {
			t.Errorf("ttl for %s should be positive, got %v", key, ttl)
		}
		return true
	})
	if got["a"] != 1 || got["b"] != 2 {
		t.Errorf("Range got %v", got)
	}
}

func TestConcurrency_Race(t *testing.T) {
	c := New[string, string](Config{})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Set("k", "v")
			c.Get(context.Background(), "k", func(context.Context, string) (string, error) {
				return "v", nil
			})
			c.GetAsync(context.Background(), "k2", func(context.Context, string) (string, error) {
				return "v2", nil
			})
			c.TTL("k")
			c.Delete("k")
		}()
	}
	wg.Wait()
}

func BenchmarkGet(b *testing.B) {
	c := New[string, string](Config{TTL: time.Minute})
	c.Set("key", "value")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if v, _ := c.Get(context.Background(), "key", func(context.Context, string) (string, error) {
			return "value", nil
		}); v != "value" {
			b.Fatalf("got %v", v)
		}
	}
}
