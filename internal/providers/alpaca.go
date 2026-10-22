package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/v3/alpaca"
	"github.com/alpacahq/alpaca-trade-api-go/v3/marketdata"
	"github.com/alpacahq/alpaca-trade-api-go/v3/marketdata/stream"

	"account-connect/assetmeta"
	"account-connect/config"
	messageutils "account-connect/internal/accountconnectmessageutils"
	"account-connect/internal/clients"
	"account-connect/internal/mappers"
	messages "account-connect/internal/messages"
)

const (
	alpacaPaperBaseURL      = "https://paper-api.alpaca.markets"
	alpacaLiveBaseURL       = "https://api.alpaca.markets"
	alpacaDataBaseURL       = "https://data.alpaca.markets"
	alpacaDefaultDepthLimit = 20
	alpacaDefaultTickLimit  = 500
	alpacaMaxTickLimit      = 1000 // same cap as Binance's maxTickLimit
)

var alpacaHTTPClient = &http.Client{Timeout: 10 * time.Second}

type alpacaBBOSub struct {
	strm chan []byte
}

type alpacaBookLevel struct {
	Price float64 `json:"p"`
	Size  float64 `json:"s"`
}

type alpacaOrderbook struct {
	Timestamp time.Time         `json:"t"`
	Bids      []alpacaBookLevel `json:"b"`
	Asks      []alpacaBookLevel `json:"a"`
}

type alpacaLocalBook struct {
	bids map[float64]float64
	asks map[float64]float64
}

func newAlpacaLocalBook() *alpacaLocalBook {
	return &alpacaLocalBook{
		bids: make(map[float64]float64),
		asks: make(map[float64]float64),
	}
}

// apply folds one Alpaca orderbook message into the book. A reset message
// carries the complete book, so anything held before it is discarded.
func (b *alpacaLocalBook) apply(reset bool, bids, asks []alpacaBookLevel) {
	if reset {
		b.bids = make(map[float64]float64, len(bids))
		b.asks = make(map[float64]float64, len(asks))
	}
	applyAlpacaLevels(b.bids, bids)
	applyAlpacaLevels(b.asks, asks)
}

func applyAlpacaLevels(side map[float64]float64, levels []alpacaBookLevel) {
	for _, l := range levels {
		if l.Size == 0 {
			delete(side, l.Price)
			continue
		}
		side[l.Price] = l.Size
	}
}

// snapshot returns the best n levels per side: bids high to low, asks low to high.
func (b *alpacaLocalBook) snapshot(n int) (bids, asks []alpacaBookLevel) {
	return topAlpacaLevels(b.bids, n, true), topAlpacaLevels(b.asks, n, false)
}

