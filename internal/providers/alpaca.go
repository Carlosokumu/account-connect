package providers

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
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
	alpacaPaperBaseURL = "https://paper-api.alpaca.markets"
	alpacaLiveBaseURL  = "https://api.alpaca.markets"
)

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
}

func NewAlpacaAdapter(accountConnClient *clients.AccountConnectClient, assetProvider assetmeta.Provider) *AlpacaAdapter {
	return &AlpacaAdapter{
		AccountConnClient: accountConnClient,
		assetProvider:     assetProvider,
	}
}

func newAlpacaAccountID() messages.AccountID {
	var b [8]byte
	for {
		_, _ = rand.Read(b[:])
		n := int64(binary.BigEndian.Uint64(b[:]) & math.MaxInt64)
		if n != 0 {
			return messages.AccountID(n)
		}
	}
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
	a.accountID = newAlpacaAccountID()

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
	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeTraderInfo, messages.Alpaca, a.AccountConnClient.ID, infoB)
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

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeTrendBars, messages.Alpaca, a.AccountConnClient.ID, resB)
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

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeAccountSymbols, messages.Alpaca, a.AccountConnClient.ID, accconnectsymsB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	a.AccountConnClient.Send <- msgB
	return nil
}

func (a *AlpacaAdapter) GetCandlestickStream(ctx context.Context, payload messages.AccountConnectCandlestickStreamPayload) error {
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
			msg := messageutils.CreateSuccessResponse(ctx, messages.TypeCandlestickStream, messages.Alpaca, a.AccountConnClient.ID, barB)
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
	return fmt.Errorf("historical ticks not yet supported for alpaca")
}

func (a *AlpacaAdapter) GetTickStream(ctx context.Context, payload messages.AccountConnectTickDataPayload) error {
	return fmt.Errorf("tick stream not yet supported for alpaca")
}

func (a *AlpacaAdapter) GetOrderBookDepth(ctx context.Context, payload messages.AccountConnectDepthPayload) error {
	return fmt.Errorf("order book depth not yet supported for alpaca")
}

func (a *AlpacaAdapter) GetDepthStream(ctx context.Context, payload messages.AccountConnectDepthPayload) error {
	return fmt.Errorf("depth stream not yet supported for alpaca")
}

func (a *AlpacaAdapter) GetBBOStream(ctx context.Context, payload messages.AccountConnectBBOPayload) error {
	return fmt.Errorf("bbo stream not yet supported for alpaca")
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

	a.client = nil
	a.marketClient = nil
	a.account = nil
	a.apiKey = ""
	a.secretKey = ""

	return nil
}
