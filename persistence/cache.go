package persistence

import (
	"fmt"
	"time"
)

var ErrCacheExpired = fmt.Errorf("cache entry expired")

// TradeCache defines the persistence contract for symbol and trade caching.
// Any backing store (bbolt, Redis, Postgres, in-memory) implements this interface,
type AccountConnectCache interface {
	PutSymbols(accountType string, symbols []byte) error
	GetSymbols(accountType string, ttl time.Duration) ([]byte, error)

	// Per-client per-symbol trade operations
	PutTrades(clientID string, symbol string, trades []byte) error
	GetTrades(prefix string, symbol string, ttl time.Duration) ([]byte, error)

	// Retrieve all cached trades for a client across all symbols
	GetAllTrades(clientID string, ttl time.Duration) (map[string][]byte, error)

	// Lifecycle
	RegisterBuckets() error
	Close() error
}