func topAlpacaLevels(side map[float64]float64, n int, descending bool) []alpacaBookLevel {
	out := make([]alpacaBookLevel, 0, len(side))
	for p, s := range side {
		out = append(out, alpacaBookLevel{Price: p, Size: s})
	}
	sort.Slice(out, func(i, j int) bool {
		if descending {
			return out[i].Price > out[j].Price
		}
		return out[i].Price < out[j].Price
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// alpacaDepthSub is one live depth subscription (one symbol, one client).
type alpacaDepthSub struct {
	mu    sync.Mutex
	book  *alpacaLocalBook
	limit int
	strm  chan []byte
}

// alpacaTickSub is one live tick subscription (one symbol, one client).
type alpacaTickSub struct {
	strm chan []byte
}

type AlpacaAdapter struct {
	AccountConnClient *clients.AccountConnectClient

	client        *alpaca.Client
	marketClient  *marketdata.Client
	accountID     messages.AccountID
	account       *alpaca.Account
	assetProvider assetmeta.Provider
	apiKey        string
	secretKey     string

	streamMu           sync.Mutex
	stocksStreamClient *stream.StocksClient
	stocksStreamCancel context.CancelFunc
	cryptoStreamClient *stream.CryptoClient
	cryptoStreamCancel context.CancelFunc

	depthMu   sync.Mutex
	depthSubs map[string]*alpacaDepthSub

	bboMu    sync.Mutex
	bboSubs  map[string]*alpacaBBOSub
	tickMu   sync.Mutex
	tickSubs map[string]*alpacaTickSub
}

func NewAlpacaAdapter(accountConnClient *clients.AccountConnectClient, assetProvider assetmeta.Provider) *AlpacaAdapter {
	return &AlpacaAdapter{
		AccountConnClient: accountConnClient,
		assetProvider:     assetProvider,
	}
}

func newAlpacaAccountID(n string) messages.AccountID {
	h := fnv.New64a()
	_, _ = h.Write([]byte(n))

	num := int64(h.Sum64() & math.MaxInt64)
	return messages.AccountID(num)
}

func (a *AlpacaAdapter) EstablishConnection(ctx context.Context, cfg config.PlatformConfigs) error {
	if cfg.Alpaca.ApiKey == "" || cfg.Alpaca.SecretKey == "" {
		return fmt.Errorf("alpaca api key and secret key are required")
	}

	baseURL := alpacaLiveBaseURL
	if cfg.Alpaca.Paper {
		baseURL = alpacaPaperBaseURL
	}

	a.apiKey = cfg.Alpaca.ApiKey
	a.secretKey = cfg.Alpaca.SecretKey

	client := alpaca.NewClient(alpaca.ClientOpts{
		APIKey:    cfg.Alpaca.ApiKey,
		APISecret: cfg.Alpaca.SecretKey,
		BaseURL:   baseURL,
	})

	a.marketClient = marketdata.NewClient(marketdata.ClientOpts{
		APIKey:    cfg.Alpaca.ApiKey,
		APISecret: cfg.Alpaca.SecretKey,
	})

	acct, err := client.GetAccount()
	if err != nil {
		return fmt.Errorf("failed to authenticate alpaca account: %w", err)
	}

	a.client = client
	a.account = acct
	a.accountID = newAlpacaAccountID(acct.ID)

	info := buildAlpacaAccountInfo(a.accountID, acct)

	res := messages.AccountConnectAccountInfoRes{
		Alpaca: &info,
	}
	resB, err := json.Marshal(res)
	if err != nil {
		return err
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeConnect, messages.Alpaca, a.AccountConnClient.ID, resB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	a.AccountConnClient.Send <- msgB
	return nil
}

func buildAlpacaAccountInfo(id messages.AccountID, acct *alpaca.Account) messages.AlpacaAccountInfo {
	return messages.AlpacaAccountInfo{
		AccountID:        int64(id),
		Status:           string(acct.Status),
		Currency:         acct.Currency,
		Cash:             acct.Cash.String(),
		Equity:           acct.Equity.String(),
		BuyingPower:      acct.BuyingPower.String(),
		PatternDayTrader: acct.PatternDayTrader,
		TradingBlocked:   acct.TradingBlocked,
	}
}

func (a *AlpacaAdapter) GetAccountInfo(ctx context.Context, payload messages.AccountConnectAccountInfoPayload) error {
	if a.client == nil || a.account == nil {
		return fmt.Errorf("alpaca account not connected")
	}
	info := buildAlpacaAccountInfo(a.accountID, a.account)
	infoB, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal alpaca account info: %w", err)
	}
	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeAccountInfo, messages.Alpaca, a.AccountConnClient.ID, infoB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	a.AccountConnClient.Send <- msgB
	return nil
}

func (a *AlpacaAdapter) AuthorizeAccount(ctx context.Context, payload messages.AccountConnectAuthorizeTradingAccountPayload) error {
	return fmt.Errorf("account authorization not applicable for alpaca — single account per connection")
}

func (a *AlpacaAdapter) GetUserAccounts(ctx context.Context) error {
	return fmt.Errorf("not yet supported for alpaca")
}

func (a *AlpacaAdapter) GetHistoricalTrades(ctx context.Context, payload messages.AccountConnectHistoricalDealsPayload) error {
	return fmt.Errorf("historical trades not yet supported for alpaca")
}

func (a *AlpacaAdapter) GetSymbolTrendBars(ctx context.Context, payload messages.AccountConnectTrendBarsPayload) error {
	if a.marketClient == nil {
		return fmt.Errorf("alpaca account not connected")
	}
	if payload.SymbolName == "" || payload.Period == "" {
		return fmt.Errorf("symbol name and period are required for trend bars")
	}
	if payload.FromTimestamp == nil || payload.ToTimestamp == nil {
		return fmt.Errorf("from and to timestamps are required for trend bars")
	}

	tf, err := mappers.PeriodStrToAlpacaTimeFrame(payload.Period)
	if err != nil {
		return err
	}

	start := time.UnixMilli(*payload.FromTimestamp)
	end := time.UnixMilli(*payload.ToTimestamp)

	var trendBars []messages.AccountConnectTrendBar

	if strings.Contains(payload.SymbolName, "/") {
		bars, err := a.marketClient.GetCryptoBars(payload.SymbolName, marketdata.GetCryptoBarsRequest{
			TimeFrame: tf,
			Start:     start,
			End:       end,
		})
		if err != nil {
			return fmt.Errorf("failed to fetch alpaca crypto bars: %w", err)
		}
		for _, b := range bars {
			trendBars = append(trendBars, messages.AccountConnectTrendBar{
				Open:                  b.Open,
				High:                  b.High,
				Low:                   b.Low,
				Close:                 b.Close,
				Volume:                int64(b.Volume),
				UtcTimestampInMinutes: uint32(b.Timestamp.Unix() / 60),
			})
		}
	} else {
		bars, err := a.marketClient.GetBars(payload.SymbolName, marketdata.GetBarsRequest{
			TimeFrame: tf,
			Start:     start,
			End:       end,
		})
		if err != nil {
			return fmt.Errorf("failed to fetch alpaca equity bars: %w", err)
		}
		for _, b := range bars {
			trendBars = append(trendBars, messages.AccountConnectTrendBar{
				Open:                  b.Open,
				High:                  b.High,
				Low:                   b.Low,
				Close:                 b.Close,
				Volume:                int64(b.Volume),
				UtcTimestampInMinutes: uint32(b.Timestamp.Unix() / 60),
			})
		}
	}

	res := messages.AccountConnectTrendBarRes{
		Trendbars: trendBars,
		Symbol:    payload.SymbolName,
		Period:    payload.Period,
	}
	resB, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("failed to marshal alpaca trend bars: %w", err)
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeOHLCHistory, messages.Alpaca, a.AccountConnClient.ID, resB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	a.AccountConnClient.Send <- msgB
	return nil
}

func (a *AlpacaAdapter) GetTradingSymbols(ctx context.Context, payload messages.AccountConnectSymbolsPayload) error {
	if a.client == nil {
		return fmt.Errorf("alpaca account not connected")
	}
	if payload.AccountID != a.accountID {
		return fmt.Errorf("unknown or expired account id: %d", payload.AccountID)
	}

	assets, err := a.client.GetAssets(alpaca.GetAssetsRequest{
		Status: "active",
	})
	if err != nil {
		return fmt.Errorf("failed to retrieve alpaca assets: %w", err)
	}

	syms := make([]messages.AccountConnectSymbol, 0, len(assets))
	for _, asset := range assets {
		if !asset.Tradable {
			continue
		}

		alp := &messages.AlpacaSymbolInfo{
			Symbol:       asset.Symbol,
			Name:         asset.Name,
			Exchange:     asset.Exchange,
			Tradable:     asset.Tradable,
			Marginable:   asset.Marginable,
			Shortable:    asset.Shortable,
			Fractionable: asset.Fractionable,
		}

		sym := messages.AccountConnectSymbol{
			SymbolName: asset.Symbol,
			Alpaca:     alp,
		}

		switch asset.Class {
		case "us_equity":
			alp.AssetClass = messages.AssetClassEquity

		case "crypto":
			alp.AssetClass = messages.AssetClassCrypto
			base, quote, ok := strings.Cut(asset.Symbol, "/")
			if ok {
				alp.BaseAsset = base
				alp.QuoteAsset = quote

				var baseName, quoteName string
				if info, ok := a.assetProvider.GetAssetInfo(base); ok {
					baseName = info.FullName
				}
				if info, ok := a.assetProvider.GetAssetInfo(quote); ok {
					quoteName = info.FullName
				}
				if baseName != "" && quoteName != "" {
					sym.SymbolName = fmt.Sprintf("%s / %s", baseName, quoteName)
				}
			}

		default:
			alp.AssetClass = string(asset.Class)
		}

		syms = append(syms, sym)
	}

	accconnectsyms := messages.AccountConnectSymbolRes{
		AccountConnectSymbols: syms,
	}
	accconnectsymsB, err := json.Marshal(accconnectsyms)
	if err != nil {
		return fmt.Errorf("failed to marshal alpaca symbol list: %w", err)
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeSymbolsList, messages.Alpaca, a.AccountConnClient.ID, accconnectsymsB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	a.AccountConnClient.Send <- msgB
	return nil
}

func (a *AlpacaAdapter) SubscribeOHLC(ctx context.Context, payload messages.AccountConnectCandlestickStreamPayload) error {
	if a.client == nil {
		return fmt.Errorf("alpaca account not connected")
	}
	if payload.AccountID != a.accountID {
		return fmt.Errorf("unknown or expired account id: %d", payload.AccountID)
	}
	if payload.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for alpaca candlestick stream")
	}
	if payload.Period != "1m" {
		return fmt.Errorf("alpaca only supports 1-minute native bar streaming — got period %q", payload.Period)
	}

	isCrypto := strings.Contains(payload.SymbolName, "/")

	streamId := fmt.Sprintf("candlestick_alpaca_%d_%s", payload.AccountID, payload.SymbolName)
	if err := a.AccountConnClient.AddStream(ctx, streamId); err != nil {
		return err
	}
	strm := a.AccountConnClient.Streams[streamId]

	go func() {
		for barB := range strm {
			msg := messageutils.CreateSuccessResponse(ctx, messages.TypeOHLCUpdate, messages.Alpaca, a.AccountConnClient.ID, barB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal alpaca candlestick stream message: %v", err)
				continue
			}
			a.AccountConnClient.Send <- msgB
		}
	}()

	if isCrypto {
		return a.subscribeCryptoBars(ctx, payload.SymbolName, strm)
	}
	return a.subscribeStockBars(ctx, payload.SymbolName, strm)
}

func (a *AlpacaAdapter) subscribeStockBars(ctx context.Context, symbol string, strm chan []byte) error {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()

	if a.stocksStreamClient == nil {
		streamCtx, cancel := context.WithCancel(context.Background())
		client := stream.NewStocksClient("iex", stream.WithCredentials(a.apiKey, a.secretKey))
		if err := client.Connect(streamCtx); err != nil {
			cancel()
			return fmt.Errorf("failed to connect alpaca stocks stream: %w", err)
		}
		a.stocksStreamClient = client
		a.stocksStreamCancel = cancel
	}

	handler := func(b stream.Bar) {
		bar := messages.AccountConnectCandlestickBar{
			OpenTime:  b.Timestamp.Unix(),
			Open:      b.Open,
			High:      b.High,
			Low:       b.Low,
			Close:     b.Close,
			Volume:    float64(b.Volume),
			CloseTime: b.Timestamp.Unix() + 60, // Alpaca bars are always 1-minute
			IsFinal:   true,                    // Alpaca only pushes completed bars, no in-progress updates
		}
		barRes := messages.AccountConnectCandlestickBarRes{
			Bars:     []messages.AccountConnectCandlestickBar{bar},
			Symbol:   b.Symbol,
			Interval: "1m",
		}
		barB, err := json.Marshal(barRes)
		if err != nil {
			log.Printf("Failed to marshal alpaca stock bar for %s: %v", symbol, err)
			return
		}
		select {
		case strm <- barB:
		default:
			log.Printf("Stream channel full for alpaca stock %s, dropping update", symbol)
		}
	}

	if err := a.stocksStreamClient.SubscribeToBars(handler, symbol); err != nil {
		return fmt.Errorf("failed to subscribe to alpaca stock bars for %s: %w", symbol, err)
	}
	return nil
}

func (a *AlpacaAdapter) subscribeCryptoBars(ctx context.Context, symbol string, strm chan []byte) error {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()

	if a.cryptoStreamClient == nil {
		streamCtx, cancel := context.WithCancel(context.Background())
		client := stream.NewCryptoClient("us", stream.WithCredentials(a.apiKey, a.secretKey))
		if err := client.Connect(streamCtx); err != nil {
			cancel()
			return fmt.Errorf("failed to connect alpaca crypto stream: %w", err)
		}
		a.cryptoStreamClient = client
		a.cryptoStreamCancel = cancel
	}

	handler := func(b stream.CryptoBar) {
		bar := messages.AccountConnectCandlestickBar{
			OpenTime:  b.Timestamp.Unix(),
			Open:      b.Open,
			High:      b.High,
			Low:       b.Low,
			Close:     b.Close,
			Volume:    b.Volume,
			CloseTime: b.Timestamp.Unix() + 60,
			IsFinal:   true,
		}
		barRes := messages.AccountConnectCandlestickBarRes{
			Bars:     []messages.AccountConnectCandlestickBar{bar},
			Symbol:   b.Symbol,
			Interval: "1m",
		}
		barB, err := json.Marshal(barRes)
		if err != nil {
			log.Printf("Failed to marshal alpaca crypto bar for %s: %v", symbol, err)
			return
		}
		select {
		case strm <- barB:
		default:
			log.Printf("Stream channel full for alpaca crypto %s, dropping update", symbol)
		}
	}

	if err := a.cryptoStreamClient.SubscribeToBars(handler, symbol); err != nil {
		return fmt.Errorf("failed to subscribe to alpaca crypto bars for %s: %w", symbol, err)
	}
	return nil
}

func (a *AlpacaAdapter) GetAccountOrders(ctx context.Context, payload messages.AccountConnectOrderPayload) error {
	return fmt.Errorf("account orders not yet supported for alpaca")
}

func (a *AlpacaAdapter) GetHistoricalTicks(ctx context.Context, payload messages.AccountConnectTickDataPayload) error {
	if a.marketClient == nil {
		return fmt.Errorf("alpaca account not connected")
	}
	if payload.Alpaca == nil {
		return fmt.Errorf("alpaca payload is required for historical tick data")
	}
	ap := payload.Alpaca
	if ap.AccountID != a.accountID {
		return fmt.Errorf("unknown or expired account id: %d", ap.AccountID)
	}
	if ap.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for historical tick data")
	}

	limit := alpacaDefaultTickLimit
	if ap.Limit != nil {
		limit = *ap.Limit
	}
	if limit <= 0 {
		return fmt.Errorf("limit must be positive, got %d", limit)
	}
	if limit > alpacaMaxTickLimit {
		limit = alpacaMaxTickLimit
	}

	var start, end time.Time
	if ap.FromTimestamp != nil {
		start = time.UnixMilli(*ap.FromTimestamp)
	}
	if ap.ToTimestamp != nil {
		end = time.UnixMilli(*ap.ToTimestamp)
	}

	var ticks []messages.AccountConnectTick

	if strings.Contains(ap.SymbolName, "/") {
		trades, err := a.marketClient.GetCryptoTrades(ap.SymbolName, marketdata.GetCryptoTradesRequest{
			Start:      start,
			End:        end,
			TotalLimit: limit,
			Sort:       marketdata.SortAsc,
		})
		if err != nil {
			return fmt.Errorf("failed to fetch alpaca crypto trades: %w", err)
		}
		ticks = mappers.AlpacaCryptoTradesToTicks(trades)
	} else {
		trades, err := a.marketClient.GetTrades(ap.SymbolName, marketdata.GetTradesRequest{
			Start:      start,
			End:        end,
			TotalLimit: limit,
			Sort:       marketdata.SortAsc,
		})
		if err != nil {
			return fmt.Errorf("failed to fetch alpaca stock trades: %w", err)
		}
		ticks = mappers.AlpacaTradesToTicks(trades)
	}

	res := messages.AccountConnectTickDataRes{
		Ticks:      ticks,
		HasMore:    len(ticks) == limit,
		SymbolName: ap.SymbolName,
	}
	resB, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("failed to marshal alpaca tick data: %w", err)
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeTicksHistory, messages.Alpaca, a.AccountConnClient.ID, resB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	a.AccountConnClient.Send <- msgB
	return nil
}

func (a *AlpacaAdapter) SubscribeTicks(ctx context.Context, payload messages.AccountConnectTickDataPayload) error {
	if a.client == nil {
		return fmt.Errorf("alpaca account not connected")
	}
	if payload.Alpaca == nil {
		return fmt.Errorf("alpaca payload is required for tick stream")
	}
	ap := payload.Alpaca
	if ap.AccountID != a.accountID {
		return fmt.Errorf("unknown or expired account id: %d", ap.AccountID)
	}
	if ap.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for tick stream")
	}

	// top of SubscribeTicks
	log.Printf("SubscribeTicks: symbol=%q crypto=%v", ap.SymbolName, strings.Contains(ap.SymbolName, "/"))

	symbol := ap.SymbolName
	isCrypto := strings.Contains(symbol, "/")

	streamId := fmt.Sprintf("ticks_alpaca_%d_%s", ap.AccountID, symbol)
	if err := a.AccountConnClient.AddStream(ctx, streamId); err != nil {
		return err
	}
	strm := a.AccountConnClient.Streams[streamId]

	a.tickMu.Lock()
	if a.tickSubs == nil {
		a.tickSubs = make(map[string]*alpacaTickSub)
	}
	a.tickSubs[symbol] = &alpacaTickSub{strm: strm}
	a.tickMu.Unlock()

	a.streamMu.Lock()
	defer a.streamMu.Unlock()

	if isCrypto {
		if err := a.ensureCryptoStreamClient(); err != nil {
			a.dropTickSub(symbol)
			return err
		}
		if err := a.cryptoStreamClient.SubscribeToTrades(a.handleCryptoTrade, symbol); err != nil {
			a.dropTickSub(symbol)
			return fmt.Errorf("failed to subscribe to alpaca crypto trades for %s: %w", symbol, err)
		}
	} else {
		if err := a.ensureStocksStreamClient(); err != nil {
			a.dropTickSub(symbol)
			return err
		}
		if err := a.stocksStreamClient.SubscribeToTrades(a.handleStockTrade, symbol); err != nil {
			a.dropTickSub(symbol)
			return fmt.Errorf("failed to subscribe to alpaca stock trades for %s: %w", symbol, err)
		}
	}

	go func() {
		for tickB := range strm {
			msg := messageutils.CreateSuccessResponse(ctx, messages.TypeTicksUpdate, messages.Alpaca, a.AccountConnClient.ID, tickB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal alpaca tick stream message: %v", err)
				continue
			}
			a.AccountConnClient.Send <- msgB
		}
	}()
	return nil
}

// UnsubscribeTicks stops the tick stream for one symbol.
func (a *AlpacaAdapter) UnsubscribeTicks(ctx context.Context, symbol string) error {
	a.dropTickSub(symbol)

	a.streamMu.Lock()
	defer a.streamMu.Unlock()

	if strings.Contains(symbol, "/") {
		if a.cryptoStreamClient == nil {
			return nil
		}
		if err := a.cryptoStreamClient.UnsubscribeFromTrades(symbol); err != nil {
			return fmt.Errorf("failed to unsubscribe from alpaca crypto trades for %s: %w", symbol, err)
		}
		return nil
	}
	if a.stocksStreamClient == nil {
		return nil
	}
	if err := a.stocksStreamClient.UnsubscribeFromTrades(symbol); err != nil {
		return fmt.Errorf("failed to unsubscribe from alpaca stock trades for %s: %w", symbol, err)
	}
	return nil
}

func (a *AlpacaAdapter) dropTickSub(symbol string) {
	a.tickMu.Lock()
	delete(a.tickSubs, symbol)
	a.tickMu.Unlock()
}

func (a *AlpacaAdapter) GetOrderBookDepth(ctx context.Context, payload messages.AccountConnectDepthPayload) error {
	if a.client == nil || a.marketClient == nil {
		return fmt.Errorf("alpaca account not connected")
	}
	if payload.Alpaca == nil {
		return fmt.Errorf("alpaca payload is required for order book depth")
	}

	ap := payload.Alpaca
	if ap.AccountID != a.accountID {
		return fmt.Errorf("unknown or expired account id: %d", ap.AccountID)
	}
	if ap.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for order book depth")
	}
	if !strings.Contains(ap.SymbolName, "/") {
		return fmt.Errorf("alpaca only supports order book depth for crypto symbols, got %q", ap.SymbolName)
	}

	book, err := a.fetchCryptoOrderbook(ctx, ap.SymbolName)
	if err != nil {
		return fmt.Errorf("failed to fetch alpaca crypto order book: %w", err)
	}

	bids := make([]messages.AccountConnectDepthLevel, 0, len(book.Bids))
	for _, l := range book.Bids {
		bids = append(bids, messages.AccountConnectDepthLevel{Price: l.Price, Quantity: l.Size})
	}
	asks := make([]messages.AccountConnectDepthLevel, 0, len(book.Asks))
	for _, l := range book.Asks {
		asks = append(asks, messages.AccountConnectDepthLevel{Price: l.Price, Quantity: l.Size})
	}

	res := messages.AccountConnectDepthRes{
		Bids:         bids,
		Asks:         asks,
		LastUpdateId: book.Timestamp.UnixMilli(),
		SymbolName:   ap.SymbolName,
	}
	resB, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("failed to marshal alpaca order book: %w", err)
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeDepthSnapshot, messages.Alpaca, a.AccountConnClient.ID, resB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	a.AccountConnClient.Send <- msgB
	return nil
}

