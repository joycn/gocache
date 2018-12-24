package ttlcache

import (
	"runtime"
	"sync"
	"time"
)

// Item cache value with ttl
type Item struct {
	Persistent bool
	Object     interface{}
	Expiration time.Time
}

// Expired Returns true if the item has expired.
func (item Item) Expired() bool {
	if item.Persistent {
		return false
	}
	return time.Now().After(item.Expiration)
}

const (
	// NoExpiration For use with functions that take an expiration time.
	NoExpiration time.Duration = -1
	// DefaultExpiration For use with functions that take an expiration time. Equivalent to
	// passing in the same expiration duration as was given to New() or
	// NewFrom() when the cache was created (e.g. 5 minutes.)
	DefaultExpiration time.Duration = 0
)

// Cache kv map with ttl
type Cache struct {
	defaultExpiration time.Duration
	*sync.Map
	stop chan bool
	// If this is confusing, see the comment at the bottom of New()
}

// Store Cache Add an item to the cache, replacing any existing item. If the duration is 0
// (DefaultExpiration), the cache's default expiration time is used. If it is -1
// (NoExpiration), the item never expires.
func (c *Cache) Store(k string, x interface{}, d time.Duration, persitent bool) {
	// "Inlining" of set
	var e time.Time
	if d == DefaultExpiration {
		d = c.defaultExpiration
	}
	if d > 0 {
		e = time.Now().Add(d)
	}

	c.Map.Store(k, &Item{
		Persistent: persitent,
		Object:     x,
		Expiration: e,
	})
	// TODO: Calls to mu.Unlock are currently not deferred because defer
	// adds ~200 ns (as of go1.)
}

// Load an item from the cache. Returns the item or nil, and a bool indicating
// whether the key was found.
func (c *Cache) Load(k string) (interface{}, bool) {
	// "Inlining" of get and Expired
	v, found := c.Map.Load(k)
	if !found {
		return nil, false
	}

	item := v.(*Item)

	if !item.Persistent {
		if time.Now().After(item.Expiration) {
			c.Map.Delete(k)
			return nil, false
		}
	}
	return item.Object, true
}

// Delete an item from the cache. Does nothing if the key is not in the cache.
func (c *Cache) Delete(k string) {
	c.Map.Delete(k)
}

// Range calls f sequentially for each key and value present in the map. If f returns false, range stops the iteration.
func (c *Cache) Range(f func(key, value interface{}) bool) {
	c.Map.Range(f)
}

// DeleteExpired iterate the map and deleted expired item
func (c *Cache) DeleteExpired() {
	c.Map.Range(func(k, v interface{}) bool {
		item := v.(*Item)
		if item.Expired() {
			c.Delete(k.(string))
		}
		return true
	})
}

func stopGC(c *Cache) {
	c.stop <- true
}

func (c *Cache) gc(ci time.Duration) {
	ticker := time.NewTicker(ci)
	for {
		select {
		case <-ticker.C:
			c.DeleteExpired()
		case <-c.stop:
			ticker.Stop()
			return
		}
	}
}

func newCache(de time.Duration, m *sync.Map) *Cache {
	if de == 0 {
		de = -1
	}
	c := &Cache{
		defaultExpiration: de,
		Map:               m,
	}
	return c
}

// New return a new cache with a given default expiration duration and cleanup
// interval. If the expiration duration is less than one (or NoExpiration),
// the items in the cache never expire (by default), and must be deleted
// manually. If the cleanup interval is less than one, expired items are not
// deleted from the cache before calling c.DeleteExpired().
func New(defaultExpiration, cleanupInterval time.Duration) *Cache {
	items := &sync.Map{}
	c := newCache(defaultExpiration, items)
	// This trick ensures that the janitor goroutine (which--granted it
	// was enabled--is running DeleteExpired on c forever) does not keep
	// the returned C object from being garbage collected. When it is
	// garbage collected, the finalizer stops the janitor goroutine, after
	// which c can be collected.
	if defaultExpiration > 0 {
		go c.gc(cleanupInterval)
		runtime.SetFinalizer(c, stopGC)
	}
	return c
}
