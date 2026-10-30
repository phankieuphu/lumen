package postgres

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/phankieuphu/lumen/internal/driver"
)

func TestPoolConfig(t *testing.T) {
	cfg := driver.Config{
		Host: "db.local", User: "app", Password: "p@ss/w?rd&sslmode=disable",
		Database: "shop", TLS: driver.TLSRequire, ReadOnly: true, MaxConns: 7,
	}.WithDefaults()
	pc, err := poolConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cc := pc.ConnConfig
	if cc.Host != "db.local" || cc.Port != DefaultPort || cc.Database != "shop" {
		t.Fatalf("target = %s:%d/%s", cc.Host, cc.Port, cc.Database)
	}
	if cc.Password != cfg.Password {
		t.Fatal("password with URL metacharacters was not preserved")
	}
	if cc.TLSConfig == nil {
		t.Fatal("password injected sslmode=disable; TLS was turned off")
	}
	if cc.RuntimeParams["default_transaction_read_only"] != "on" {
		t.Fatal("read-only not set on session")
	}
	if pc.MaxConns != 7 {
		t.Fatalf("MaxConns = %d", pc.MaxConns)
	}
}

func TestPoolConfigErrorHidesSecrets(t *testing.T) {
	_, err := poolConfig(driver.Config{Host: "h", Password: "hunter2", TLS: "bogus"}.WithDefaults())
	if err == nil {
		t.Fatal("expected error for bad sslmode")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error leaks password: %v", err)
	}
}

func TestNormalize(t *testing.T) {
	u := [16]byte{0x12, 0x3e, 0x45, 0x67, 0xe8, 0x9b, 0x12, 0xd3, 0xa4, 0x56, 0x42, 0x66, 0x14, 0x17, 0x40, 0x00}
	if got := normalize(pgtype.UUIDOID, u); got != "123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("uuid = %v", got)
	}
	var n pgtype.Numeric
	if err := n.Scan("12345678901234567890.123"); err != nil {
		t.Fatal(err)
	}
	if got := normalize(pgtype.NumericOID, n); got != "12345678901234567890.123" {
		t.Fatalf("numeric = %#v", got)
	}
}