func (a *AlpacaAdapter) SubscribeDepth(ctx context.Context, payload messages.AccountConnectDepthPayload) error {
	if a.client == nil {
		return fmt.Errorf("alpaca account not connected")
	}
	if payload.Alpaca == nil {
		return fmt.Errorf("alpaca payload is required for depth stream")
	}
	ap := payload.Alpaca
	if ap.AccountID != a.accountID {
		return fmt.Errorf("unknown or expired account id: %d", ap.AccountID)
	}
	if ap.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for depth stream")
	}
	if !strings.Contains(ap.SymbolName, "/") {
		return fmt.Errorf("alpaca only supports depth streaming for crypto symbols, got %q", ap.SymbolName)
	}

	//TODO: wire to ap.Limit here if the Alpaca payload  if available
	limit := alpacaDefaultDepthLimit

	streamId := fmt.Sprintf("depth_alpaca_%d_%s", ap.AccountID, ap.SymbolName)
	if err := a.AccountConnClient.AddStream(ctx, streamId); err != nil {
		return err
	}
	strm := a.AccountConnClient.Streams[streamId]

	sub := &alpacaDepthSub{book: newAlpacaLocalBook(), limit: limit, strm: strm}
	a.depthMu.Lock()
	if a.depthSubs == nil {
		a.depthSubs = make(map[string]*alpacaDepthSub)
	}
	a.depthSubs[ap.SymbolName] = sub
	a.depthMu.Unlock()

	a.streamMu.Lock()
	defer a.streamMu.Unlock()

	if err := a.ensureCryptoStreamClient(); err != nil {
		a.dropDepthSub(ap.SymbolName)
		return err
	}

	if err := a.cryptoStreamClient.SubscribeToOrderbooks(a.handleCryptoOrderbook, ap.SymbolName); err != nil {
		a.dropDepthSub(ap.SymbolName)
		return fmt.Errorf("failed to subscribe to alpaca crypto order book for %s: %w", ap.SymbolName, err)
	}

	go func() {
		for depthB := range strm {
			msg := messageutils.CreateSuccessResponse(ctx, messages.TypeDepthUpdate, messages.Alpaca, a.AccountConnClient.ID, depthB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal alpaca depth stream message: %v", err)
				continue
			}
			a.AccountConnClient.Send <- msgB
		}
	}()
	return nil
}

