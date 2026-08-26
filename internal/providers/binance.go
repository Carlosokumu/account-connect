package providers

import (
	"account-connect/config"
	messageutils "account-connect/internal/accountconnectmessageutils"
	"account-connect/internal/clients"
	"account-connect/internal/mappers"
	"account-connect/internal/messages"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"account-connect/persistence"

	"github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/delivery"
	"github.com/adshao/go-binance/v2/futures"
	"github.com/gorilla/websocket"
)

var defaultQuoteAssets = []string{"USDT", "BUSD", "BTC", "ETH", "BNB"}

const (
	symbolsCacheTTL      = 24 * time.Hour
	tradesCacheTTL       = 1 * time.Hour
	workerPoolSize       = 10
	workerCallIntervalMs = 1000
)

// tradeResult carries the result of a single worker's symbol trade fetch.
type tradeResult struct {
	symbol string
	trades []messages.BinanceAccountConnectDeal
	err    error
}

// MiniTickerEvent represents a miniTicker WebSocket message
type MiniTickerEvent struct {
	EventTime   int64  `json:"E"`
	Symbol      string `json:"s"`
	ClosePrice  string `json:"c"`
	OpenPrice   string `json:"o"`
	HighPrice   string `json:"h"`
	LowPrice    string `json:"l"`
	Volume      string `json:"v"`
	QuoteVolume string `json:"q"`
}

// BinanceKlineEvent represents a kline/candlestick WebSocket message from Binance
type BinanceKlineEvent struct {
	EventType string       `json:"e"`
	EventTime int64        `json:"E"`
	Symbol    string       `json:"s"`
	Kline     BinanceKline `json:"k"`
}

type BinanceKline struct {
	StartTime           int64  `json:"t"`
	EndTime             int64  `json:"T"`
	Symbol              string `json:"s"`
	Interval            string `json:"i"`
	FirstTradeId        int64  `json:"f"`
	LastTradeId         int64  `json:"L"`
	Open                string `json:"o"`
	Close               string `json:"c"`
	High                string `json:"h"`
	Low                 string `json:"l"`
	Volume              string `json:"v"`
	NumberOfTrades      int64  `json:"n"`
	IsFinal             bool   `json:"x"`
	QuoteVolume         string `json:"q"`
	TakerBuyBaseVolume  string `json:"V"`
	TakerBuyQuoteVolume string `json:"Q"`
	Ignore              string `json:"B"`
}

// WsMiniTickerServe connects to Binance miniTicker stream and streams updates
func WsMiniTickerServe(
	ctx context.Context,
	symbol string,
	wsHandler func(event *MiniTickerEvent),
	errHandler func(err error),
) (doneC chan struct{}, stopC chan struct{}, err error) {
	doneC = make(chan struct{})
	stopC = make(chan struct{})

	wsURL := url.URL{
		Scheme: "wss",
		Host:   "stream.binance.com:9443",
		Path:   fmt.Sprintf("/ws/%s@miniTicker", symbol),
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect WebSocket: %w", err)
	}
	go func() {
		defer close(doneC)
		defer conn.Close()

		for {
			msgChan := make(chan []byte)
			errChan := make(chan error)

			go func() {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					errChan <- err
					return
				}
				msgChan <- msg
			}()

			select {
			case <-stopC:
				log.Printf("Stopped miniTicker stream for %s", symbol)
				return
			case <-ctx.Done():
				log.Printf("Context cancelled for %s", symbol)
				return
			case err := <-errChan:
				errHandler(fmt.Errorf("read error for %s: %w", symbol, err))
				return
			case msg := <-msgChan:
				var event MiniTickerEvent
				if err := json.Unmarshal(msg, &event); err != nil {
					errHandler(fmt.Errorf("unmarshal error for %s: %w", symbol, err))
					return
				}
				wsHandler(&event)
			}
		}
	}()

	return doneC, stopC, nil
}

type BinanceConnection struct {
	AccountConnClient *clients.AccountConnectClient
	wsServeMux        sync.Mutex
	doneChans         map[string]chan struct{}
	accountType       messages.BinanceAccountType
	account           *binance.Account

	spotClient     *binance.Client
	futuresClient  *futures.Client
	deliveryClient *delivery.Client

	futuresAccount  *futures.Account
	deliveryAccount *delivery.Account
	marginAccount   *binance.MarginAccount

	cache persistence.AccountConnectCache
}

func NewBinanceConnection(accountConnClient *clients.AccountConnectClient, cache persistence.AccountConnectCache) *BinanceConnection {
	return &BinanceConnection{
		AccountConnClient: accountConnClient,
		doneChans:         make(map[string]chan struct{}),
		cache:             cache,
	}
}

