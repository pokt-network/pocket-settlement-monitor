package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSupplierAddresses_MonitorAll(t *testing.T) {
	addrs, err := LoadSupplierAddresses(SuppliersConfig{})
	require.NoError(t, err)
	assert.Nil(t, addrs)
}

func TestLoadSupplierAddresses_ExplicitAddresses(t *testing.T) {
	cfg := SuppliersConfig{
		Addresses: []string{"pokt1abc", "pokt1def"},
	}
	addrs, err := LoadSupplierAddresses(cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"pokt1abc", "pokt1def"}, addrs)
}

func TestLoadSupplierAddresses_Deduplication(t *testing.T) {
	cfg := SuppliersConfig{
		Addresses: []string{"pokt1abc", "pokt1def", "pokt1abc"},
	}
	addrs, err := LoadSupplierAddresses(cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"pokt1abc", "pokt1def"}, addrs)
}

func TestLoadSupplierAddresses_TrimWhitespace(t *testing.T) {
	cfg := SuppliersConfig{
		Addresses: []string{" pokt1abc ", "  pokt1def  "},
	}
	addrs, err := LoadSupplierAddresses(cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"pokt1abc", "pokt1def"}, addrs)
}

func TestLoadSupplierAddresses_EmptyStringsFiltered(t *testing.T) {
	cfg := SuppliersConfig{
		Addresses: []string{"pokt1abc", "", "  ", "pokt1def"},
	}
	addrs, err := LoadSupplierAddresses(cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"pokt1abc", "pokt1def"}, addrs)
}

func TestLoadSupplierAddresses_FromKeysFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.yaml")
	// Valid 32-byte hex key (64 hex chars).
	err := os.WriteFile(path, []byte(`keys:
  - "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
`), 0644)
	require.NoError(t, err)

	cfg := SuppliersConfig{KeysFile: path}
	addrs, err := LoadSupplierAddresses(cfg)
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	assert.Contains(t, addrs[0], "pokt1")
}

func TestLoadSupplierAddresses_KeysFileNotFound(t *testing.T) {
	cfg := SuppliersConfig{KeysFile: "/nonexistent/keys.yaml"}
	_, err := LoadSupplierAddresses(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "loading supplier keys file")
}

func TestDeriveAddress_ValidKey(t *testing.T) {
	hexKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	addr, err := deriveAddress(hexKey)
	require.NoError(t, err)
	assert.Contains(t, addr, "pokt1")
}

func TestDeriveAddress_With0xPrefix(t *testing.T) {
	hexKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	addrNoPrefix, err := deriveAddress(hexKey)
	require.NoError(t, err)

	addrWithPrefix, err := deriveAddress("0x" + hexKey)
	require.NoError(t, err)

	assert.Equal(t, addrNoPrefix, addrWithPrefix)
}

func TestDeriveAddress_InvalidHex(t *testing.T) {
	_, err := deriveAddress("ZZZZ")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid hex")
}

func TestDeriveAddress_WrongKeyLength(t *testing.T) {
	// 16 bytes = 32 hex chars (too short).
	_, err := deriveAddress("0123456789abcdef0123456789abcdef")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected 32 bytes")
}
