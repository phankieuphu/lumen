// Package secret stores connection passwords outside the local database:
// in the OS keychain (macOS Keychain, Windows Credential Manager, Linux
// Secret Service), or in memory for tests.
package secret

import (
	"errors"
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound is returned when no secret exists for a key.
var ErrNotFound = errors.New("secret: not found")

// Store keeps secrets by key. Implementations are safe for concurrent use.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	// Delete removes the secret; deleting a missing key is not an error.
	Delete(key string) error
}

// DefaultService is the keychain service name entries are filed under.
const DefaultService = "lumen"

// Keyring stores secrets in the OS keychain.
type Keyring struct {
	// Service groups entries in the keychain. Defaults to DefaultService.
	Service string
}

func (k Keyring) service() string {
	if k.Service == "" {
		return DefaultService
	}
	return k.Service
}

// Get implements Store.
func (k Keyring) Get(key string) (string, error) {
	v, err := keyring.Get(k.service(), key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("secret: keychain get: %w", err)
	}
	return v, nil
}

// Set implements Store.
func (k Keyring) Set(key, value string) error {
	if err := keyring.Set(k.service(), key, value); err != nil {
		return fmt.Errorf("secret: keychain set: %w", err)
	}
	return nil
}

// Delete implements Store.
func (k Keyring) Delete(key string) error {
	err := keyring.Delete(k.service(), key)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("secret: keychain delete: %w", err)
	}
	return nil
}

// Memory is an in-process Store for tests. The zero value is ready to use.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

// Get implements Store.
func (m *Memory) Get(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set implements Store.
func (m *Memory) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[string]string{}
	}
	m.m[key] = value
	return nil
}

// Delete implements Store.
func (m *Memory) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, key)
	return nil
}