func (b *BinanceConnection) Connect(ctx context.Context, apiKey, secretKey string, accountType messages.BinanceAccountType) error {
	if apiKey == "" || secretKey == "" {
		return fmt.Errorf("binance api key and secret key are required")
	}

	binance.UseDemo = true
	futures.UseDemo = true
	delivery.UseDemo = true

	switch accountType {
	case messages.BinanceAccountTypeSpot:
		client := binance.NewClient(apiKey, secretKey)
		account, err := client.NewGetAccountService().Do(ctx)
		if err != nil {
			return fmt.Errorf("failed to authenticate binance spot account: %w", err)
		}
		b.spotClient = client
		b.account = account

	case messages.BinanceAccountTypeFutures:
		client := binance.NewFuturesClient(apiKey, secretKey)
		account, err := client.NewGetAccountService().Do(ctx)
		if err != nil {
			return fmt.Errorf("failed to authenticate binance futures account: %w", err)
		}
		b.futuresClient = client
		b.futuresAccount = account

	case messages.BinanceAccountTypeDelivery:
		client := binance.NewDeliveryClient(apiKey, secretKey)
		account, err := client.NewGetAccountService().Do(ctx)
		if err != nil {
			return fmt.Errorf("failed to authenticate binance delivery account: %w", err)
		}
		b.deliveryClient = client
		b.deliveryAccount = account

	case messages.BinanceAccountTypeMargin:
		client := binance.NewClient(apiKey, secretKey)
		marginAccount, err := client.NewGetMarginAccountService().Do(ctx)
		if err != nil {
			return fmt.Errorf("failed to authenticate binance margin account: %w", err)
		}
		b.spotClient = client
		b.marginAccount = marginAccount

	default:
		return fmt.Errorf("unsupported binance account type: %s", accountType)
	}

	b.accountType = accountType
	return nil
}

// accountScopedClientID returns a cache key prefix that scopes trades to
// a specific client + account type combination, preventing collisions
// between e.g. SPOT and FUTURES trades for the same client.
func (b *BinanceConnection) accountScopedClientID() string {
	return b.AccountConnClient.ID + ":" + string(b.accountType)
}

