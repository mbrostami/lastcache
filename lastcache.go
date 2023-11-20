package lastcache

import (
	"sync"
	"time"
)

const defaultTTL = 1 * time.Minute
const defaultSemaphore int = 1

var now = time.Now

// syncCallback given key, should return the value
// true useLastCache can be used to retrieve the latest available value from cache
// if it's not possible to get the value at the moment
type syncCallback func(key any) (value any, useLastCache bool, err error)
type asyncCallback func(key any) (value any, err error)

type Config struct {
	// will be used to set expire time for all the keys
	// if set to negative or 0 the defaultTTL will be used
	GlobalTTL time.Duration

	// will be used to extend the ttl if cache is stale and callback is failed
	// if set to 0 ttl will not be extended and evey call to LoadOrStore for stale cache will execute the callback
	// until the callback can return new value with no error
	// in most cases this should be set to the same value as GlobalTTL,
	// unless the GlobalTTL is too high, or the callback is expensive to be called
	ExtendTTL time.Duration

	// number of background callbacks allowed in AsyncLoadOrStore
	// if set to 0 the default value defaultSemaphore will be used
	// if you want to use AsyncLoadOrStore this will limit the number of callback calls while cache is expired
	// if callback is too expensive to run, it's better to set to low value (e.g. 1)
	// if you are using different callback processes for different keys, you might want to optimize this value
	AsyncSemaphore int
}

type Entry struct {
	Value any
	Stale bool

	// holds the underlying error if last available cache is used
	Err error
}

type Cache struct {
	config      Config
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
//		2. If key doesn't exist, syncCallback will be called to store the value.
//		   2.1 If syncCallback returns error, the error will be returned
//		   2.2 If syncCallback returns no error, the value will be stored and returned
//		3. If key is expired, syncCallback will be called to replace the value,
//		   3.1 if syncCallback returns no error, key will be updated with new value and returned
//	       3.2 if syncCallback returns error with true useLastCache,
//				cached value will be added to the entry.Value,
//	   			syncCallback error will be added to the entry.Err,
//				ttl will be extended,
//			   	entry and nil will be returned
//	       3.3 if syncCallback returns error with false useLastCache,
//				error will be returned
func (c *Cache) LoadOrStore(key any, callback syncCallback) (*Entry, error) {
	var newValue any
	var err error
	var entry Entry

	v, ok := c.timeStorage.Load(key)
	if !ok {
		// first time miss
		newValue, _, err = callback(key)
		if err != nil {
			return nil, err
		}

		// store cache
		c.Set(key, newValue)
		entry.Value = newValue
		return &entry, nil
	}

	d, _ := v.(time.Time)
	if now().After(d) { // expired
		var useLastCache bool
		newValue, useLastCache, err = callback(key)
		if err == nil {
			// store cache and set new ttl
			c.Set(key, newValue)
			entry.Value = newValue
			return &entry, nil
		}

		if !useLastCache {
			return nil, err
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
	return &entry, nil
}

// AsyncLoadOrStore loads the key from cache with respect to the ttl and runs the callback in background
//
//		There will be three cases:
//
//		1. If key exists and is not expired, the value will be returned as Entry
//		2. If key doesn't exist, callback will be called to store the value.
//		   2.1 If syncCallback returns error, the error will be returned
//		   2.2 If syncCallback returns no error, the value will be stored and returned
//		3. If key is expired, callback will be called in background to replace the value,
//		   and existing cache will be returned immediately
//		   a buffered error channel size 1 will be returned if cache is stale,
//	       nil or error will be sent to the error channel
func (c *Cache) AsyncLoadOrStore(key any, callback asyncCallback) (*Entry, chan error, error) {
	var err error
	var entry Entry

	v, ok := c.timeStorage.Load(key)
	if !ok {
		var newValue any
		// first time miss
		newValue, err = callback(key)
		if err != nil {
			return nil, nil, err
		}

		// store cache
		c.Set(key, newValue)
		entry.Value = newValue
		return &entry, nil, nil
	}

	d, _ := v.(time.Time)
	var ch chan error
	if now().After(d) { // expired
		ch = make(chan error, 1)
		go c.updateCache(key, callback, ch)
		entry.Stale = true
	}

	v, _ = c.mapStorage.Load(key)
	entry.Value = v
	return &entry, ch, nil
}

func (c *Cache) checkIfExpired(key any) bool {
	v, ok := c.timeStorage.Load(key)
	if !ok {
		return true
	}

	d, _ := v.(time.Time)
	return now().After(d)
}

func (c *Cache) updateCache(key any, callback asyncCallback, errChan chan error) {
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

	newValue, err := callback(key)
	if err == nil {
		// store cache and set new ttl
		c.Set(key, newValue)
	}
}

func (c *Cache) updateTTL(key any, ttl time.Duration) {
	c.timeStorage.Store(key, now().Add(ttl))
}
