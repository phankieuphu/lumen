package driver

import (
	"math"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	ts := time.Date(2026, 11, 2, 9, 30, 0, 123, time.UTC)
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, nil},
		{"small_int64_stays_numeric", int64(42), int64(42)},
		{"big_int64_becomes_string", int64(1 << 60), "1152921504606846976"},
		{"negative_big_int64_becomes_string", int64(-(1 << 60)), "-1152921504606846976"},
		{"big_uint64_becomes_string", uint64(math.MaxUint64), "18446744073709551615"},
		{"nan_becomes_string", math.NaN(), "NaN"},
		{"inf_becomes_string", math.Inf(1), "+Inf"},
		{"utf8_bytes_become_string", []byte("héllo"), "héllo"},
		{"binary_bytes_become_base64", []byte{0xff, 0x00}, `\x/wA=`},
		{"time_is_rfc3339", ts, "2026-11-02T09:30:00.000000123Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestConfigRedacted(t *testing.T) {
	c := Config{User: "u", Password: "secret"}
	if got := c.Redacted().Password; got != "***" {
		t.Fatalf("Redacted password = %q", got)
	}
	if c.Password != "secret" {
		t.Fatal("Redacted mutated the original")
	}
}

func TestConfigWithDefaults(t *testing.T) {
	c := Config{}.WithDefaults()
	if c.MaxConns != DefaultMaxConns || c.ConnectTimeout != DefaultConnectTimeout ||
		c.AppName != DefaultAppName || c.TLS != TLSPrefer {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}