func (b *BinanceConnection) GetHistoricalTrades(ctx context.Context, payload messages.AccountConnectHistoricalDealsPayload) error {
	quoteAssets := defaultQuoteAssets
	var limitPerSymbol int = 500
	if payload.Binance != nil {
		if len(payload.Binance.QuoteAssets) > 0 {
			quoteAssets = payload.Binance.QuoteAssets
		}
		if payload.Binance.LimitPerSymbol != nil {
			limitPerSymbol = *payload.Binance.LimitPerSymbol
		}
	}

	cacheKey := b.accountScopedClientID()

	// fast path: fresh trades already cached for this client+accountType
	cachedAll, err := b.cache.GetAllTrades(cacheKey, tradesCacheTTL)
	if err != nil && err != persistence.ErrCacheExpired {
		return fmt.Errorf("failed to read trades cache: %w", err)
	}
	if len(cachedAll) > 0 {
		log.Printf("Returning cached trades for %s", cacheKey)
		return b.sendAggregatedTrades(ctx, cachedAll)
	}

	// derive nonzero base assets from cached account info
	baseAssets, err := b.getNonZeroBaseAssets()
	if err != nil {
		return err
	}
	if len(baseAssets) == 0 {
		return fmt.Errorf("no nonzero balances found in account")
	}
	for _, v := range baseAssets {
		log.Printf("Got asset: %v", v)
	}
	log.Printf("Derived %d nonzero base assets for %s", len(baseAssets), cacheKey)

	// load cached symbol list to validate pairs — must have called GetTradingSymbols first
	accountTypeKey := string(b.accountType)
	symsB, err := b.cache.GetSymbols(accountTypeKey, symbolsCacheTTL)
	if err == persistence.ErrCacheExpired || symsB == nil {
		return fmt.Errorf("symbol list not available: call GetTradingSymbols first to populate the cache")
	}
	if err != nil {
		return fmt.Errorf("failed to read symbols cache: %w", err)
	}

	var allSymbols []messages.AccountConnectSymbol
	if err := json.Unmarshal(symsB, &allSymbols); err != nil {
		return fmt.Errorf("failed to unmarshal cached symbols: %w", err)
	}

	// build validity set from cached symbols
	validSymbols := make(map[string]bool, len(allSymbols))
	for _, sym := range allSymbols {
		if sym.SymbolName != nil {
			validSymbols[*sym.SymbolName] = true
		}
	}

	// derive candidate pairs: nonzero base asset × quote asset, validated against symbol list
	var symbols []string
	seen := make(map[string]bool)
	for _, base := range baseAssets {
		for _, quote := range quoteAssets {
			if base == quote {
				continue
			}
			candidate := base + quote
			if validSymbols[candidate] && !seen[candidate] {
				symbols = append(symbols, candidate)
				seen[candidate] = true
			}
		}
	}

	if len(symbols) == 0 {
		return fmt.Errorf("no valid trading pairs found for account balances")
	}
	log.Printf("Fetching trades for %d derived pairs for %s", len(symbols), cacheKey)

	// fan out to worker pool — safe rate: 2 workers, 1.5s between calls
	symbolsCh := make(chan string, len(symbols))
	for _, sym := range symbols {
		symbolsCh <- sym
	}
	close(symbolsCh)

	resultsCh := make(chan tradeResult, len(symbols))

	var wg sync.WaitGroup
	for i := 0; i < workerPoolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for symbol := range symbolsCh {
				time.Sleep(time.Duration(workerCallIntervalMs) * time.Millisecond)

				trades, err := b.fetchTradesForSymbol(ctx, symbol, limitPerSymbol, payload.FromTimestamp, payload.ToTimestamp)
				if err != nil {
					log.Printf("Worker: failed to fetch trades for %s: %v", symbol, err)
					resultsCh <- tradeResult{symbol: symbol, err: err}
					continue
				}
				if len(trades) == 0 {
					continue
				}

				tradesB, err := json.Marshal(trades)
				if err != nil {
					log.Printf("Worker: failed to marshal trades for %s: %v", symbol, err)
				} else {
					if err := b.cache.PutTrades(cacheKey, symbol, tradesB); err != nil {
						log.Printf("Worker: failed to cache trades for %s: %v", symbol, err)
					}
				}

				resultsCh <- tradeResult{symbol: symbol, trades: trades}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	var allTrades []messages.BinanceAccountConnectDeal
	for result := range resultsCh {
		if result.err != nil {
			continue
		}
		allTrades = append(allTrades, result.trades...)
	}

	res := messages.AccountConnectHistoricalDealsRes{
		Binance: &messages.BinanceAccountConnectDealsRes{
			Trades: allTrades,
		},
	}
	resB, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("failed to marshal historical trades: %w", err)
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeHistoricalTrades, messages.Binance, b.AccountConnClient.ID, resB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.AccountConnClient.Send <- msgB
	return nil
}

// sendAggregatedTrades assembles and sends a cached trade result to the client.
func (b *BinanceConnection) sendAggregatedTrades(ctx context.Context, cachedAll map[string][]byte) error {
	var allTrades []messages.BinanceAccountConnectDeal
	for _, tradesB := range cachedAll {
		var trades []messages.BinanceAccountConnectDeal
		if err := json.Unmarshal(tradesB, &trades); err != nil {
			log.Printf("Failed to unmarshal cached trades: %v", err)
			continue
		}
		allTrades = append(allTrades, trades...)
	}

	res := messages.AccountConnectHistoricalDealsRes{
		Binance: &messages.BinanceAccountConnectDealsRes{
			Trades: allTrades,
		},
	}
	resB, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("failed to marshal cached trades: %w", err)
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeHistoricalTrades, messages.Binance, b.AccountConnClient.ID, resB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.AccountConnClient.Send <- msgB
	return nil
}

// StartSymbolPriceStream starts a real-time price stream for a symbol(trading pair)
func (b *BinanceConnection) StartSymbolPriceStream(ctx context.Context, symbol string, strm chan []byte) error {
	b.wsServeMux.Lock()
	defer b.wsServeMux.Unlock()

	doneChan := make(chan struct{})
	b.doneChans[symbol] = doneChan

	wsHandler := func(event *MiniTickerEvent) {
		msg := messages.AccountConnectCryptoPrice{
			Symbol: event.Symbol,
			Price:  event.ClosePrice,
		}
		msgB, err := json.Marshal(msg)
		if err != nil {
			log.Printf("Failed to unmarshal crypto price: %v", err)
		}
		select {
		case <-ctx.Done():
			b.StopSymbolPriceStream(symbol)
			return
		case strm <- msgB:
		default:
			log.Printf("Stream channel full for %s, dropping update", symbol)
		}

	}

	errHandler := func(err error) {
		log.Printf("Error in price stream for %s: %v\n", symbol, err)
		b.StopSymbolPriceStream(symbol)
	}

	doneC, stopC, err := WsMiniTickerServe(ctx, symbol, wsHandler, errHandler)
	if err != nil {
		delete(b.doneChans, symbol)
		close(doneChan)
		return fmt.Errorf("failed to start websocket: %v", err)
	}

	go func() {
		select {
		case <-ctx.Done():
			log.Printf("Context cancelled for %s: %v", symbol, ctx.Err())
			stopC <- struct{}{}
			delete(b.doneChans, symbol)
			close(doneChan)
		case <-doneC:
			delete(b.doneChans, symbol)
			close(doneChan)
		}
	}()

	return nil
}

// StopSymbolPriceStream stops a running price stream
func (b *BinanceConnection) StopSymbolPriceStream(symbol string) {
	b.wsServeMux.Lock()
	defer b.wsServeMux.Unlock()

	if doneChan, exists := b.doneChans[symbol]; exists {
		close(doneChan)
		delete(b.doneChans, symbol)
	}
}

// startMarketPriceStream will start a realtime market price stream for a given symbol(trading pair) for the specified stream id
func (b *BinanceConnection) startMarketPriceStream(ctx context.Context, sym string, streamID string) error {
	if stream, exists := b.AccountConnClient.Streams[streamID]; exists {
		sym = strings.ToLower(sym)
		err := b.StartSymbolPriceStream(ctx, sym, stream)
		if err != nil {
			log.Printf("Failed to start stream for symbol: %s and stream id: %s", sym, streamID)
			return err
		}
		return nil
	}
	return fmt.Errorf("stream ID %s not found", streamID)
}

// getValidSymbols fetches exchange info and returns a set of currently trading symbols.
func (b *BinanceConnection) getValidSymbols(ctx context.Context) (map[string]bool, error) {
	switch b.accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		exchangeInfo, err := b.spotClient.NewExchangeInfoService().Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch spot exchange info: %w", err)
		}
		valid := make(map[string]bool, len(exchangeInfo.Symbols))
		for _, sym := range exchangeInfo.Symbols {
			if sym.Status == "TRADING" {
				valid[sym.Symbol] = true
			}
		}
		return valid, nil

	case messages.BinanceAccountTypeFutures:
		exchangeInfo, err := b.futuresClient.NewExchangeInfoService().Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch futures exchange info: %w", err)
		}
		valid := make(map[string]bool, len(exchangeInfo.Symbols))
		for _, sym := range exchangeInfo.Symbols {
			if sym.Status == "TRADING" {
				valid[sym.Symbol] = true
			}
		}
		return valid, nil

	case messages.BinanceAccountTypeDelivery:
		exchangeInfo, err := b.deliveryClient.NewExchangeInfoService().Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch delivery exchange info: %w", err)
		}
		valid := make(map[string]bool, len(exchangeInfo.Symbols))
		// for _,  := range exchangeInfo.Symbols {
		// 	// if sym.Status == "TRADING" {
		// 	// 	valid[sym.Symbol] = true
		// 	// }
		// }
		return valid, nil

	default:
		return nil, fmt.Errorf("unsupported account type for exchange info: %s", b.accountType)
	}
}

