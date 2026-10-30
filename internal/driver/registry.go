package driver

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

var (
	regMu    sync.RWMutex
	registry = map[Dialect]Driver{}
)

// Register makes a driver available by its dialect. It is meant to be called
// from an engine package's init and panics on a duplicate, which is a
// programming error caught at startup.
func Register(d Driver) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[d.Dialect()]; dup {
		panic(fmt.Sprintf("driver: Register called twice for %q", d.Dialect()))
	}
	registry[d.Dialect()] = d
}

// Lookup returns the driver for a dialect.
func Lookup(d Dialect) (Driver, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	drv, ok := registry[d]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDialect, d)
	}
	return drv, nil
}

// Dialects lists registered dialects in sorted order.
func Dialects() []Dialect {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Dialect, 0, len(registry))
	for d := range registry {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Open looks up the driver for dialect and opens cfg with defaults applied.
func Open(ctx context.Context, dialect Dialect, cfg Config) (Conn, error) {
	drv, err := Lookup(dialect)
	if err != nil {
		return nil, err
	}
	return drv.Open(ctx, cfg.WithDefaults())
}
