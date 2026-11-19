package schema

import (
	"context"
	"fmt"
	"sync"
)

// DefaultMaxCached bounds a Cache; the least recently used entry goes first.
const DefaultMaxCached = 32

// Loader reads the schema of one connection.
type Loader func(ctx context.Context, connID string) (Schema, error)

// Cache keeps each connection's schema so the editor's autocomplete and the
// diagram do not read the catalog on every request. A schema is reloaded
// after Invalidate, which the query path calls after DDL. It is safe for
// concurrent use and starts no goroutines.
type Cache struct {
	load Loader
	max  int

	mu      sync.Mutex
	entries map[string]*entry
	seq     uint64 // last Version handed out, across all connections
	clock   uint64 // last use, for eviction
}

type entry struct {
	s      Schema
	ok     bool // s holds a load
	stale  bool // Invalidate was called since s was loaded
	gen    uint64
	used   uint64
	flight *flight // the load in progress, if any
}

// flight is one load that concurrent Gets share.
type flight struct {
	done chan struct{}
	s    Schema
	err  error
}

// NewCache returns a Cache that loads with load and holds at most max
// connections (DefaultMaxCached if max <= 0).
func NewCache(load Loader, max int) *Cache {
	if max <= 0 {
		max = DefaultMaxCached
	}
	return &Cache{load: load, max: max, entries: map[string]*entry{}}
}

// Get returns the schema of connID, loading it when it is missing or stale.
// Concurrent Gets share one load and its result; the load runs with the
// first caller's context.
func (c *Cache) Get(ctx context.Context, connID string) (Schema, error) {
	c.mu.Lock()
	e := c.entry(connID)
	if e.ok && !e.stale {
		s := e.s
		c.mu.Unlock()
		return s, nil
	}
	if f := e.flight; f != nil {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.s, f.err
		case <-ctx.Done():
			return Schema{}, fmt.Errorf("schema: wait for load: %w", ctx.Err())
		}
	}
	f := &flight{done: make(chan struct{})}
	e.flight = f
	gen := e.gen
	c.mu.Unlock()

	s, err := c.load(ctx, connID)

	c.mu.Lock()
	if err == nil {
		c.seq++
		s.Version = c.seq
		// DDL during the load may be missing from s: keep it stale.
		if e.gen == gen {
			e.s, e.ok, e.stale = s, true, false
		}
	}
	e.flight = nil
	f.s, f.err = s, err
	close(f.done)
	c.mu.Unlock()
	return s, err
}

// Invalidate marks connID's schema stale; the next Get reloads it.
func (c *Cache) Invalidate(connID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[connID]; e != nil {
		e.stale = true
		e.gen++
	}
}

// Forget drops connID, e.g. when the connection is edited or deleted.
func (c *Cache) Forget(connID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, connID)
}

// entry returns connID's entry, creating it and evicting the least recently
// used idle entry when full. c.mu must be held.
func (c *Cache) entry(connID string) *entry {
	c.clock++
	if e := c.entries[connID]; e != nil {
		e.used = c.clock
		return e
	}
	if len(c.entries) >= c.max {
		var oldest string
		for id, e := range c.entries {
			if e.flight == nil && (oldest == "" || e.used < c.entries[oldest].used) {
				oldest = id
			}
		}
		delete(c.entries, oldest)
	}
	e := &entry{used: c.clock}
	c.entries[connID] = e
	return e
}
