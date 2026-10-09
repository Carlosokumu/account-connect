package assetmeta

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

type Cache interface {
	GetAllAssetMetadata() (map[string][]byte, error)
	PutAssetMetadata(symbol string, data []byte) error
	PutAssetMetadataBulk(entries map[string][]byte) error
}

// MemoryProvider holds all asset metadata in memory
type MemoryProvider struct {
	mu     sync.RWMutex
	assets map[string]AssetInfo // ticker symbol -> info

	cache Cache
}

// NewMemoryProvider loads asset metadata
func NewMemoryProvider(ctx context.Context, cache Cache) (*MemoryProvider, error) {
	p := &MemoryProvider{
		assets: make(map[string]AssetInfo),
		cache:  cache,
	}

	fresh, err := fetchFromCoinGecko(ctx)
	if err != nil {
		log.Printf("assetmeta: coingecko fetch failed at startup, falling back to cache: %v", err)
		if loadErr := p.loadFromCache(); loadErr != nil {
			return nil, fmt.Errorf("both coingecko fetch and cache load failed: coingecko=%v cache=%w", err, loadErr)
		}
		log.Printf("assetmeta: loaded %d assets from cache fallback", len(p.assets))
		return p, nil
	}
	log.Printf("assetmeta: coingecko fetch complete, %d coins", len(fresh))

	p.mu.Lock()
	p.assets = fresh
	p.mu.Unlock()

	p.persistToCache(fresh)
	log.Printf("assetmeta: loaded %d assets from coingecko", len(fresh))
	return p, nil
}

func (p *MemoryProvider) GetAssetInfo(symbol string) (AssetInfo, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	info, ok := p.assets[symbol]
	return info, ok
}

// Refresh replaces the in-memory asset data with a fresh CoinGecko fetch.
func (p *MemoryProvider) Refresh(ctx context.Context) error {
	fresh, err := fetchFromCoinGecko(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.assets = fresh
	p.mu.Unlock()
	p.persistToCache(fresh)
	return nil
}

func (p *MemoryProvider) loadFromCache() error {
	raw, err := p.cache.GetAllAssetMetadata()
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return fmt.Errorf("cache is empty")
	}

	assets := make(map[string]AssetInfo, len(raw))
	for symbol, data := range raw {
		var info AssetInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue // skip corrupt entries
		}
		assets[symbol] = info
	}

	p.mu.Lock()
	p.assets = assets
	p.mu.Unlock()
	return nil
}

func (p *MemoryProvider) persistToCache(assets map[string]AssetInfo) {
	entries := make(map[string][]byte, len(assets))
	for symbol, info := range assets {
		data, err := json.Marshal(info)
		if err != nil {
			log.Printf("assetmeta: failed to marshal %s, skipping: %v", symbol, err)
			continue
		}
		entries[symbol] = data
	}

	if err := p.cache.PutAssetMetadataBulk(entries); err != nil {
		log.Printf("assetmeta: failed to persist asset metadata to cache: %v", err)
	}
}