// UnsubscribeDepth stops the live order book for one symbol.
func (a *AlpacaAdapter) UnsubscribeDepth(ctx context.Context, symbol string) error {
	a.dropDepthSub(symbol)

	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.cryptoStreamClient == nil {
		return nil
	}
	if err := a.cryptoStreamClient.UnsubscribeFromOrderbooks(symbol); err != nil {
		return fmt.Errorf("failed to unsubscribe from alpaca crypto order book for %s: %w", symbol, err)
	}
	return nil
}

func (a *AlpacaAdapter) dropDepthSub(symbol string) {
	a.depthMu.Lock()
	delete(a.depthSubs, symbol)
	a.depthMu.Unlock()
}

func (a *AlpacaAdapter) SubscribeBBO(ctx context.Context, payload messages.AccountConnectBBOPayload) error {
	if a.client == nil {
		return fmt.Errorf("alpaca account not connected")
	}
	if payload.Alpaca == nil {
		return fmt.Errorf("alpaca payload is required for BBO stream")
	}
	ap := payload.Alpaca
	if ap.AccountID != a.accountID {
		return fmt.Errorf("unknown or expired account id: %d", ap.AccountID)
	}
	if ap.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for alpaca BBO stream")
	}

	symbol := ap.SymbolName
	isCrypto := strings.Contains(symbol, "/")

	streamId := fmt.Sprintf("bbo_alpaca_%d_%s", ap.AccountID, symbol)
	if err := a.AccountConnClient.AddStream(ctx, streamId); err != nil {
		return err
	}
	strm := a.AccountConnClient.Streams[streamId]

	a.bboMu.Lock()
	if a.bboSubs == nil {
		a.bboSubs = make(map[string]*alpacaBBOSub)
	}
	a.bboSubs[symbol] = &alpacaBBOSub{strm: strm}
	a.bboMu.Unlock()

	a.streamMu.Lock()
	defer a.streamMu.Unlock()

	if isCrypto {
		if err := a.ensureCryptoStreamClient(); err != nil {
			a.dropBBOSub(symbol)
			return err
		}
		if err := a.cryptoStreamClient.SubscribeToQuotes(a.handleCryptoQuote, symbol); err != nil {
			a.dropBBOSub(symbol)
			return fmt.Errorf("failed to subscribe to alpaca crypto quotes for %s: %w", symbol, err)
		}
	} else {
		if err := a.ensureStocksStreamClient(); err != nil {
			a.dropBBOSub(symbol)
			return err
		}
		if err := a.stocksStreamClient.SubscribeToQuotes(a.handleStockQuote, symbol); err != nil {
			a.dropBBOSub(symbol)
			return fmt.Errorf("failed to subscribe to alpaca stock quotes for %s: %w", symbol, err)
		}
	}

	// Started only after the subscription succeeded.
	go func() {
		for bboB := range strm {
			msg := messageutils.CreateSuccessResponse(ctx, messages.TypeBBOUpdate, messages.Alpaca, a.AccountConnClient.ID, bboB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal alpaca BBO stream message: %v", err)
				continue
			}
			a.AccountConnClient.Send <- msgB
		}
	}()
	return nil
}

