// Package lastcache implements stale-while-revalidate and stale-if-error in-memory cache strategy.
//
//	stale-if-error
//	In the event of an error when fetching fresh data, the cache serves stale (expired) data for a specified period (Config.ExtendTTL). This ensures a fallback mechanism to provide some data even when the retrieval process encounters errors.
//	`LoadOrStore` function is based on this strategy.
//
//	stale-while-revalidate
//	Stale (expired) data is served to caller while a background process runs to refresh the cache.
//	`AsyncLoadOrStore` function is based on this strategy.
package lastcache

import (
	"context"
	"sync"
	"time"
)

const defaultTTL = 1 * time.Minute

const defaultSemaphore int = 1

var now = time.Now

// SyncCallback given key, should return the value
// true useStale can be used to retrieve the stale cache
type SyncCallback func(ctx context.Context, key any) (value any, useStale bool, err error)

// AsyncCallback given a key, should return the value
// This will be called in a goroutine, considering the AsyncSemaphore
type AsyncCallback func(ctx context.Context, key any) (value any, err error)

// Config configuration to construct LastCache
type Config struct {
	// Will be used to set expire time for all the keys
	// If set to negative or 0 the defaultTTL will be used
	GlobalTTL time.Duration

	// Will be used to extend the ttl if cache is stale and callback is failed
	// If set to 0 ttl will not be extended and evey call to LoadOrStore for stale cache will execute the callback
	// Until the callback can return new value with no error
	// In most cases this should be set to the same value as GlobalTTL,
	// Unless the GlobalTTL is too high, or the callback is expensive to be called
	ExtendTTL time.Duration

	// Number of background callbacks allowed in AsyncLoadOrStore
	// If set to 0 the default value defaultSemaphore will be used
	// If you want to use AsyncLoadOrStore this will limit the number of callback calls while cache is expired
	// If callback is too expensive to run, it's better to set to low value (e.g. 1)
	// If you are using different callback processes for different keys, you might want to optimize this value or use another instance of LastCache
	AsyncSemaphore int

	// Context to be used in lifetime of the Cache instance
	// Default is context.TODO()
	Context context.Context
}

// Entry cache entry
type Entry struct {
	// Value retrieved from callback
	Value any

	// Either the cache entry is stale or not
	Stale bool

	// Holds the underlying error if stale cache is used when using LoadOrStore
	// In case of using AsyncLoadOrStore this always will be nil and the underlying error will be returned in channel
	Err error
}

// Cache use New function to construct a new Cache
// Must not be copied after first use
type Cache struct {
	config      Config
	ctx         context.Context
	mapStorage  sync.Map
	timeStorage sync.Map
	semaphore   chan bool
}

// New returns new Cache, zero value Config can be passed to use default values
func New(config Config) *Cache {
	if config.GlobalTTL <= 0 {
		config.GlobalTTL = defaultTTL
	}

	c := Cache{
		config: config,
	}

	c.ctx = context.TODO()
	if config.Context != nil {
		c.ctx = config.Context
	}

	semaphore := defaultSemaphore
	if config.AsyncSemaphore > 0 {
		semaphore = config.AsyncSemaphore
	}
	c.semaphore = make(chan bool, semaphore)

	return &c
}

// Set sets the value and ttl for a key.
func (c *Cache) Set(key, value any) {
	c.mapStorage.Store(key, value)
	c.timeStorage.Store(key, now().Add(c.config.GlobalTTL))
}

// Delete deletes the value for a key.
func (c *Cache) Delete(key any) {
	c.mapStorage.Delete(key)
	c.timeStorage.Delete(key)
}

// Range calls f sequentially for each key and value and ttl present in the map.
// If f returns false, range stops the iteration.
//
// Range does not necessarily correspond to any consistent snapshot of the Map's
// contents: no key will be visited more than once, but if the value for any key
// is stored or deleted concurrently (including by f), Range may reflect any
// mapping for that key from any point during the Range call. Range does not
// block other methods on the receiver; even f itself may call any method on Cache.
//
// Range may be O(N) with the number of elements in the map even if f returns
// false after a constant number of calls.
func (c *Cache) Range(f func(key, value any, ttl time.Duration) bool) {
	c.mapStorage.Range(func(key, value any) bool {
		return f(key, value, c.TTL(key))
	})
}

// TTL returns ttl in duration format. The returned value can be negative as well, which in that case
// means item is already expired. Positive values are valid items in the cache.
func (c *Cache) TTL(key any) time.Duration {
	if v, ok := c.timeStorage.Load(key); ok {
		d, _ := v.(time.Time)
		return d.Sub(now())
	}
	return 0
}

