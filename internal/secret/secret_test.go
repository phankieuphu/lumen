package secret

import (
	"errors"
	"os"
	"testing"
)

func testStore(t *testing.T, s Store) {
	t.Helper()
	const key = "kiemdb-test-key"
	t.Cleanup(func() { _ = s.Delete(key) })

	if _, err := s.Get(key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: err = %v, want ErrNotFound", err)
	}
	if err := s.Set(key, "p@ss"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get(key); err != nil || v != "p@ss" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(key); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestMemory(t *testing.T) { testStore(t, &Memory{}) }

// TestKeyring touches the real OS keychain, so it only runs on request.
func TestKeyring(t *testing.T) {
	if os.Getenv("KIEMDB_TEST_KEYCHAIN") == "" {
		t.Skip("set KIEMDB_TEST_KEYCHAIN=1 to test the OS keychain")
	}
	testStore(t, Keyring{Service: "kiemdb-test"})
}