// handleCryptoQuote runs on the read goroutine for every crypto quote.
func (a *AlpacaAdapter) handleCryptoQuote(q stream.CryptoQuote) {
	a.sendBBO(q.Symbol, q.BidPrice, q.BidSize, q.AskPrice, q.AskSize, q.Timestamp)
}

// handleStockQuote runs on the  read goroutine for every stock quote.
func (a *AlpacaAdapter) handleStockQuote(q stream.Quote) {
	a.sendBBO(q.Symbol, q.BidPrice, float64(q.BidSize), q.AskPrice, float64(q.AskSize), q.Timestamp)
}

func (a *AlpacaAdapter) sendBBO(symbol string, bidPrice, bidQty, askPrice, askQty float64, ts time.Time) {
	a.bboMu.Lock()
	sub := a.bboSubs[symbol]
	a.bboMu.Unlock()
	if sub == nil {
		return
	}

	bbo := messages.AccountConnectBBO{
		SymbolName: symbol,
		BidPrice:   bidPrice,
		BidQty:     bidQty,
		AskPrice:   askPrice,
		AskQty:     askQty,
		UpdateId:   ts.UnixNano(), // Alpaca has no update sequence; the quote timestamp is monotonic per symbol
	}
	bboB, err := json.Marshal(bbo)
	if err != nil {
		log.Printf("Failed to marshal alpaca BBO for %s: %v", symbol, err)
		return
	}
	select {
	case sub.strm <- bboB:
	default:
		log.Printf("BBO stream channel full for alpaca %s, dropping update", symbol)
	}
}

