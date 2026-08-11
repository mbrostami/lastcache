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

// Config.Clock lets tests control expiry without sleeping or touching
// internals.
func TestConfig_Clock(t *testing.T) {
	var mu sync.Mutex
	now := fixedTime
	c := New[string, string](Config{
		TTL: time.Minute,
		Clock: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
	})
	c.Set("k", "v")

	var calls int32
	fetch := func(context.Context, string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "v2", nil
	}

	if v, _ := c.Get(context.Background(), "k", fetch); v != "v" || atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("fresh hit: got %q with %d fetches, want \"v\" with 0", v, atomic.LoadInt32(&calls))
	}

	mu.Lock()
	now = now.Add(2 * time.Minute) // expire
	mu.Unlock()

	if v, _ := c.Get(context.Background(), "k", fetch); v != "v2" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("after expiry: got %q with %d fetches, want \"v2\" with 1", v, atomic.LoadInt32(&calls))
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

// The staleness cap is a hard wall-clock bound from the last successful fetch:
// repeated failed refreshes do not extend it, and past it the error is
// returned instead of the stale value.
func TestGetStale_HardCap(t *testing.T) {
	now := fixedTime
	c := New[string, string](Config{TTL: time.Second, StaleTTL: 10 * time.Second})
	withClock(c, &now)
	c.Set("k", "stored")
	failing := func(context.Context, string) (string, error) {
		return "", errors.New("upstream down")
	}

	// Expired but within the cap: served stale, repeatedly.
	now = now.Add(5 * time.Second)
	for i := 0; i < 3; i++ {
		res, err := c.GetStale(context.Background(), "k", failing)
		if err != nil || res.Value != "stored" || !res.Stale {
			t.Fatalf("within cap: got (%+v,%v), want stale 'stored'", res, err)
		}
	}

	// Past fetchedAt+TTL+StaleTTL: the failed refreshes above must not have
	// pushed the cap out.
	now = fixedTime.Add(12 * time.Second)
	if _, err := c.GetStale(context.Background(), "k", failing); err == nil {
		t.Fatal("past the hard cap a failing fetch must return the error")
	}
}

// GetAsync stops serving a value past the hard cap and falls back to a
// synchronous fetch, like a cold miss.
func TestGetAsync_HardCap(t *testing.T) {
	now := fixedTime
	c := New[string, string](Config{TTL: time.Second, StaleTTL: 10 * time.Second})
	withClock(c, &now)
	c.Set("k", "stored")

	now = now.Add(12 * time.Second) // past the cap
	res := c.GetAsync(context.Background(), "k", func(context.Context, string) (string, error) {
		return "new", nil
	})
	if res.Value != "new" || res.Stale || res.Err != nil {
		t.Fatalf("got %+v, want fresh 'new' fetched synchronously", res)
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

// A waiter whose context is canceled gets ctx.Err() immediately instead of
// blocking until the shared fetch finishes.
func TestGet_WaiterHonorsOwnContext(t *testing.T) {
	c := New[string, string](Config{TTL: time.Minute})

	release := make(chan struct{})
	started := make(chan struct{})
	fetch := func(context.Context, string) (string, error) {
		close(started)
		<-release
		return "v", nil
	}

	// Initiate the shared fetch and keep it blocked.
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		if v, err := c.Get(context.Background(), "k", fetch); err != nil || v != "v" {
			t.Errorf("initiator got (%v,%v), want (v,nil)", v, err)
		}
	}()
	<-started

	// A second caller joins the in-flight fetch but cancels while waiting.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(ctx, "k", fetch); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter got %v, want context.Canceled", err)
	}

	close(release)
	<-leaderDone
}

