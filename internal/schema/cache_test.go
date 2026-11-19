package schema

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// countingLoader returns a schema with one table per load, named after the
// load number, so a test can tell which load it got.
type countingLoader struct {
	loads atomic.Int64
	err   error
	gate  chan struct{} // when set, each load waits for a receive
}

func (l *countingLoader) load(context.Context, string) (Schema, error) {
	n := l.loads.Add(1)
	if l.gate != nil {
		<-l.gate
	}
	if l.err != nil {
		return Schema{}, l.err
	}
	return Schema{Tables: []Table{{Name: fmt.Sprintf("load%d", n)}}}, nil
}

func TestCacheGetInvalidate(t *testing.T) {
	ctx := t.Context()
	l := &countingLoader{}
	c := NewCache(l.load, 0)

	s1, err := c.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := c.Get(ctx, "a")
	if l.loads.Load() != 1 || s2.Version != s1.Version {
		t.Fatalf("second Get loaded again: %d loads, versions %d/%d", l.loads.Load(), s1.Version, s2.Version)
	}

	c.Invalidate("a")
	s3, _ := c.Get(ctx, "a")
	if l.loads.Load() != 2 || s3.Version <= s1.Version || s3.Tables[0].Name != "load2" {
		t.Fatalf("after Invalidate: %d loads, version %d (was %d), table %q", l.loads.Load(), s3.Version, s1.Version, s3.Tables[0].Name)
	}

	// Versions are unique across connections.
	b, _ := c.Get(ctx, "b")
	if b.Version <= s3.Version {
		t.Fatalf("version %d for b not after %d", b.Version, s3.Version)
	}

	c.Forget("a")
	if _, _ = c.Get(ctx, "a"); l.loads.Load() != 4 {
		t.Fatalf("Get after Forget did not load: %d loads", l.loads.Load())
	}
}

func TestCacheSharesOneLoad(t *testing.T) {
	l := &countingLoader{gate: make(chan struct{})}
	c := NewCache(l.load, 0)
	const n = 20
	var wg sync.WaitGroup
	versions := make([]uint64, n)
	for i := range n {
		wg.Go(func() {
			s, err := c.Get(t.Context(), "a")
			if err != nil {
				t.Error(err)
			}
			versions[i] = s.Version
		})
	}
	// Let the one load finish once the others had time to queue on it.
	for l.loads.Load() == 0 {
		runtimeYield()
	}
	close(l.gate)
	wg.Wait()
	if got := l.loads.Load(); got != 1 {
		t.Fatalf("%d concurrent Gets made %d loads, want 1", n, got)
	}
	for _, v := range versions {
		if v != versions[0] {
			t.Fatalf("Gets saw different versions: %v", versions)
		}
	}
}

func TestCacheDDLDuringLoadStaysStale(t *testing.T) {
	l := &countingLoader{gate: make(chan struct{}, 1)}
	c := NewCache(l.load, 0)
	done := make(chan Schema)
	go func() {
		s, _ := c.Get(t.Context(), "a")
		done <- s
	}()
	for l.loads.Load() == 0 {
		runtimeYield()
	}
	c.Invalidate("a") // DDL lands while the catalog is being read
	l.gate <- struct{}{}
	<-done
	l.gate <- struct{}{}
	s, _ := c.Get(t.Context(), "a")
	if l.loads.Load() != 2 || s.Tables[0].Name != "load2" {
		t.Fatalf("a load that raced DDL was cached: %d loads, table %q", l.loads.Load(), s.Tables[0].Name)
	}
}

func TestCacheErrorsAreNotCached(t *testing.T) {
	l := &countingLoader{err: errors.New("boom")}
	c := NewCache(l.load, 0)
	if _, err := c.Get(t.Context(), "a"); err == nil {
		t.Fatal("no error from a failing load")
	}
	l.err = nil
	if _, err := c.Get(t.Context(), "a"); err != nil || l.loads.Load() != 2 {
		t.Fatalf("failed load was cached: %v, %d loads", err, l.loads.Load())
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	ctx := t.Context()
	l := &countingLoader{}
	c := NewCache(l.load, 2)
	_, _ = c.Get(ctx, "a")
	_, _ = c.Get(ctx, "b")
	_, _ = c.Get(ctx, "a") // a is now the most recent
	_, _ = c.Get(ctx, "c") // evicts b
	if len(c.entries) != 2 || c.entries["b"] != nil || c.entries["a"] == nil {
		t.Fatalf("entries after eviction: %v", keys(c.entries))
	}
}

func keys(m map[string]*entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func runtimeYield() { runtime.Gosched() }
