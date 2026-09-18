package mysql

import (
	"testing"
	"time"
)

func TestOceanBaseOracleCapabilities(t *testing.T) {
	cfg := NewConfig()
	cfg.OceanBaseOracle = true
	conn := &mysqlConn{}
	conn.initCapabilities(^capabilityFlag(0), 0, cfg)

	if conn.capabilities&clientSupportOracle == 0 {
		t.Fatal("OceanBase Oracle capability is not enabled")
	}
	if conn.capabilities&clientMySQL != 0 {
		t.Fatal("generic MySQL client marker must be disabled for OceanBase Oracle")
	}
}

func TestParseOceanBaseOracleTimestamp(t *testing.T) {
	value, err := parseOceanBaseOracleTimestamp(
		[]byte{20, 26, 9, 18, 7, 21, 9, 0x00, 0xd0, 0x54, 0x08, 6}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.September, 18, 7, 21, 9, 139776000, time.UTC)
	if !value.Equal(want) {
		t.Fatalf("timestamp = %s, want %s", value, want)
	}
}