// LoadOrStore loads the key from cache with respect to the ttl.
//
//		There will be three cases:
//
//		1. If key exists and is not expired, the value will be returned as Entry
//		2. If key doesn't exist, SyncCallback will be called to store the value.
//		   2.1 If SyncCallback returns error, the error will be returned
//		   2.2 If SyncCallback returns no error, the value will be stored and returned
//		3. If key is expired, SyncCallback will be called to replace the value,
//		   3.1 if SyncCallback returns no error, key will be updated with new value and returned
//	       3.2 if SyncCallback returns error with true useStale,
//				cached value will be added to the entry.Value,
//	   			SyncCallback error will be added to the entry.Err,
//				ttl will be extended,
//			   	entry and nil will be returned
//	       3.3 if SyncCallback returns error with false useStale,
//				error will be returned
func (c *Cache) LoadOrStore(key any, callback SyncCallback) (Entry, error) {
	return c.loadOrStore(c.context(), key, callback)
}

// LoadOrStoreWithCtx check LoadOrStore
func (c *Cache) LoadOrStoreWithCtx(ctx context.Context, key any, callback SyncCallback) (Entry, error) {
	return c.loadOrStore(ctx, key, callback)
}

// AsyncLoadOrStore loads the key from cache with respect to the ttl and runs the callback in background
//
//		There will be three cases:
//
//		1. If key exists and is not expired, the value will be returned as Entry
//		2. If key doesn't exist, callback will be called to store the value.
//		   2.1 If SyncCallback returns error, the error will be returned
//		   2.2 If SyncCallback returns no error, the value will be stored and returned
//		3. If key is expired, callback will be called in background to replace the value,
//		   and existing cache will be returned immediately
//		   a buffered error channel size 1 will be returned if cache is stale,
//	       nil or error will be sent to the error channel
func (c *Cache) AsyncLoadOrStore(key any, callback AsyncCallback) (Entry, chan error, error) {
	return c.asyncLoadOrStore(c.context(), key, callback)
}

// AsyncLoadOrStoreWithCtx check AsyncLoadOrStore
func (c *Cache) AsyncLoadOrStoreWithCtx(ctx context.Context, key any, callback AsyncCallback) (Entry, chan error, error) {
	return c.asyncLoadOrStore(ctx, key, callback)
}

func (c *Cache) asyncLoadOrStore(ctx context.Context, key any, callback AsyncCallback) (Entry, chan error, error) {
	var err error
	var entry Entry

	v, ok := c.timeStorage.Load(key)
	if !ok {
		var newValue any
		// first time miss
		newValue, err = callback(ctx, key)
		if err != nil {
			return entry, nil, err
		}

		// store cache
		c.Set(key, newValue)
		entry.Value = newValue
		return entry, nil, nil
	}

	d, _ := v.(time.Time)
	var ch chan error
	if now().After(d) { // expired
		ch = make(chan error, 1)
		go c.updateCache(ctx, key, callback, ch)
		entry.Stale = true
	}

	v, _ = c.mapStorage.Load(key)
	entry.Value = v
	return entry, ch, nil
}

func (c *Cache) loadOrStore(ctx context.Context, key any, callback SyncCallback) (Entry, error) {
	var newValue any
	var err error
	var entry Entry

	v, ok := c.timeStorage.Load(key)
	if !ok {
		// first time miss
		newValue, _, err = callback(ctx, key)
		if err != nil {
			return entry, err
		}

		// store cache
		c.Set(key, newValue)
		entry.Value = newValue
		return entry, nil
	}

	d, _ := v.(time.Time)
	if now().After(d) { // expired
		var useStale bool
		newValue, useStale, err = callback(ctx, key)
		if err == nil {
			// store cache and set new ttl
			c.Set(key, newValue)
			entry.Value = newValue
			return entry, nil
		}

		if !useStale {
			return entry, err
		}

		entry.Stale = true
		entry.Err = err
	}

	// extend stale cache ttl
	if entry.Stale && c.config.ExtendTTL > 0 {
		c.updateTTL(key, c.config.ExtendTTL)
	}

	v, _ = c.mapStorage.Load(key)
	entry.Value = v
	return entry, nil
}

func (c *Cache) checkIfExpired(key any) bool {
	v, ok := c.timeStorage.Load(key)
	if !ok {
		return true
	}

	d, _ := v.(time.Time)
	return now().After(d)
}

func (c *Cache) updateCache(ctx context.Context, key any, callback AsyncCallback, errChan chan error) {
	c.semaphore <- true
	var err error
	defer func() {
		<-c.semaphore
		errChan <- err
	}()

	// only execute callback if cache is expired
	if !c.checkIfExpired(key) {
		return
	}

	// extend stale cache ttl
	if c.config.ExtendTTL > 0 {
		c.updateTTL(key, c.config.ExtendTTL)
	}

	newValue, err := callback(ctx, key)
	if err == nil {
		// store cache and set new ttl
		c.Set(key, newValue)
	}
}

func (c *Cache) context() context.Context {
	return c.ctx
}

func (c *Cache) updateTTL(key any, ttl time.Duration) {
	c.timeStorage.Store(key, now().Add(ttl))
}