func (b *BinanceConnection) GetTraderInfo(ctx context.Context) error {
	var info messages.AccountConnectBinanceTraderInfo

	switch b.accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		if b.account == nil {
			return fmt.Errorf("no cached spot/margin account info")
		}
		var balances []messages.AccountConnectBinanceBalance
		for _, bal := range b.account.Balances {
			free, err := strconv.ParseFloat(bal.Free, 64)
			if err != nil {
				log.Printf("Failed to parse free balance for %s: %v", bal.Asset, err)
				continue
			}
			locked, err := strconv.ParseFloat(bal.Locked, 64)
			if err != nil {
				log.Printf("Failed to parse locked balance for %s: %v", bal.Asset, err)
				continue
			}
			if free == 0 && locked == 0 {
				continue
			}
			balances = append(balances, messages.AccountConnectBinanceBalance{
				Asset:  bal.Asset,
				Free:   bal.Free,
				Locked: bal.Locked,
			})
		}
		info = messages.AccountConnectBinanceTraderInfo{
			AccountType: string(b.accountType),
			Spot: &messages.AccountConnectBinanceSpotInfo{
				CanTrade:        b.account.CanTrade,
				CanWithdraw:     b.account.CanWithdraw,
				CanDeposit:      b.account.CanDeposit,
				MakerCommission: b.account.MakerCommission,
				TakerCommission: b.account.TakerCommission,
				Balances:        balances,
			},
		}

	case messages.BinanceAccountTypeFutures:
		if b.futuresAccount == nil {
			return fmt.Errorf("no cached futures account info")
		}
		var assets []messages.AccountConnectBinanceBalance
		for _, a := range b.futuresAccount.Assets {
			balance, err := strconv.ParseFloat(a.WalletBalance, 64)
			if err != nil || balance == 0 {
				continue
			}
			assets = append(assets, messages.AccountConnectBinanceBalance{
				Asset:  a.Asset,
				Free:   a.AvailableBalance,
				Locked: a.WalletBalance,
			})
		}
		info = messages.AccountConnectBinanceTraderInfo{
			AccountType: string(b.accountType),
			Futures: &messages.AccountConnectBinanceFuturesInfo{
				CanTrade:              b.futuresAccount.CanTrade,
				TotalWalletBalance:    b.futuresAccount.TotalWalletBalance,
				TotalUnrealizedProfit: b.futuresAccount.TotalUnrealizedProfit,
				TotalMarginBalance:    b.futuresAccount.TotalMarginBalance,
				Assets:                assets,
			},
		}

	case messages.BinanceAccountTypeDelivery:
		if b.deliveryAccount == nil {
			return fmt.Errorf("no cached delivery account info")
		}
		var assets []messages.AccountConnectBinanceBalance
		for _, a := range b.deliveryAccount.Assets {
			balance, err := strconv.ParseFloat(a.WalletBalance, 64)
			if err != nil || balance == 0 {
				continue
			}
			assets = append(assets, messages.AccountConnectBinanceBalance{
				Asset:  a.Asset,
				Free:   a.AvailableBalance,
				Locked: a.WalletBalance,
			})
		}
		info = messages.AccountConnectBinanceTraderInfo{
			AccountType: string(b.accountType),
			Delivery: &messages.AccountConnectBinanceDeliveryInfo{
				CanTrade: b.deliveryAccount.CanTrade,
				// TotalWalletBalance: b.deliveryAccount.TotalWalletBalance,
				Assets: assets,
			},
		}

	default:
		return fmt.Errorf("unsupported account type for trader info: %s", b.accountType)
	}

	infoB, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal binance trader info: %w", err)
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeTraderInfo, messages.Binance, b.AccountConnClient.ID, infoB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.AccountConnClient.Send <- msgB
	return nil
}

