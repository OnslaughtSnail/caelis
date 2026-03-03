package imageutil

import (
	"container/list"
	"crypto/sha1"
	"fmt"
	"sync"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

// Cache is an LRU cache for processed image ContentParts.
// The cache key is the SHA-1 hex digest of the raw image bytes.
type Cache struct {
	mu    sync.Mutex
	cap   int
	items map[string]*list.Element
	order *list.List // front = most recently used
}

type cacheItem struct {
	key  string
	part model.ContentPart
}

// NewCache creates a new image cache with the given capacity.
func NewCache(capacity int) *Cache {
	if capacity <= 0 {
		capacity = 32
	}
	return &Cache{
		cap:   capacity,
		items: make(map[string]*list.Element, capacity),
		order: list.New(),
	}
}

// Key computes the cache key for raw image bytes.
func Key(data []byte) string {
	h := sha1.Sum(data)
	return fmt.Sprintf("%x", h)
}

// Get returns a cached ContentPart for the given key, if present.
// Moves the entry to the front of the LRU list.
func (c *Cache) Get(key string) (model.ContentPart, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return model.ContentPart{}, false
	}
	c.order.MoveToFront(elem)
	return elem.Value.(*cacheItem).part, true
}

// Put inserts a ContentPart into the cache, evicting the oldest entry if full.
func (c *Cache) Put(key string, part model.ContentPart) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		c.order.MoveToFront(elem)
		elem.Value.(*cacheItem).part = part
		return
	}
	if c.order.Len() >= c.cap {
		oldest := c.order.Back()
		if oldest != nil {
			c.order.Remove(oldest)
			delete(c.items, oldest.Value.(*cacheItem).key)
		}
	}
	item := &cacheItem{key: key, part: part}
	elem := c.order.PushFront(item)
	c.items[key] = elem
}

// Len returns the number of entries in the cache.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