// UnsubscribeBBO stops the BBO stream for one symbol.
func (a *AlpacaAdapter) UnsubscribeBBO(ctx context.Context, symbol string) error {
	a.dropBBOSub(symbol)

	a.streamMu.Lock()
	defer a.streamMu.Unlock()

	if strings.Contains(symbol, "/") {
		if a.cryptoStreamClient == nil {
			return nil
		}
		if err := a.cryptoStreamClient.UnsubscribeFromQuotes(symbol); err != nil {
			return fmt.Errorf("failed to unsubscribe from alpaca crypto quotes for %s: %w", symbol, err)
		}
		return nil
	}
	if a.stocksStreamClient == nil {
		return nil
	}
	if err := a.stocksStreamClient.UnsubscribeFromQuotes(symbol); err != nil {
		return fmt.Errorf("failed to unsubscribe from alpaca stock quotes for %s: %w", symbol, err)
	}
	return nil
}

func (a *AlpacaAdapter) dropBBOSub(symbol string) {
	a.bboMu.Lock()
	delete(a.bboSubs, symbol)
	a.bboMu.Unlock()
}

func (a *AlpacaAdapter) Disconnect(ctx context.Context) error {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()

	if a.stocksStreamCancel != nil {
		a.stocksStreamCancel()
		a.stocksStreamClient = nil
		a.stocksStreamCancel = nil
	}
	if a.cryptoStreamCancel != nil {
		a.cryptoStreamCancel()
		a.cryptoStreamClient = nil
		a.cryptoStreamCancel = nil
	}

	a.depthMu.Lock()
	a.depthSubs = nil
	a.depthMu.Unlock()

	a.bboMu.Lock()
	a.bboSubs = nil
	a.bboMu.Unlock()

	a.tickMu.Lock()
	a.tickSubs = nil
	a.tickMu.Unlock()

	a.client = nil
	a.marketClient = nil
	a.account = nil
	a.apiKey = ""
	a.secretKey = ""

	return nil
}