func (b *BinanceConnection) GetAccountOrders(ctx context.Context, accountConnectPayload messages.AccountConnectOrderPayload) error {
	return nil
}

// spotOrMarginClient returns the spot client, valid for both SPOT and MARGIN account types.
// Returns an error if called on a futures/delivery connection.
func (b *BinanceConnection) spotOrMarginClient() (*binance.Client, error) {
	if b.spotClient == nil {
		return nil, fmt.Errorf("operation requires a SPOT or MARGIN account, current type: %s", b.accountType)
	}
	return b.spotClient, nil
}

// futuresClient returns the USDT-M futures client.
func (b *BinanceConnection) getFuturesClient() (*futures.Client, error) {
	if b.futuresClient == nil {
		return nil, fmt.Errorf("operation requires a FUTURES account, current type: %s", b.accountType)
	}
	return b.futuresClient, nil
}

// deliveryClient returns the Coin-M futures client.
func (b *BinanceConnection) getDeliveryClient() (*delivery.Client, error) {
	if b.deliveryClient == nil {
		return nil, fmt.Errorf("operation requires a DELIVERY account, current type: %s", b.accountType)
	}
	return b.deliveryClient, nil
}

func (b *BinanceConnection) GetSymbolTrendBars(ctx context.Context, trendbarsArgs messages.AccountConnectTrendBarsPayload) ([]byte, error) {
	var (
		ohlc []*binance.Kline
		err  error
	)

	if trendbarsArgs.SymbolName == "" || trendbarsArgs.Period == "" {
		return nil, fmt.Errorf("symbol name and period are required for trend bars")
	}
	if trendbarsArgs.FromTimestamp == nil || trendbarsArgs.ToTimestamp == nil {
		return nil, fmt.Errorf("from and to timestamps are required for trend bars")
	}

	switch b.accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		client, err := b.spotOrMarginClient()
		if err != nil {
			return nil, err
		}
		ohlc, err = client.NewKlinesService().
			Symbol(trendbarsArgs.SymbolName).
			Interval(trendbarsArgs.Period).
			StartTime(*trendbarsArgs.FromTimestamp).
			EndTime(*trendbarsArgs.ToTimestamp).
			Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch klines: %w", err)
		}

	case messages.BinanceAccountTypeFutures:
		fc, err := b.getFuturesClient()
		if err != nil {
			return nil, err
		}
		futuresOhlc, err := fc.NewKlinesService().
			Symbol(trendbarsArgs.SymbolName).
			Interval(trendbarsArgs.Period).
			StartTime(*trendbarsArgs.FromTimestamp).
			EndTime(*trendbarsArgs.ToTimestamp).
			Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch futures klines: %w", err)
		}
		ohlc = mappers.FuturesKlinesToBinanceKlines(futuresOhlc)

	case messages.BinanceAccountTypeDelivery:
		dc, err := b.getDeliveryClient()
		if err != nil {
			return nil, err
		}
		deliveryOhlc, err := dc.NewKlinesService().
			Symbol(trendbarsArgs.SymbolName).
			Interval(trendbarsArgs.Period).
			StartTime(*trendbarsArgs.FromTimestamp).
			EndTime(*trendbarsArgs.ToTimestamp).
			Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch delivery klines: %w", err)
		}
		ohlc = mappers.DeliveryKlinesToBinanceKlines(deliveryOhlc)

	default:
		return nil, fmt.Errorf("unsupported account type for trend bars: %s", b.accountType)
	}

	acctrendbars, err := mappers.BinanceKlineDataToAccountConnectTrendBar(ohlc)
	if err != nil {
		return nil, err
	}

	acctrendbarsRes := messages.AccountConnectTrendBarRes{
		Trendbars: acctrendbars,
		Symbol:    trendbarsArgs.SymbolName,
		Period:    trendbarsArgs.Period,
	}
	acctrendbarsB, err := json.Marshal(acctrendbarsRes)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal trend bars: %w", err)
	}
	return acctrendbarsB, nil
}

