package assetmeta

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const coinGeckoListURL = "https://api.coingecko.com/api/v3/coins/list"

type coinGeckoCoin struct {
	ID     string `json:"id"`
	Symbol string `json:"symbol"`
	Name   string `json:"name"`
}

func fetchFromCoinGecko(ctx context.Context) (map[string]AssetInfo, error) {
	result := make(map[string]AssetInfo)

	topCoins, err := fetchTopCoinsByMarketCap(ctx)
	if err != nil {
		log.Printf("assetmeta: failed to fetch top coins by market cap, proceeding with full list only: %v", err)
	} else {
		for _, c := range topCoins {
			symbol := strings.ToUpper(strings.TrimSpace(c.Symbol))
			if symbol == "" {
				continue
			}
			result[symbol] = AssetInfo{Symbol: symbol, FullName: c.Name}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, coinGeckoListURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build coingecko request: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch coingecko coin list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coingecko coin list request returned status %d", resp.StatusCode)
	}

	var coins []coinGeckoCoin
	if err := json.NewDecoder(resp.Body).Decode(&coins); err != nil {
		return nil, fmt.Errorf("failed to decode coingecko coin list: %w", err)
	}

	for _, c := range coins {
		symbol := strings.ToUpper(strings.TrimSpace(c.Symbol))
		if symbol == "" {
			continue
		}
		if _, exists := result[symbol]; exists {
			continue
		}
		result[symbol] = AssetInfo{Symbol: symbol, FullName: c.Name}
	}

	return result, nil
}

type coinGeckoMarketCoin struct {
	Symbol string `json:"symbol"`
	Name   string `json:"name"`
}

func fetchTopCoinsByMarketCap(ctx context.Context) ([]coinGeckoMarketCoin, error) {
	url := "https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=250&page=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coingecko markets request returned status %d", resp.StatusCode)
	}
	var coins []coinGeckoMarketCoin
	if err := json.NewDecoder(resp.Body).Decode(&coins); err != nil {
		return nil, err
	}
	return coins, nil
}