// Canceling the context of the caller that initiated the shared fetch must not
// poison the result for the other callers: the fetch runs on a cancel-free
// context and its result is still cached.
func TestGet_InitiatorCancelDoesNotAbortSharedFetch(t *testing.T) {
	c := New[string, string](Config{TTL: time.Minute})

	release := make(chan struct{})
	started := make(chan struct{})
	fetch := func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-release
		// The fetch context must outlive the initiator's cancellation.
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "v", nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	initiatorDone := make(chan struct{})
	go func() {
		defer close(initiatorDone)
		if _, err := c.Get(ctx, "k", fetch); !errors.Is(err, context.Canceled) {
			t.Errorf("initiator got %v, want context.Canceled", err)
		}
	}()
	<-started

	// A second caller joins, then the initiator gives up.
	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		if v, err := c.Get(context.Background(), "k", fetch); err != nil || v != "v" {
			t.Errorf("waiter got (%v,%v), want (v,nil)", v, err)
		}
	}()
	cancel()
	<-initiatorDone

	close(release)
	<-waiterDone

	if v, ok := c.load("k"); !ok || v.value != "v" {
		t.Fatalf("fetch result was not cached, got (%v,%v)", v.value, ok)
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

func TestGetAsync_ColdMiss_Error(t *testing.T) {
	var hookCalls int32
	c := New[string, string](Config{
		OnError: func(key any, err error) { atomic.AddInt32(&hookCalls, 1) },
	})
	res := c.GetAsync(context.Background(), "missing", func(context.Context, string) (string, error) {
		return "", errors.New("boom")
	})
	if res.Err == nil {
		t.Fatal("want Result.Err on a failed cold-miss fetch")
	}
	if res.Value != "" || res.Stale {
		t.Fatalf("want zero/non-stale result, got %+v", res)
	}
	if atomic.LoadInt32(&hookCalls) != 1 {
		t.Fatalf("OnError fired %d times, want 1", atomic.LoadInt32(&hookCalls))
	}
}

// A second GetAsync while a refresh is already in flight must not start another.
func TestGetAsync_DedupesBackgroundRefresh(t *testing.T) {
	now := fixedTime
	c := New[string, string](Config{TTL: time.Millisecond})
	withClock(c, &now)
	c.Set("k", "old")
	now = now.Add(time.Second) // expired (no more writes to now after this)

	var calls int32
	release := make(chan struct{})
	fetch := func(context.Context, string) (string, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return "new", nil
	}

	c.GetAsync(context.Background(), "k", fetch) // starts a background refresh that blocks
	res := c.GetAsync(context.Background(), "k", fetch) // must find one already running
	if !res.Stale || res.Value != "old" {
		t.Fatalf("second call got %+v, want stale 'old'", res)
	}
	close(release)

	deadline := time.Now().Add(time.Second)
	for {
		if v, ok := c.load("k"); ok && v.value == "new" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not land")
		}
		time.Sleep(time.Millisecond)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("background fetch ran %d times, want 1 (deduped)", n)
	}
}

// If the key is refreshed by another path while the background goroutine waits
// for the refresh slot, the goroutine must skip its fetch and not overwrite.
func TestGetAsync_SkipsRefreshIfAlreadyFresh(t *testing.T) {
	now := fixedTime
	c := New[string, string](Config{TTL: time.Millisecond, MaxConcurrentRefresh: 1})
	withClock(c, &now)
	c.Set("k", "old")
	c.Set("other", "x")
	now = now.Add(time.Second) // both expired (no more writes to now after this)

	// Occupy the single refresh slot with a blocked refresh on "other".
	otherStarted := make(chan struct{})
	blockOther := make(chan struct{})
	c.GetAsync(context.Background(), "other", func(context.Context, string) (string, error) {
		close(otherStarted)
		<-blockOther
		return "x2", nil
	})
	<-otherStarted // the slot is now held

	var kFetched int32
	c.GetAsync(context.Background(), "k", func(context.Context, string) (string, error) {
		atomic.AddInt32(&kFetched, 1)
		return "new-from-fetch", nil
	})

	// Make k fresh via a direct Set while its refresh goroutine waits for the slot.
	c.Set("k", "fresh-direct")
	close(blockOther) // release the slot; k's goroutine wakes and re-checks freshness

	deadline := time.Now().Add(time.Second)
	for {
		if _, busy := c.refreshing.Load("k"); !busy {
			break // k's refresh goroutine finished
		}
		if time.Now().After(deadline) {
			t.Fatal("k refresh goroutine never finished")
		}
		time.Sleep(time.Millisecond)
	}

	if n := atomic.LoadInt32(&kFetched); n != 0 {
		t.Fatalf("k fetch ran %d times, want 0 (already fresh)", n)
	}
	if v, _ := c.load("k"); v.value != "fresh-direct" {
		t.Fatalf("k value = %q, want fresh-direct (stale refresh must not overwrite)", v.value)
	}
}

var errNotFound = errors.New("not found")

func notFoundConfig(negTTL time.Duration) Config {
	return Config{
		TTL:         time.Second,
		StaleTTL:    time.Minute,
		NegativeTTL: negTTL,
		NotFound:    func(err error) bool { return errors.Is(err, errNotFound) },
	}
}