// GetBinanceTradingSymbols retrieves tradable symbols, using the cache when available.
func (b *BinanceConnection) GetBinanceTradingSymbols(ctx context.Context) ([]messages.AccountConnectSymbol, error) {
	accountTypeKey := string(b.accountType)

	// check cache first
	cached, err := b.cache.GetSymbols(accountTypeKey, symbolsCacheTTL)
	if err != nil && err != persistence.ErrCacheExpired {
		return nil, fmt.Errorf("failed to read symbols cache: %w", err)
	}
	if cached != nil {
		var syms []messages.AccountConnectSymbol
		if err := json.Unmarshal(cached, &syms); err != nil {
			log.Printf("Failed to unmarshal cached symbols, refetching: %v", err)
		} else {
			log.Printf("Returning cached symbols for account type: %s", accountTypeKey)
			return syms, nil
		}
	}

	// cache miss or expired — fetch from Binance
	var syms []messages.AccountConnectSymbol

	switch b.accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		client, err := b.spotOrMarginClient()
		if err != nil {
			return nil, err
		}
		exchangeInfo, err := client.NewExchangeInfoService().Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve binance spot trading symbols: %w", err)
		}
		syms = mappers.BinanceSymbolToAccountConnectSymbol(exchangeInfo.Symbols)

	case messages.BinanceAccountTypeFutures:
		fc, err := b.getFuturesClient()
		if err != nil {
			return nil, err
		}
		exchangeInfo, err := fc.NewExchangeInfoService().Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve binance futures trading symbols: %w", err)
		}
		syms = mappers.FuturesSymbolToAccountConnectSymbol(exchangeInfo.Symbols)

	case messages.BinanceAccountTypeDelivery:
		dc, err := b.getDeliveryClient()
		if err != nil {
			return nil, err
		}
		exchangeInfo, err := dc.NewExchangeInfoService().Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve binance delivery trading symbols: %w", err)
		}
		syms = mappers.DeliverySymbolToAccountConnectSymbol(exchangeInfo.Symbols)

	default:
		return nil, fmt.Errorf("unsupported account type for trading symbols: %s", b.accountType)
	}

	// store in cache for future calls
	symsB, err := json.Marshal(syms)
	if err != nil {
		log.Printf("Failed to marshal symbols for cache, skipping cache write: %v", err)
	} else {
		if err := b.cache.PutSymbols(accountTypeKey, symsB); err != nil {
			log.Printf("Failed to write symbols to cache: %v", err)
			// non-fatal — we still have the data, just won't be cached
		}
	}

	return syms, nil
}

type BinanceAdapter struct {
	binanceConn *BinanceConnection
}

func NewBinanceAdapter(accountConnClient *clients.AccountConnectClient, cache persistence.AccountConnectCache) *BinanceAdapter {
	return &BinanceAdapter{
		binanceConn: NewBinanceConnection(accountConnClient, cache),
	}
}

func (b *BinanceAdapter) EstablishConnection(ctx context.Context, cfg config.PlatformConfigs) error {
	conn := NewBinanceConnection(b.binanceConn.AccountConnClient, b.binanceConn.cache)
	if err := conn.Connect(ctx, cfg.Binance.ApiKey, cfg.Binance.SecretKey, cfg.Binance.AccountType); err != nil {
		return err
	}
	b.binanceConn = conn

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeConnect, messages.Binance, b.binanceConn.AccountConnClient.ID, nil)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.binanceConn.AccountConnClient.Send <- msgB
	return nil
}

func (b *BinanceAdapter) AuthorizeAccount(ctx context.Context, payload messages.AccountConnectAuthorizeTradingAccountPayload) error {
	return nil
}

func (b *BinanceAdapter) GetUserAccounts(ctx context.Context) error {
	return nil
}

func (b *BinanceAdapter) GetTradingSymbols(ctx context.Context, payload messages.AccountConnectSymbolsPayload) error {
	binanceSyms, err := b.binanceConn.GetBinanceTradingSymbols(ctx)
	if err != nil {
		return err
	}
	accconnectsyms := messages.AccountConnectSymbolRes{
		AccountConnectSymbols: binanceSyms,
	}

	accconnectsymsB, err := json.Marshal(accconnectsyms)
	if err != nil {
		log.Printf("Failed to marshal symbol list data: %v", err)
		return err
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeAccountSymbols, messages.Binance, b.binanceConn.AccountConnClient.ID, accconnectsymsB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	b.binanceConn.AccountConnClient.Send <- msgB
	return nil
}

func (b *BinanceAdapter) GetHistoricalTrades(ctx context.Context, payload messages.AccountConnectHistoricalDealsPayload) error {
	return b.binanceConn.GetHistoricalTrades(ctx, payload)
}

func (b *BinanceAdapter) GetTraderInfo(ctx context.Context, payload messages.AccountConnectTraderInfoPayload) error {
	return b.binanceConn.GetTraderInfo(ctx)
}

func (b *BinanceAdapter) GetSymbolTrendBars(ctx context.Context, payload messages.AccountConnectTrendBarsPayload) error {
	trendbars, err := b.binanceConn.GetSymbolTrendBars(ctx, payload)
	if err != nil {
		log.Printf("Failed to retrieve binance ohlc data: %v", err)
		return err
	}
	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeTrendBars, messages.Binance, b.binanceConn.AccountConnClient.ID, trendbars)

	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.binanceConn.AccountConnClient.Send <- msgB
	return nil
}

