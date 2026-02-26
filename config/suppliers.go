package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cosmostypes "github.com/cosmos/cosmos-sdk/types"
	"gopkg.in/yaml.v3"
)

// supplierKeysFile is the structure of a supplier-keys.yaml file.
type supplierKeysFile struct {
	Keys []string `yaml:"keys"`
}

// LoadSupplierAddresses resolves supplier addresses from the config.
// Returns nil (monitor-all mode) if no suppliers are configured.
func LoadSupplierAddresses(cfg SuppliersConfig) ([]string, error) {
	var addresses []string

	// Load from keys file if specified.
	if cfg.KeysFile != "" {
		derived, err := addressesFromKeysFile(cfg.KeysFile)
		if err != nil {
			return nil, fmt.Errorf("loading supplier keys file: %w", err)
		}
		addresses = append(addresses, derived...)
	}

	// Append explicit addresses.
	for _, addr := range cfg.Addresses {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			addresses = append(addresses, addr)
		}
	}

	// Deduplicate.
	if len(addresses) > 0 {
		seen := make(map[string]bool, len(addresses))
		deduped := make([]string, 0, len(addresses))
		for _, a := range addresses {
			if !seen[a] {
				seen[a] = true
				deduped = append(deduped, a)
			}
		}
		return deduped, nil
	}

	return nil, nil // monitor-all mode
}

// addressesFromKeysFile reads hex private keys and derives bech32 addresses.
func addressesFromKeysFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading keys file %s: %w", path, err)
	}

	var kf supplierKeysFile
	if err := yaml.Unmarshal(data, &kf); err != nil {
		return nil, fmt.Errorf("parsing keys file: %w", err)
	}

	if len(kf.Keys) == 0 {
		return nil, fmt.Errorf("keys file %s has no keys", path)
	}

	addresses := make([]string, 0, len(kf.Keys))
	for i, hexKey := range kf.Keys {
		addr, err := deriveAddress(hexKey)
		if err != nil {
			return nil, fmt.Errorf("key[%d]: %w", i, err)
		}
		addresses = append(addresses, addr)
	}

	return addresses, nil
}

// deriveAddress derives a pokt1... bech32 address from a hex private key.
func deriveAddress(hexKey string) (string, error) {
	hexKey = strings.TrimPrefix(strings.TrimSpace(hexKey), "0x")

	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		return "", fmt.Errorf("invalid hex: %w", err)
	}

	if len(keyBytes) != 32 {
		return "", fmt.Errorf("expected 32 bytes, got %d", len(keyBytes))
	}

	privKey := &secp256k1.PrivKey{Key: keyBytes}
	pubKey := privKey.PubKey()
	addr := cosmostypes.AccAddress(pubKey.Address())

	bech32Addr, err := cosmostypes.Bech32ifyAddressBytes("pokt", addr)
	if err != nil {
		return "", fmt.Errorf("encoding address: %w", err)
	}

	return bech32Addr, nil
}
