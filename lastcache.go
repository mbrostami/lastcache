package lastcache

import (
	"errors"
	"sync"
	"time"
)

const defaultTTL = 1 * time.Minute

var ErrRecordDoesntExist = errors.New("record doesnt exist")
var now = time.Now

type Config struct {
	// can not be negative or 0,
	GlobalTTL time.Duration
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

// LoadOrStore loads an item from cache with respect to the ttl. There will be three cases:
//
//  1. If item doesn't exist, callback will be called to store the cache version.
//     1.1 If callback returns error, the err will be passed to the higher level
//
//  2. If item is expired, callback will be called to replace the value,
//     2.1 if callback returns error, the last existing in memory cache will be used and ttl will be reset
//
//  3. If item is not expired, the value will be returned
//
// In theory ErrRecordDoesntExist shouldn't happen, unless there is inconsistency between ttl, and value storage
func (c *Cache) LoadOrStore(key any, callback func(key any) (any, error)) (any, error) {
	var newValue any
	var err error
	v, ok := c.timeStorage.Load(key)
	if !ok {
		// first time miss
		newValue, err = callback(key)
		if err != nil {
			return nil, err
		}

		// store cache
		c.Set(key, newValue)
		return newValue, nil
	}

	var updateTTL bool
	d, _ := v.(time.Time)
	if now().After(d) { // expired
		if newValue, err = callback(key); err == nil {
			// store cache and set new ttl
			c.Set(key, newValue)
			return newValue, nil
		}
		// if there is any error ignore expiration and load from cache
		updateTTL = true
	}

	if v, ok = c.mapStorage.Load(key); ok {
		if updateTTL {
			c.updateTTL(key, c.config.GlobalTTL)
		}
		return v, nil
	}

	// should never happen
	return nil, ErrRecordDoesntExist
}

// updateTTL updates the ttl for the item.
func (c *Cache) updateTTL(key any, ttl time.Duration) {
	c.timeStorage.Store(key, now().Add(ttl))
}
