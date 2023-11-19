package lastcache

import (
	"sync"
	"time"
)

const defaultTTL = 1 * time.Minute

var now = time.Now

// callback given key, should return the value
// true useLastCache can be used to retrieve the latest available value from cache
// if it's not possible to get the value at the moment
type callback func(key any) (value any, useLastCache bool, err error)

type Config struct {
	// can not be negative or 0,
	GlobalTTL time.Duration
}

type Entry struct {
	Value   any
	Expired bool

	// holds the underlying error if last available cache is used
	Err error
}

type Cache struct {
	config      Config
	mapStorage  sync.Map
	timeStorage sync.Map
}

// New pass empty config to use default ttl for all the keys
func New(config Config) *Cache {
	if config.GlobalTTL <= 0 {
		config.GlobalTTL = defaultTTL
	}
	return &Cache{
		config: config,
	}
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

// LoadOrStore loads a key from cache with respect to the ttl.
//
//		There will be three cases:
//
//		1. If key exists and is not expired, the value will be returned as Entry
//		2. If key doesn't exist, callback will be called to store the value.
//		   2.1 If callback returns error, the error will be returned
//		   2.2 If callback returns no error, the value will be stored and returned
//		3. If key is expired, callback will be called to replace the value,
//		   3.1 if callback returns no error, key will be updated with new value and returned
//	       3.2 if callback returns error with true useLastCache,
//				cached value will be added to the entry.Value,
//	   			callback error will be added to the entry.Err,
//				ttl will be extended,
//			   	entry and nil will be returned
//	       3.3 if callback returns error with false useLastCache,
//				error will be returned
func (c *Cache) LoadOrStore(key any, callback callback) (*Entry, error) {
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
		entry.Expired = true
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

		entry.Expired = true
		entry.Err = err
	}

	v, _ = c.mapStorage.Load(key)
	if entry.Expired {
		c.updateTTL(key, c.config.GlobalTTL)
	}
	entry.Value = v
	return &entry, nil
}

// updateTTL updates the ttl for the item.
func (c *Cache) updateTTL(key any, ttl time.Duration) {
	c.timeStorage.Store(key, now().Add(ttl))
}