// An authoritative miss is cached: repeat lookups within NegativeTTL are
// answered from cache, and after expiry the next lookup re-fetches.
func TestNegative_CachedAndExpires(t *testing.T) {
	now := fixedTime
	c := New[string, string](notFoundConfig(5 * time.Second))
	withClock(c, &now)

	var calls int32
	exists := false
	fetch := func(context.Context, string) (string, error) {
		atomic.AddInt32(&calls, 1)
		if exists {
			return "v", nil
		}
		return "", errNotFound
	}

	for i := 0; i < 3; i++ { // one fetch, then served from the negative entry
		if _, err := c.Get(context.Background(), "k", fetch); !errors.Is(err, errNotFound) {
			t.Fatalf("want errNotFound, got %v", err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("fetch ran %d times, want 1 (negative hit)", n)
	}

	// Past NegativeTTL the miss is re-fetched and the new value found.
	exists = true
	now = now.Add(6 * time.Second)
	if v, err := c.Get(context.Background(), "k", fetch); err != nil || v != "v" {
		t.Fatalf("after negative expiry: got (%v,%v), want (v,nil)", v, err)
	}
}

// An authoritative miss evicts a cached value: despite StaleTTL, the stale
// value must not be served once the upstream said the key is gone.
func TestNegative_AuthoritativeMissEvictsValue(t *testing.T) {
	now := fixedTime
	c := New[string, string](notFoundConfig(5 * time.Second))
	withClock(c, &now)
	c.Set("k", "stored")
	now = now.Add(2 * time.Second) // expired, well within the stale cap

	var calls int32
	fetch := func(context.Context, string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "", errNotFound
	}

	res, err := c.GetStale(context.Background(), "k", fetch)
	if !errors.Is(err, errNotFound) || res.Stale {
		t.Fatalf("authoritative miss must not serve stale, got (%+v,%v)", res, err)
	}
	// Evicted and negatively cached: answered without another fetch.
	if _, err := c.Get(context.Background(), "k", fetch); !errors.Is(err, errNotFound) {
		t.Fatalf("want errNotFound, got %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("fetch ran %d times, want 1", n)
	}
}

// With NegativeTTL = 0 an authoritative miss still evicts the value but is not
// cached: every lookup re-fetches.
func TestNegative_DisabledStillEvicts(t *testing.T) {
	now := fixedTime
	c := New[string, string](notFoundConfig(0))
	withClock(c, &now)
	c.Set("k", "stored")
	now = now.Add(2 * time.Second)

	var calls int32
	fetch := func(context.Context, string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "", errNotFound
	}
	for i := 0; i < 2; i++ {
		if _, err := c.Get(context.Background(), "k", fetch); !errors.Is(err, errNotFound) {
			t.Fatalf("want errNotFound, got %v", err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("fetch ran %d times, want 2 (no negative caching)", n)
	}
	if _, ok := c.load("k"); ok {
		t.Fatal("value must be evicted on authoritative miss")
	}
}

// A background refresh that comes back "not found" evicts the value, stores a
// negative entry, and does not fire OnError.
func TestGetAsync_BackgroundNotFound(t *testing.T) {
	now := fixedTime
	var onErrCalls int32
	cfg := notFoundConfig(5 * time.Second)
	cfg.OnError = func(any, error) { atomic.AddInt32(&onErrCalls, 1) }
	c := New[string, string](cfg)
	withClock(c, &now)
	c.Set("k", "stored")
	now = now.Add(2 * time.Second) // expired (no clock writes after this)

	res := c.GetAsync(context.Background(), "k", func(context.Context, string) (string, error) {
		return "", errNotFound
	})
	if res.Value != "stored" || !res.Stale {
		t.Fatalf("got %+v, want stale 'stored' while the refresh runs", res)
	}

	deadline := time.Now().Add(time.Second)
	for {
		if it, ok := c.load("k"); ok && it.err != nil {
			break // negative entry landed
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not store a negative entry")
		}
		time.Sleep(time.Millisecond)
	}

	var calls int32
	res = c.GetAsync(context.Background(), "k", func(context.Context, string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "x", nil
	})
	if !errors.Is(res.Err, errNotFound) || atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("want cached errNotFound with no fetch, got (%+v, %d calls)", res, calls)
	}
	if n := atomic.LoadInt32(&onErrCalls); n != 0 {
		t.Fatalf("OnError fired %d times for an authoritative miss, want 0", n)
	}
}

// len reports the current number of entries (test helper).
func cacheLen[K comparable, V any](c *Cache[K, V]) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func TestCapacity_Bounded(t *testing.T) {
	c := New[string, int](Config{TTL: time.Minute, Capacity: 3})
	for i := 0; i < 10; i++ {
		c.Set(string(rune('a'+i)), i)
	}
	if n := cacheLen(c); n != 3 {
		t.Fatalf("len = %d, want 3", n)
	}
}

// Dead entries (past the staleness cap) are evicted before live ones.
func TestCapacity_EvictsDeadFirst(t *testing.T) {
	now := fixedTime
	c := New[string, int](Config{TTL: time.Second, StaleTTL: 10 * time.Second, Capacity: 2})
	withClock(c, &now)

	c.Set("dead", 1)
	now = now.Add(time.Minute) // "dead" is now past TTL+StaleTTL
	c.Set("live", 2)
	c.Set("live2", 3) // over capacity: must evict "dead", not a live entry

	if _, ok := c.load("dead"); ok {
		t.Error("dead entry should have been evicted")
	}
	if _, ok := c.load("live"); !ok {
		t.Error("live entry was evicted while a dead one existed")
	}
	if _, ok := c.load("live2"); !ok {
		t.Error("just-inserted entry must survive eviction")
	}
	if n := cacheLen(c); n != 2 {
		t.Fatalf("len = %d, want 2", n)
	}
}

func TestCapacity_ZeroMeansUnbounded(t *testing.T) {
	c := New[string, int](Config{TTL: time.Minute}) // Capacity unset
	for i := 0; i < 100; i++ {
		c.Set(string(rune(i)), i)
	}
	if n := cacheLen(c); n != 100 {
		t.Fatalf("len = %d, want 100 (unbounded)", n)
	}
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
