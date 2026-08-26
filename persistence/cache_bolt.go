package persistence

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	bucketBinanceSymbols = "binance_symbols"
	bucketBinanceTrades  = "binance_trades"
)

// BboltAccountConnectCache implements TradeCache using bbolt as the backing store.
type BboltAccountConnectCache struct {
	db *bolt.DB
}

func NewBboltTradeCache(db *bolt.DB) *BboltAccountConnectCache {
	return &BboltAccountConnectCache{db: db}
}

func (c *BboltAccountConnectCache) RegisterBuckets() error {
	return c.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketBinanceSymbols)); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists([]byte(bucketBinanceTrades))
		return err
	})
}

func (c *BboltAccountConnectCache) Close() error {
	return c.db.Close()
}

func (c *BboltAccountConnectCache) PutSymbols(accountType string, symbols []byte) error {
	return c.putWithTimestamp(bucketBinanceSymbols, accountType, symbols)
}

func (c *BboltAccountConnectCache) GetSymbols(accountType string, ttl time.Duration) ([]byte, error) {
	return c.getWithTTL(bucketBinanceSymbols, accountType, ttl)
}

func (c *BboltAccountConnectCache) PutTrades(clientID string, symbol string, trades []byte) error {
	key := clientID + ":" + symbol
	return c.putWithTimestamp(bucketBinanceTrades, key, trades)
}

func (c *BboltAccountConnectCache) GetTrades(clientID string, symbol string, ttl time.Duration) ([]byte, error) {
	key := clientID + ":" + symbol
	return c.getWithTTL(bucketBinanceTrades, key, ttl)
}

func (c *BboltAccountConnectCache) GetAllTrades(clientID string, ttl time.Duration) (map[string][]byte, error) {
	prefix := clientID + ":"
	results := make(map[string][]byte)
	log.Printf("Getting trades for key: %v", prefix)

	err := c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketBinanceTrades))
		if b == nil {
			return fmt.Errorf("bucket %s not found", bucketBinanceTrades)
		}
		cursor := b.Cursor()
		prefixB := []byte(prefix)
		for k, v := cursor.Seek(prefixB); k != nil && len(k) >= len(prefixB) && string(k[:len(prefixB)]) == prefix; k, v = cursor.Next() {
			var entry cachedEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				continue
			}
			if time.Since(entry.StoredAt) > ttl {
				continue
			}
			// strip the clientID: prefix so the key is just the symbol name
			symbol := string(k[len(prefixB):])
			data := make([]byte, len(entry.Data))
			copy(data, entry.Data)
			results[symbol] = data
		}
		return nil
	})
	return results, err
}

type cachedEntry struct {
	Data     []byte    `json:"data"`
	StoredAt time.Time `json:"stored_at"`
}

func (c *BboltAccountConnectCache) putWithTimestamp(bucket, key string, data []byte) error {
	entry := cachedEntry{
		Data:     data,
		StoredAt: time.Now().UTC(),
	}
	entryB, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal cache entry: %w", err)
	}
	return c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s not found", bucket)
		}
		return b.Put([]byte(key), entryB)
	})
}

func (c *BboltAccountConnectCache) getWithTTL(bucket, key string, ttl time.Duration) ([]byte, error) {
	var data []byte
	err := c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s not found", bucket)
		}
		v := b.Get([]byte(key))
		if v == nil {
			return nil // cache miss — not an error
		}
		var entry cachedEntry
		if err := json.Unmarshal(v, &entry); err != nil {
			return fmt.Errorf("failed to unmarshal cache entry: %w", err)
		}
		if time.Since(entry.StoredAt) > ttl {
			return ErrCacheExpired
		}
		data = make([]byte, len(entry.Data))
		copy(data, entry.Data)
		return nil
	})
	return data, err
}
