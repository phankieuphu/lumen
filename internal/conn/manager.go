package conn

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/patrick-rontech/kiemdb/internal/driver"
	"github.com/patrick-rontech/kiemdb/internal/secret"
	"github.com/patrick-rontech/kiemdb/internal/store"
)

// Manager owns connection profiles and their pools. It is safe for
// concurrent use.
type Manager struct {
	repo    repo
	secrets secret.Store

	mu    sync.Mutex
	pools map[string]*pool
}

// pool is a cached open connection. ready is closed once conn/err are set,
// so concurrent Get calls for the same profile share one Open.
type pool struct {
	ready   chan struct{}
	conn    driver.Conn
	profile Profile
	err     error
}

// NewManager returns a Manager over the store and secret store.
func NewManager(s *store.Store, secrets secret.Store) *Manager {
	return &Manager{repo: repo{db: s.DB()}, secrets: secrets, pools: map[string]*pool{}}
}

// Create saves a new profile and its password and returns it with ID and
// timestamps set.
func (m *Manager) Create(ctx context.Context, p Profile, password string) (Profile, error) {
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	p.ID = newID()
	p.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	p.UpdatedAt = p.CreatedAt
	if password != "" {
		if err := m.secrets.Set(p.ID, password); err != nil {
			return Profile{}, fmt.Errorf("conn: save password: %w", err)
		}
	}
	if err := m.repo.insert(ctx, p); err != nil {
		_ = m.secrets.Delete(p.ID)
		return Profile{}, err
	}
	return p, nil
}

// Update saves changes to a profile. A nil password keeps the stored one; an
// empty string removes it. The open pool, if any, is closed so the next use
// picks up the new settings.
func (m *Manager) Update(ctx context.Context, p Profile, password *string) (Profile, error) {
	old, err := m.repo.get(ctx, p.ID)
	if err != nil {
		return Profile{}, err
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	p.CreatedAt = old.CreatedAt
	p.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	if err := m.repo.update(ctx, p); err != nil {
		return Profile{}, err
	}
	if password != nil {
		if *password == "" {
			err = m.secrets.Delete(p.ID)
		} else {
			err = m.secrets.Set(p.ID, *password)
		}
		if err != nil {
			return Profile{}, fmt.Errorf("conn: save password: %w", err)
		}
	}
	return p, m.Close(p.ID)
}

// Delete removes a profile, its password, and its pool.
func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := m.repo.delete(ctx, id); err != nil {
		return err
	}
	return errors.Join(m.Close(id), m.secrets.Delete(id))
}

// Profile returns a saved profile.
func (m *Manager) Profile(ctx context.Context, id string) (Profile, error) {
	return m.repo.get(ctx, id)
}

// List returns all profiles sorted by name.
func (m *Manager) List(ctx context.Context) ([]Profile, error) {
	return m.repo.list(ctx)
}

// Get returns the open pool for a profile, opening it on first use.
// Concurrent callers for the same profile wait for a single Open.
func (m *Manager) Get(ctx context.Context, id string) (driver.Conn, Profile, error) {
	m.mu.Lock()
	if p, ok := m.pools[id]; ok {
		m.mu.Unlock()
		select {
		case <-p.ready:
			if p.err != nil {
				return nil, Profile{}, p.err
			}
			return p.conn, p.profile, nil
		case <-ctx.Done():
			return nil, Profile{}, fmt.Errorf("conn: open: %w", ctx.Err())
		}
	}
	p := &pool{ready: make(chan struct{})}
	m.pools[id] = p
	m.mu.Unlock()

	p.conn, p.profile, p.err = m.open(ctx, id)
	if p.err != nil {
		// Forget the failure so the next call retries.
		m.mu.Lock()
		if m.pools[id] == p {
			delete(m.pools, id)
		}
		m.mu.Unlock()
	}
	close(p.ready)
	if p.err != nil {
		return nil, Profile{}, p.err
	}
	return p.conn, p.profile, nil
}

func (m *Manager) open(ctx context.Context, id string) (driver.Conn, Profile, error) {
	prof, err := m.repo.get(ctx, id)
	if err != nil {
		return nil, Profile{}, err
	}
	pw, err := m.password(id)
	if err != nil {
		return nil, Profile{}, err
	}
	c, err := driver.Open(ctx, prof.Dialect, prof.Config(pw))
	if err != nil {
		return nil, Profile{}, fmt.Errorf("conn: open %q: %w", prof.Name, err)
	}
	return c, prof, nil
}

func (m *Manager) password(id string) (string, error) {
	pw, err := m.secrets.Get(id)
	if errors.Is(err, secret.ErrNotFound) {
		return "", nil // passwordless auth (trust, peer, SQLite)
	}
	if err != nil {
		return "", fmt.Errorf("conn: read password: %w", err)
	}
	return pw, nil
}

// Test opens and pings p without saving it.
func (m *Manager) Test(ctx context.Context, p Profile, password string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	c, err := driver.Open(ctx, p.Dialect, p.Config(password))
	if err != nil {
		return err
	}
	return c.Close()
}

// Close closes the pool for a profile if one is open. It waits for an
// in-flight open to finish first.
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	p, ok := m.pools[id]
	delete(m.pools, id)
	m.mu.Unlock()
	if !ok {
		return nil
	}
	<-p.ready
	if p.conn == nil {
		return nil
	}
	return p.conn.Close()
}

// CloseAll closes every pool. Call it on shutdown.
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.pools))
	for id := range m.pools {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	var errs []error
	for _, id := range ids {
		errs = append(errs, m.Close(id))
	}
	return errors.Join(errs...)
}

// Stats returns pool stats for every open pool, keyed by profile ID.
func (m *Manager) Stats() map[string]driver.PoolStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]driver.PoolStats, len(m.pools))
	for id, p := range m.pools {
		select {
		case <-p.ready:
			if p.conn != nil {
				out[id] = p.conn.Stats()
			}
		default:
		}
	}
	return out
}