func (a *AlpacaAdapter) fetchCryptoOrderbook(ctx context.Context, symbol string) (*alpacaOrderbook, error) {
	q := url.Values{}
	q.Set("symbols", symbol)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		alpacaDataBaseURL+"/v1beta3/crypto/us/latest/orderbooks?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("APCA-API-KEY-ID", a.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", a.secretKey)

	resp, err := alpacaHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("alpaca orderbook request failed: %s: %s", resp.Status, string(body))
	}

	var out struct {
		Orderbooks map[string]alpacaOrderbook `json:"orderbooks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	book, ok := out.Orderbooks[symbol]
	if !ok {
		return nil, fmt.Errorf("no orderbook returned for %s", symbol)
	}
	return &book, nil
}

// handleCryptoOrderbook runs on the read goroutine for every orderbook
// message, whatever the symbol.
func (a *AlpacaAdapter) handleCryptoOrderbook(o stream.CryptoOrderbook) {
	a.depthMu.Lock()
	sub := a.depthSubs[o.Symbol]
	a.depthMu.Unlock()
	if sub == nil {
		return
	}

	bidsIn := make([]alpacaBookLevel, 0, len(o.Bids))
	for _, e := range o.Bids {
		bidsIn = append(bidsIn, alpacaBookLevel{Price: e.Price, Size: e.Size})
	}
	asksIn := make([]alpacaBookLevel, 0, len(o.Asks))
	for _, e := range o.Asks {
		asksIn = append(asksIn, alpacaBookLevel{Price: e.Price, Size: e.Size})
	}

	sub.mu.Lock()
	sub.book.apply(o.Reset, bidsIn, asksIn)
	bids, asks := sub.book.snapshot(sub.limit)
	sub.mu.Unlock()

	res := messages.AccountConnectDepthRes{
		Bids:         toAlpacaDepthLevels(bids),
		Asks:         toAlpacaDepthLevels(asks),
		LastUpdateId: o.Timestamp.UnixMilli(),
		SymbolName:   o.Symbol,
	}
	resB, err := json.Marshal(res)
	if err != nil {
		log.Printf("Failed to marshal alpaca depth for %s: %v", o.Symbol, err)
		return
	}
	select {
	case sub.strm <- resB:
	default:
		log.Printf("Depth stream channel full for alpaca %s, dropping update", o.Symbol)
	}
}

// handleCryptoTrade runs on the SDK's read goroutine for every crypto trade.
func (a *AlpacaAdapter) handleCryptoTrade(t stream.CryptoTrade) {
	a.sendTick(t.Symbol, t.Price, t.Size, t.Timestamp)
}

// handleStockTrade runs on the SDK's read goroutine for every stock trade.
func (a *AlpacaAdapter) handleStockTrade(t stream.Trade) {
	a.sendTick(t.Symbol, t.Price, float64(t.Size), t.Timestamp)
}

func (a *AlpacaAdapter) sendTick(symbol string, price, qty float64, ts time.Time) {
	// top of sendTick
	log.Printf("sendTick: symbol=%q price=%v subFound=%v", symbol, price, a.tickSubs[symbol] != nil)
	a.tickMu.Lock()
	sub := a.tickSubs[symbol]
	a.tickMu.Unlock()
	if sub == nil {
		return
	}

	tick := messages.AccountConnectTickStreamMsg{
		SymbolName: symbol,
		Price:      price,
		Quantity:   qty,
		Timestamp:  ts.UnixMilli(),
	}
	tickB, err := json.Marshal(tick)
	if err != nil {
		log.Printf("Failed to marshal alpaca tick for %s: %v", symbol, err)
		return
	}
	select {
	case sub.strm <- tickB:
	default:
		log.Printf("Tick stream channel full for alpaca %s, dropping trade", symbol)
	}
}

// ensureCryptoStreamClient connects the shared crypto stream client on first
// use
func (a *AlpacaAdapter) ensureCryptoStreamClient() error {
	if a.cryptoStreamClient != nil {
		return nil
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	client := stream.NewCryptoClient("us", stream.WithCredentials(a.apiKey, a.secretKey))
	if err := client.Connect(streamCtx); err != nil {
		cancel()
		return fmt.Errorf("failed to connect alpaca crypto stream: %w", err)
	}
	a.cryptoStreamClient = client
	a.cryptoStreamCancel = cancel
	return nil
}

// ensureStocksStreamClient connects the shared stocks stream client on first
// use.
func (a *AlpacaAdapter) ensureStocksStreamClient() error {
	if a.stocksStreamClient != nil {
		return nil
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	client := stream.NewStocksClient("iex", stream.WithCredentials(a.apiKey, a.secretKey))
	if err := client.Connect(streamCtx); err != nil {
		cancel()
		return fmt.Errorf("failed to connect alpaca stocks stream: %w", err)
	}
	a.stocksStreamClient = client
	a.stocksStreamCancel = cancel
	return nil
}

func toAlpacaDepthLevels(in []alpacaBookLevel) []messages.AccountConnectDepthLevel {
	out := make([]messages.AccountConnectDepthLevel, 0, len(in))
	for _, l := range in {
		out = append(out, messages.AccountConnectDepthLevel{Price: l.Price, Quantity: l.Size})
	}
	return out
}