func (b *BinanceAdapter) GetAccountOrders(ctx context.Context, payload messages.AccountConnectOrderPayload) error {
	return nil
}

// InitializeClientStream will initialize a stream of real time market prices for the specified stream id for a particular symbol
func (b *BinanceAdapter) InitializeClientStream(ctx context.Context, payload messages.AccountConnectStreamPayload) error {
	streamType := payload.StreamType
	symbolId := payload.SymbolId
	if streamType == "" || symbolId == "" {
		return fmt.Errorf("required streamid or symbolid is missing")
	}
	streamId := streamType + "_" + symbolId

	err := b.binanceConn.AccountConnClient.AddStream(ctx, streamId)
	if err != nil {
		return err
	}
	log.Printf("Initialized a new stream with id: %s stream len now: %d", streamId, len(b.binanceConn.AccountConnClient.Streams))
	payloadB, err := json.Marshal(map[string]string{
		"messsage":  "stream initialized",
		"stream_id": streamId,
		"symbol_id": symbolId,
	})
	if err != nil {
		return err
	}
	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeStream, messages.Binance, b.binanceConn.AccountConnClient.ID, payloadB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.binanceConn.AccountConnClient.Send <- msgB
	err = b.binanceConn.startMarketPriceStream(ctx, payload.SymbolId, streamId)
	if err != nil {
		return err
	}
	return nil
}

// StartCandlestickStream starts a real-time candlestick/kline stream for a given symbol and interval
func (b *BinanceConnection) StartCandlestickStream(ctx context.Context, payload messages.BinanceCandlestickStreamPayload, strm chan []byte) error {
	b.wsServeMux.Lock()
	defer b.wsServeMux.Unlock()
	symbol := strings.ToLower(payload.Symbol)
	streamKey := symbol + "_" + payload.Interval
	doneChan := make(chan struct{})
	b.doneChans[streamKey] = doneChan
	wsURL := url.URL{
		Scheme: "wss",
		Host:   "stream.binance.com:443",
		Path:   fmt.Sprintf("/ws/%s@kline_%s", symbol, payload.Interval),
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		delete(b.doneChans, streamKey)
		close(doneChan)
		return fmt.Errorf("failed to connect candlestick WebSocket: %w", err)
	}
	go func() {
		defer conn.Close()
		for {
			msgChan := make(chan []byte)
			errChan := make(chan error)
			go func() {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					fmt.Printf("Error here: %s", err)
					errChan <- err
					return
				}
				msgChan <- msg
			}()
			select {
			case <-ctx.Done():
				log.Printf("Context cancelled for candlestick stream %s", streamKey)
				delete(b.doneChans, streamKey)
				close(doneChan)
				return
			case err := <-errChan:
				log.Printf("Error in candlestick stream %s: %v", streamKey, err)
				delete(b.doneChans, streamKey)
				close(doneChan)
				return
			case msg := <-msgChan:
				var event BinanceKlineEvent
				if err := json.Unmarshal(msg, &event); err != nil {
					log.Printf("Failed to unmarshal kline event for %s: %v", streamKey, err)
					return
				}
				bar := messages.AccountConnectCandlestickBar{
					OpenTime:  event.Kline.StartTime,
					Open:      parseFloat(event.Kline.Open),
					High:      parseFloat(event.Kline.High),
					Low:       parseFloat(event.Kline.Low),
					Close:     parseFloat(event.Kline.Close),
					Volume:    parseFloat(event.Kline.Volume),
					CloseTime: event.Kline.EndTime,
					IsFinal:   event.Kline.IsFinal,
				}
				barRes := messages.AccountConnectCandlestickBarRes{
					Bars:     []messages.AccountConnectCandlestickBar{bar},
					Symbol:   payload.Symbol,
					Interval: payload.Interval,
				}
				barB, err := json.Marshal(barRes)
				if err != nil {
					log.Printf("Failed to marshal candlestick bar: %v", err)
					return
				}
				select {
				case <-ctx.Done():
					return
				case strm <- barB:
				default:
					log.Printf("Stream channel full for %s, dropping update", streamKey)
				}
			}
		}
	}()
	return nil
}

func (b *BinanceAdapter) GetCandlestickStream(ctx context.Context, payload messages.AccountConnectCandlestickStreamPayload) error {
	if payload.Binance == nil {
		return fmt.Errorf("missing binance payload for candlestick stream")
	}
	bp := payload.Binance
	if bp.Symbol == "" || bp.Interval == "" {
		return fmt.Errorf("required symbol or interval is missing")
	}
	streamId := "candlestick_" + strings.ToLower(bp.Symbol) + "_" + bp.Interval
	err := b.binanceConn.AccountConnClient.AddStream(ctx, streamId)
	if err != nil {
		return err
	}
	stream := b.binanceConn.AccountConnClient.Streams[streamId]
	go func() {
		for barB := range stream {
			msg := messageutils.CreateSuccessResponse(ctx, messages.TypeCandlestickStream, messages.Binance, b.binanceConn.AccountConnClient.ID, barB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal candlestick stream message: %v", err)
				continue
			}
			b.binanceConn.AccountConnClient.Send <- msgB
		}
	}()
	return b.binanceConn.StartCandlestickStream(ctx, *bp, stream)
}

func (b *BinanceAdapter) Disconnect(ctx context.Context) error {
	return nil
}

func parseFloat(s string) float64 {
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		log.Printf("Failed to parse float from string %q: %v", s, err)
		return 0
	}
	return val
}

// getNonZeroBaseAssets returns assets with nonzero balances for the current account type.
func (b *BinanceConnection) getNonZeroBaseAssets() ([]string, error) {
	var baseAssets []string

	switch b.accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		if b.account == nil {
			return nil, fmt.Errorf("no cached spot/margin account info")
		}
		for _, bal := range b.account.Balances {
			free, err := strconv.ParseFloat(bal.Free, 64)
			if err != nil {
				continue
			}
			locked, err := strconv.ParseFloat(bal.Locked, 64)
			if err != nil {
				continue
			}
			if free == 0 && locked == 0 {
				continue
			}
			baseAssets = append(baseAssets, bal.Asset)
		}

	case messages.BinanceAccountTypeFutures:
		if b.futuresAccount == nil {
			return nil, fmt.Errorf("no cached futures account info")
		}
		for _, asset := range b.futuresAccount.Assets {
			balance, err := strconv.ParseFloat(asset.WalletBalance, 64)
			if err != nil {
				continue
			}
			if balance == 0 {
				continue
			}
			baseAssets = append(baseAssets, asset.Asset)
		}

	case messages.BinanceAccountTypeDelivery:
		if b.deliveryAccount == nil {
			return nil, fmt.Errorf("no cached delivery account info")
		}
		for _, asset := range b.deliveryAccount.Assets {
			balance, err := strconv.ParseFloat(asset.WalletBalance, 64)
			if err != nil {
				continue
			}
			if balance == 0 {
				continue
			}
			baseAssets = append(baseAssets, asset.Asset)
		}

	default:
		return nil, fmt.Errorf("unsupported account type for historical trades: %s", b.accountType)
	}

	return baseAssets, nil
}

// fetchTradesForSymbol fetches account trades for a single symbol using the correct
// service for the current account type.
func (b *BinanceConnection) fetchTradesForSymbol(ctx context.Context, symbol string, limit int, from, to *int64) ([]messages.BinanceAccountConnectDeal, error) {
	var trades []messages.BinanceAccountConnectDeal

	switch b.accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		fmt.Printf("Fetching trade for symbol: %v", symbol)
		svc := b.spotClient.NewListTradesService().Symbol(symbol).Limit(limit)
		if from != nil {
			svc = svc.StartTime(*from)
		}
		if to != nil {
			svc = svc.EndTime(*to)
		}
		result, err := svc.Do(ctx)
		if err != nil {
			return nil, err
		}
		fmt.Printf("Got results here: %v", len(result))
		for _, t := range result {
			trades = append(trades, mappers.BinanceTradeToAccountConnectDeal(t, symbol))
		}

	case messages.BinanceAccountTypeFutures:
		svc := b.futuresClient.NewListAccountTradeService().Symbol(symbol).Limit(limit)
		if from != nil {
			svc = svc.StartTime(*from)
		}
		if to != nil {
			svc = svc.EndTime(*to)
		}
		result, err := svc.Do(ctx)
		if err != nil {
			return nil, err
		}
		for _, t := range result {
			trades = append(trades, mappers.BinanceFuturesTradeToAccountConnectDeal(t))
		}

	case messages.BinanceAccountTypeDelivery:
		// svc := b.deliveryClient.NewA().Symbol(symbol).Limit(limit)
		// if from != nil {
		// 	svc = svc.StartTime(*from)
		// }
		// if to != nil {
		// 	svc = svc.EndTime(*to)
		// }
		// result, err := svc.Do(ctx)
		// if err != nil {
		// 	return nil, err
		// }
		// for _, t := range result {
		// 	trades = append(trades, mappers.BinanceDeliveryTradeToAccountConnectDeal(t))
		// }

	default:
		return nil, fmt.Errorf("unsupported account type for trade fetch: %s", b.accountType)
	}

	return trades, nil
}
