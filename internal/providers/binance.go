package providers

import (
	"account-connect/assetmeta"
	"account-connect/config"
	messageutils "account-connect/internal/accountconnectmessageutils"
	"account-connect/internal/clients"
	"account-connect/internal/mappers"
	"account-connect/internal/messages"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
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

var validBinanceIntervals = map[string]bool{
	"1m": true, "3m": true, "5m": true, "15m": true, "30m": true,
	"1h": true, "2h": true, "4h": true, "6h": true, "8h": true, "12h": true,
	"1d": true, "3d": true, "1w": true, "1M": true,
}

const binanceAssetClass = "Crypto"

const (
	symbolsCacheTTL            = 24 * time.Hour
	tradesCacheTTL             = 1 * time.Hour
	workerPoolSize             = 10
	workerCallIntervalMs       = 1000
	maxTickLimit               = 1000
	maxTickPages               = 200
	pageDelayMs                = 25
	tickWorkerPoolSize         = 15
	tickChunkDuration          = 1 * time.Minute
	maxFuturesWindowMs   int64 = 2 * 24 * 60 * 60 * 1000 // 2 days in milliseconds
)

// tradeResult carries the result of a single worker's symbol trade fetch.
type tradeResult struct {
	symbol string
	trades []messages.BinanceAccountConnectDeal
	err    error
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

// BinanceAggTradeStreamEvent represents a raw aggTrade WebSocket push from Binance
type BinanceAggTradeStreamEvent struct {
	EventType    string `json:"e"`
	EventTime    int64  `json:"E"`
	Symbol       string `json:"s"`
	AggTradeId   int64  `json:"a"`
	Price        string `json:"p"`
	Quantity     string `json:"q"`
	FirstTradeId int64  `json:"f"`
	LastTradeId  int64  `json:"l"`
	TradeTime    int64  `json:"T"`
	IsBuyerMaker bool   `json:"m"`
}

// BinanceDepthStreamEvent represents a partial book depth push (@depth5/10/20)
type BinanceDepthStreamEvent struct {
	LastUpdateId int64      `json:"lastUpdateId"`
	Bids         [][]string `json:"bids"`
	Asks         [][]string `json:"asks"`
}

type BinanceBookTickerEvent struct {
	UpdateId int64  `json:"u"`
	Symbol   string `json:"s"`
	BidPrice string `json:"b"`
	BidQty   string `json:"B"`
	AskPrice string `json:"a"`
	AskQty   string `json:"A"`
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

	assets map[string]BinanceAssetInfo
}

type BinanceAssetInfo struct {
	Symbol string `json:"symbol"`
	Name   string `json:"name"`
}

func NewBinanceConnection(accountConnClient *clients.AccountConnectClient, cache persistence.AccountConnectCache) *BinanceConnection {
	return &BinanceConnection{
		AccountConnClient: accountConnClient,
		doneChans:         make(map[string]chan struct{}),
		cache:             cache,
	}
}

func newAccountID() messages.AccountID {
	var b [8]byte
	for {
		_, _ = rand.Read(b[:])
		n := int64(binary.BigEndian.Uint64(b[:]) & math.MaxInt64) // mask sign bit, always positive
		if n != 0 {
			return messages.AccountID(n)
		}
	}
}

// account holds everything needed to service subsequent
// requests for a single (apiKey, accountType) pair.
type Naccount struct {
	id          messages.AccountID
	accountType messages.BinanceAccountType

	spotClient     *binance.Client
	futuresClient  *futures.Client
	deliveryClient *delivery.Client

	info any
}

// AccountRegistry owns the lifetime of allocated account IDs.
// One registry per BinanceAdapter (i.e. per WS connection).
type AccountRegistry struct {
	mu       sync.RWMutex
	accounts map[messages.AccountID]*Naccount
}

func NewAccountRegistry() *AccountRegistry {
	return &AccountRegistry{
		accounts: make(map[messages.AccountID]*Naccount),
	}
}

func (r *AccountRegistry) put(a *Naccount) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts[a.id] = a
}

func (r *AccountRegistry) Get(id messages.AccountID) (*Naccount, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.accounts[id]
	if !ok {
		return nil, fmt.Errorf("unknown or expired account id: %s", id)
	}
	return a, nil
}

// Drop removes an account.
func (r *AccountRegistry) Drop(id messages.AccountID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.accounts, id)
}

// All returns a snapshot of every currently registered account.
func (r *AccountRegistry) All() []*Naccount {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Naccount, 0, len(r.accounts))
	for _, a := range r.accounts {
		out = append(out, a)
	}
	return out
}

// ConnectResult reports, per Binance account type, either an allocated
// account ID or the error encountered authenticating against it.
type ConnectResult struct {
	Accounts map[messages.BinanceAccountType]messages.AccountID
	Errors   map[messages.BinanceAccountType]error
}

func (b *BinanceConnection) Connect(ctx context.Context, registry *AccountRegistry, apiKey, secretKey string) (*ConnectResult, error) {
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	if apiKey == "" || secretKey == "" {
		return nil, fmt.Errorf("binance api key and secret key are required")
	}

	binance.UseDemo = true
	futures.UseDemo = true
	delivery.UseDemo = true

	result := &ConnectResult{
		Accounts: make(map[messages.BinanceAccountType]messages.AccountID),
		Errors:   make(map[messages.BinanceAccountType]error),
	}

	record := func(t messages.BinanceAccountType, id messages.AccountID, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			result.Errors[t] = err
			return
		}
		result.Accounts[t] = id
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		spotClient := binance.NewClient(apiKey, secretKey)

		spotAcct, err := spotClient.NewGetAccountService().Do(ctx)
		if err != nil {
			record(messages.BinanceAccountTypeSpot, 0, fmt.Errorf("failed to authenticate binance spot account: %w", err))
		} else {
			id := newAccountID()
			registry.put(&Naccount{
				id:          id,
				accountType: messages.BinanceAccountTypeSpot,
				spotClient:  spotClient,
				info:        spotAcct,
			})
			record(messages.BinanceAccountTypeSpot, id, nil)
		}

		// Margin reuses the spot client, but gets its own account ID.
		marginAcct, err := spotClient.NewGetMarginAccountService().Do(ctx)
		if err != nil {
			record(messages.BinanceAccountTypeMargin, 0, fmt.Errorf("failed to authenticate binance margin account: %w", err))
		} else {
			id := newAccountID()
			registry.put(&Naccount{
				id:          id,
				accountType: messages.BinanceAccountTypeMargin,
				spotClient:  spotClient,
				info:        marginAcct,
			})
			record(messages.BinanceAccountTypeMargin, id, nil)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		client := futures.NewClient(apiKey, secretKey)
		futuresAcct, err := client.NewGetAccountService().Do(ctx)
		if err != nil {
			record(messages.BinanceAccountTypeFutures, 0, fmt.Errorf("failed to authenticate binance futures account: %w", err))
			return
		}
		id := newAccountID()
		registry.put(&Naccount{
			id:            id,
			accountType:   messages.BinanceAccountTypeFutures,
			futuresClient: client,
			info:          futuresAcct,
		})
		record(messages.BinanceAccountTypeFutures, id, nil)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		client := delivery.NewClient(apiKey, secretKey)
		deliveryAcct, err := client.NewGetAccountService().Do(ctx)
		if err != nil {
			record(messages.BinanceAccountTypeDelivery, 0, fmt.Errorf("failed to authenticate binance delivery account: %w", err))
			return
		}
		id := newAccountID()
		registry.put(&Naccount{
			id:             id,
			accountType:    messages.BinanceAccountTypeDelivery,
			deliveryClient: client,
			info:           deliveryAcct,
		})
		record(messages.BinanceAccountTypeDelivery, id, nil)
	}()

	wg.Wait()

	if len(result.Accounts) == 0 {
		return result, fmt.Errorf("no binance account types could be authenticated")
	}

	return result, nil
}

func (b *BinanceConnection) GetAccountOrders(ctx context.Context, accountConnectPayload messages.AccountConnectOrderPayload) error {
	return nil
}

func (b *BinanceConnection) GetHistoricalTrades(ctx context.Context, payload messages.AccountConnectHistoricalDealsPayload) error {
	return nil
}

// fetchAllFuturesAggTrades handles both the 2-day search window restriction
// by chunking time windows AND inner page pagination via fromId.
func (b *BinanceConnection) fetchAllFuturesAggTrades(
	ctx context.Context,
	client *futures.Client,
	symbolName string,
	from, to *int64,
	limit int,
) ([]*futures.AggTrade, error) {
	var all []*futures.AggTrade
	var fromId *int64

	for page := 0; page < maxTickPages; page++ {
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		default:
		}

		svc := client.NewAggTradesService().Symbol(symbolName).Limit(limit)

		if fromId != nil {
			// Pure FromID pagination bypasses the 2-day restriction completely
			svc = svc.FromID(*fromId)
		} else if from != nil {
			// First call: pass ONLY StartTime to locate the initial trade ID
			svc = svc.StartTime(*from)
		}

		batch, err := svc.Do(ctx)
		if err != nil {
			return all, fmt.Errorf("failed to fetch futures agg trades page %d: %w", page, err)
		}

		if len(batch) == 0 {
			break
		}

		// Client-side enforcement of to_timestamp (EndTime)
		if to != nil {
			cutoff := len(batch)
			hitCutoff := false
			for i, t := range batch {
				if t.Timestamp > *to {
					cutoff = i
					hitCutoff = true
					break
				}
			}
			batch = batch[:cutoff]
			all = append(all, batch...)
			if hitCutoff || len(batch) == 0 {
				break
			}
		} else {
			all = append(all, batch...)
		}

		if len(batch) < limit {
			break // Reached end of available trade history
		}

		// Increment last aggregate trade ID for the next page
		last := batch[len(batch)-1]
		next := last.AggTradeID + 1
		fromId = &next

		time.Sleep(time.Duration(pageDelayMs) * time.Millisecond)
	}

	return all, nil
}

// fetchAllSpotAggTrades pages through Binance Spot aggTrades using fromId continuation
func (b *BinanceConnection) fetchAllSpotAggTrades(ctx context.Context, client *binance.Client, symbolName string, from, to *int64, limit int) ([]*binance.AggTrade, error) {
	var all []*binance.AggTrade
	var fromId *int64

	for page := 0; page < maxTickPages; page++ {
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		default:
		}

		svc := client.NewAggTradesService().Symbol(symbolName).Limit(limit)
		if fromId != nil {
			svc = svc.FromID(*fromId)
		} else {
			if from != nil {
				svc = svc.StartTime(*from)
			}
			if to != nil {
				svc = svc.EndTime(*to)
			}
		}

		batch, err := svc.Do(ctx)
		if err != nil {
			return all, fmt.Errorf("failed to fetch agg trades page %d: %w", page, err)
		}

		if len(batch) == 0 {
			break
		}

		// once paginating by fromId, trim anything past the originally-requested
		// upper bound — this is the actual stopping condition for a bounded range
		if fromId != nil && to != nil {
			cutoff := len(batch)
			hitCutoff := false
			for i, t := range batch {
				if t.Timestamp > *to {
					cutoff = i
					hitCutoff = true
					break
				}
			}
			batch = batch[:cutoff]
			all = append(all, batch...)
			if hitCutoff || len(batch) == 0 {
				break
			}
		} else {
			all = append(all, batch...)
		}

		if len(batch) < limit {
			break // fewer than a full page — genuinely no more data
		}

		last := all[len(all)-1]
		next := last.AggTradeID + 1
		fromId = &next

		time.Sleep(time.Duration(pageDelayMs) * time.Millisecond)
	}

	return all, nil
}

// StartDepthStream starts a real-time partial-book-depth stream for the specified symbolName.
func (b *BinanceConnection) StartDepthStream(ctx context.Context, accountType messages.BinanceAccountType, symbolName string, limit int, strm chan []byte) error {
	host, err := depthStreamHost(accountType)
	if err != nil {
		return err
	}

	symbol := strings.ToLower(symbolName)
	streamKey := fmt.Sprintf("depth_%s_%d", symbol, limit)

	b.wsServeMux.Lock()
	doneChan := make(chan struct{})
	b.doneChans[streamKey] = doneChan
	b.wsServeMux.Unlock()

	wsURL := url.URL{
		Scheme: "wss",
		Host:   host,
		Path:   fmt.Sprintf("/ws/%s@depth%d", symbol, limit),
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		b.wsServeMux.Lock()
		delete(b.doneChans, streamKey)
		b.wsServeMux.Unlock()
		close(doneChan)
		return fmt.Errorf("failed to connect depth stream WebSocket: %w", err)
	}

	go func() {
		defer conn.Close()
		cleanup := func() {
			b.wsServeMux.Lock()
			delete(b.doneChans, streamKey)
			b.wsServeMux.Unlock()
			close(doneChan)
		}

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
			case <-ctx.Done():
				log.Printf("Context cancelled for depth stream %s", streamKey)
				cleanup()
				return
			case err := <-errChan:
				log.Printf("Error in depth stream %s: %v", streamKey, err)
				cleanup()
				return
			case msg := <-msgChan:
				var event BinanceDepthStreamEvent
				if err := json.Unmarshal(msg, &event); err != nil {
					log.Printf("Failed to unmarshal depth event for %s, skipping frame: %v", streamKey, err)
					continue
				}
				depth := messages.AccountConnectDepthRes{
					Bids:         parseDepthLevels(event.Bids),
					Asks:         parseDepthLevels(event.Asks),
					LastUpdateId: event.LastUpdateId,
					SymbolName:   symbolName,
				}
				depthB, err := json.Marshal(depth)
				if err != nil {
					log.Printf("Failed to marshal depth for %s, skipping frame: %v", streamKey, err)
					continue
				}
				select {
				case <-ctx.Done():
					cleanup()
					return
				case strm <- depthB:
				default:
					log.Printf("Depth stream channel full for %s, dropping update", streamKey)
				}
			}
		}
	}()
	return nil
}

func (b *BinanceConnection) StartBBOStream(ctx context.Context, accountType messages.BinanceAccountType, symbolName string, strm chan []byte) error {
	var path string
	var streamKey string
	if symbolName == "" {
		path = "/ws/!bookTicker"
		streamKey = "bbo_all"
	} else {
		symbol := strings.ToLower(symbolName)
		path = fmt.Sprintf("/ws/%s@bookTicker", symbol)
		streamKey = "bbo_" + symbol
	}

	host, err := bboStreamHost(accountType)
	if err != nil {
		return err
	}

	b.wsServeMux.Lock()
	doneChan := make(chan struct{})
	b.doneChans[streamKey] = doneChan
	b.wsServeMux.Unlock()

	wsURL := url.URL{Scheme: "wss", Host: host, Path: path}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		b.wsServeMux.Lock()
		delete(b.doneChans, streamKey)
		b.wsServeMux.Unlock()
		close(doneChan)
		return fmt.Errorf("failed to connect BBO stream WebSocket: %w", err)
	}

	go func() {
		defer conn.Close()
		cleanup := func() {
			b.wsServeMux.Lock()
			delete(b.doneChans, streamKey)
			b.wsServeMux.Unlock()
			close(doneChan)
		}

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
			case <-ctx.Done():
				log.Printf("Context cancelled for BBO stream %s", streamKey)
				cleanup()
				return
			case err := <-errChan:
				log.Printf("Error in BBO stream %s: %v", streamKey, err)
				cleanup()
				return
			case msg := <-msgChan:
				var event BinanceBookTickerEvent
				if err := json.Unmarshal(msg, &event); err != nil {
					log.Printf("Failed to unmarshal BBO event for %s, skipping frame: %v", streamKey, err)
					continue
				}
				bbo := messages.AccountConnectBBO{
					SymbolName: event.Symbol,
					BidPrice:   parseFloat(event.BidPrice),
					BidQty:     parseFloat(event.BidQty),
					AskPrice:   parseFloat(event.AskPrice),
					AskQty:     parseFloat(event.AskQty),
					UpdateId:   event.UpdateId,
				}
				bboB, err := json.Marshal(bbo)
				if err != nil {
					log.Printf("Failed to marshal BBO for %s, skipping frame: %v", streamKey, err)
					continue
				}
				select {
				case <-ctx.Done():
					cleanup()
					return
				case strm <- bboB:
				default:
					log.Printf("BBO stream channel full for %s, dropping update", streamKey)
				}
			}
		}
	}()
	return nil
}

// StartTickStream starts a real-time aggregate-trade stream for a symbol.
func (b *BinanceConnection) StartTickStream(ctx context.Context, symbolName string, strm chan []byte) error {
	symbol := strings.ToLower(symbolName)
	streamKey := "ticks_" + symbol

	b.wsServeMux.Lock()
	doneChan := make(chan struct{})
	b.doneChans[streamKey] = doneChan
	b.wsServeMux.Unlock()

	wsURL := url.URL{
		Scheme: "wss",
		Host:   "stream.binance.com:443",
		Path:   fmt.Sprintf("/ws/%s@aggTrade", symbol),
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		b.wsServeMux.Lock()
		delete(b.doneChans, streamKey)
		b.wsServeMux.Unlock()
		close(doneChan)
		return fmt.Errorf("failed to connect tick stream WebSocket: %w", err)
	}

	go func() {
		defer conn.Close()

		cleanup := func() {
			b.wsServeMux.Lock()
			delete(b.doneChans, streamKey)
			b.wsServeMux.Unlock()
			close(doneChan)
		}

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
			case <-ctx.Done():
				log.Printf("Context cancelled for tick stream %s", streamKey)
				cleanup()
				return
			case err := <-errChan:
				log.Printf("Error in tick stream %s: %v", streamKey, err)
				cleanup()
				return
			case msg := <-msgChan:
				var event BinanceAggTradeStreamEvent
				if err := json.Unmarshal(msg, &event); err != nil {
					log.Printf("Failed to unmarshal aggTrade event for %s, skipping frame: %v", streamKey, err)
					continue
				}
				tick := messages.AccountConnectTickStreamMsg{
					SymbolName: event.Symbol,
					Price:      parseFloat(event.Price),
					Quantity:   parseFloat(event.Quantity),
					Timestamp:  event.TradeTime,
				}
				tickB, err := json.Marshal(tick)
				if err != nil {
					log.Printf("Failed to marshal tick for %s, skipping frame: %v", streamKey, err)
					continue
				}
				select {
				case <-ctx.Done():
					cleanup()
					return
				case strm <- tickB:
				default:
					log.Printf("Tick stream channel full for %s, dropping update", streamKey)
				}
			}
		}
	}()
	return nil
}

func bboStreamHost(accountType messages.BinanceAccountType) (string, error) {
	switch accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		return "stream.binance.com:443", nil
	case messages.BinanceAccountTypeFutures:
		return "fstream.binance.com", nil
	case messages.BinanceAccountTypeDelivery:
		return "dstream.binance.com", nil
	default:
		return "", fmt.Errorf("unsupported account type for BBO stream: %s", accountType)
	}
}

func depthStreamHost(accountType messages.BinanceAccountType) (string, error) {
	switch accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		return "stream.binance.com:443", nil
	case messages.BinanceAccountTypeFutures:
		return "fstream.binance.com", nil
	case messages.BinanceAccountTypeDelivery:
		return "dstream.binance.com", nil
	default:
		return "", fmt.Errorf("unsupported account type for depth stream: %s", accountType)
	}
}

type BinanceAdapter struct {
	AccountConnClient *clients.AccountConnectClient
	assetProvider     assetmeta.Provider
	registry          *AccountRegistry
	binanceConn       *BinanceConnection
	wsServeMux        sync.Mutex
	doneChans         map[string]chan struct{}
}

func (b *BinanceAdapter) EstablishConnection(ctx context.Context, cfg config.PlatformConfigs) error {
	registry := NewAccountRegistry()

	result, err := b.binanceConn.Connect(ctx, registry, cfg.Binance.ApiKey, cfg.Binance.SecretKey)
	if err != nil {
		return err
	}
	b.registry = registry

	info, err := b.buildAccountInfo(result)
	if err != nil {
		return fmt.Errorf("connected but failed to build account info: %w", err)
	}

	res := messages.AccountConnectAccountInfoRes{
		Binance: &info,
	}
	resB, err := json.Marshal(res)
	if err != nil {
		return err
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeConnect, messages.Binance, b.AccountConnClient.ID, resB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.AccountConnClient.Send <- msgB
	return nil
}

// buildAccountInfo constructs the unified account-info shape from every
// account type that successfully connected, using the registry entries
// allocated by Connect.
func (b *BinanceAdapter) buildAccountInfo(result *ConnectResult) (messages.AccountConnectBinanceAccountInfo, error) {
	info := messages.AccountConnectBinanceAccountInfo{
		Accounts: make(map[messages.BinanceAccountType]messages.AccountConnectBinanceAccount),
		Errors:   make(map[messages.BinanceAccountType]string),
	}

	for accountType, connID := range result.Accounts {
		conn, err := b.registry.Get(connID)
		if err != nil {
			return info, err
		}

		entry := messages.AccountConnectBinanceAccount{
			AccountID: int64(conn.id),
		}

		switch accountType {
		case messages.BinanceAccountTypeSpot:
			acct, ok := conn.info.(*binance.Account)
			if !ok || acct == nil {
				return info, fmt.Errorf("no cached spot account info")
			}
			entry.Spot = buildSpotInfo(acct)

		case messages.BinanceAccountTypeMargin:
			acct, ok := conn.info.(*binance.MarginAccount)
			if !ok || acct == nil {
				return info, fmt.Errorf("no cached margin account info")
			}
			entry.Margin = buildMarginInfo(acct)

		case messages.BinanceAccountTypeFutures:
			acct, ok := conn.info.(*futures.Account)
			if !ok || acct == nil {
				return info, fmt.Errorf("no cached futures account info")
			}
			entry.Futures = buildFuturesInfo(acct)

		case messages.BinanceAccountTypeDelivery:
			acct, ok := conn.info.(*delivery.Account)
			if !ok || acct == nil {
				return info, fmt.Errorf("no cached delivery account info")
			}
			entry.Delivery = buildDeliveryInfo(acct)

		default:
			return info, fmt.Errorf("unsupported account type for account info: %s", accountType)
		}

		info.Accounts[accountType] = entry
	}

	for accountType, err := range result.Errors {
		info.Errors[accountType] = err.Error()
	}

	return info, nil
}

func buildSpotInfo(acct *binance.Account) *messages.AccountConnectBinanceSpotInfo {
	var balances []messages.AccountConnectBinanceBalance
	for _, bal := range acct.Balances {
		free, err := strconv.ParseFloat(bal.Free, 64)
		if err != nil {
			log.Printf("failed to parse free balance for %s: %v", bal.Asset, err)
			continue
		}
		locked, err := strconv.ParseFloat(bal.Locked, 64)
		if err != nil {
			log.Printf("failed to parse locked balance for %s: %v", bal.Asset, err)
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
	return &messages.AccountConnectBinanceSpotInfo{
		CanTrade:        acct.CanTrade,
		CanWithdraw:     acct.CanWithdraw,
		CanDeposit:      acct.CanDeposit,
		MakerCommission: acct.MakerCommission,
		TakerCommission: acct.TakerCommission,
		Balances:        balances,
	}
}

func buildMarginInfo(acct *binance.MarginAccount) *messages.AccountConnectBinanceMarginInfo {
	var assets []messages.AccountConnectBinanceMarginAsset
	for _, a := range acct.UserAssets {
		free, err := strconv.ParseFloat(a.Free, 64)
		if err != nil {
			log.Printf("failed to parse free margin balance for %s: %v", a.Asset, err)
			continue
		}
		locked, err := strconv.ParseFloat(a.Locked, 64)
		if err != nil {
			log.Printf("failed to parse locked margin balance for %s: %v", a.Asset, err)
			continue
		}
		borrowed, _ := strconv.ParseFloat(a.Borrowed, 64) // borrowed/interest can legitimately be "0", no need to skip on parse-zero
		if free == 0 && locked == 0 && borrowed == 0 {
			continue
		}
		assets = append(assets, messages.AccountConnectBinanceMarginAsset{
			Asset:    a.Asset,
			Free:     a.Free,
			Locked:   a.Locked,
			Borrowed: a.Borrowed,
			Interest: a.Interest,
			NetAsset: a.NetAsset,
		})
	}
	return &messages.AccountConnectBinanceMarginInfo{
		BorrowEnabled:     acct.BorrowEnabled,
		TradeEnabled:      acct.TradeEnabled,
		TransferEnabled:   acct.TransferOutEnabled,
		MarginLevel:       acct.MarginLevel,
		TotalAssetBTC:     acct.TotalAssetOfBTC,
		TotalLiabilityBTC: acct.TotalLiabilityOfBTC,
		TotalNetAssetBTC:  acct.TotalNetAssetOfBTC,
		Assets:            assets,
	}
}

func buildFuturesInfo(acct *futures.Account) *messages.AccountConnectBinanceFuturesInfo {
	var assets []messages.AccountConnectBinanceFuturesAsset
	for _, a := range acct.Assets {
		balance, err := strconv.ParseFloat(a.WalletBalance, 64)
		if err != nil {
			log.Printf("failed to parse wallet balance for %s: %v", a.Asset, err)
			continue
		}
		if balance == 0 {
			continue
		}
		assets = append(assets, messages.AccountConnectBinanceFuturesAsset{
			Asset:            a.Asset,
			WalletBalance:    a.WalletBalance,
			UnrealizedProfit: a.UnrealizedProfit,
			MarginBalance:    a.MarginBalance,
			AvailableBalance: a.AvailableBalance,
		})
	}
	var positions []messages.AccountConnectBinanceFuturesPosition
	for _, p := range acct.Positions {
		amt, err := strconv.ParseFloat(p.PositionAmt, 64)
		if err != nil {
			log.Printf("failed to parse position amount for %s: %v", p.Symbol, err)
			continue
		}
		if amt == 0 {
			continue
		}
		positions = append(positions, messages.AccountConnectBinanceFuturesPosition{
			Symbol:           p.Symbol,
			PositionAmt:      p.PositionAmt,
			EntryPrice:       p.EntryPrice,
			UnrealizedProfit: p.UnrealizedProfit,
			PositionSide:     string(p.PositionSide),
		})
	}
	return &messages.AccountConnectBinanceFuturesInfo{
		CanTrade:              acct.CanTrade,
		TotalWalletBalance:    acct.TotalWalletBalance,
		TotalUnrealizedProfit: acct.TotalUnrealizedProfit,
		TotalMarginBalance:    acct.TotalMarginBalance,
		// Assets:                acct.Assets,
		// Positions:             acct.Positions,
	}
}

func buildDeliveryInfo(acct *delivery.Account) *messages.AccountConnectBinanceDeliveryInfo {
	var assets []messages.AccountConnectBinanceDeliveryAsset
	for _, a := range acct.Assets {
		balance, err := strconv.ParseFloat(a.WalletBalance, 64)
		if err != nil {
			log.Printf("failed to parse delivery wallet balance for %s: %v", a.Asset, err)
			continue
		}
		if balance == 0 {
			continue
		}
		assets = append(assets, messages.AccountConnectBinanceDeliveryAsset{
			Asset:            a.Asset,
			WalletBalance:    a.WalletBalance,
			AvailableBalance: a.AvailableBalance,
		})
	}
	return &messages.AccountConnectBinanceDeliveryInfo{
		CanTrade: acct.CanTrade,
		// Assets:   assets,
	}
}

func NewBinanceAdapter(accountConnClient *clients.AccountConnectClient, cache persistence.AccountConnectCache, assetProvider assetmeta.Provider) *BinanceAdapter {
	return &BinanceAdapter{
		binanceConn:       NewBinanceConnection(accountConnClient, cache),
		AccountConnClient: accountConnClient,
		assetProvider:     assetProvider,
		doneChans:         make(map[string]chan struct{}),
	}
}

func (b *BinanceAdapter) AuthorizeAccount(ctx context.Context, payload messages.AccountConnectAuthorizeTradingAccountPayload) error {
	return nil
}

func (b *BinanceAdapter) GetUserAccounts(ctx context.Context) error {
	return nil
}

func (b *BinanceAdapter) GetTradingSymbols(ctx context.Context, payload messages.AccountConnectSymbolsPayload) error {
	binanceSyms, err := b.GetBinanceTradingSymbols(ctx, payload.AccountID)
	if err != nil {
		return err
	}

	accconnectsyms := messages.AccountConnectSymbolRes{
		AccountConnectSymbols: binanceSyms,
	}

	accconnectsymsB, err := json.Marshal(accconnectsyms)
	if err != nil {
		log.Printf("failed to marshal symbol list data: %v", err)
		return err
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeAccountSymbols, messages.Binance, b.AccountConnClient.ID, accconnectsymsB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	b.AccountConnClient.Send <- msgB
	return nil
}

func (b *BinanceAdapter) GetHistoricalTrades(ctx context.Context, payload messages.AccountConnectHistoricalDealsPayload) error {
	return b.binanceConn.GetHistoricalTrades(ctx, payload)
}

// GetHistoricalTicks fetches historical aggregate trades for a symbol.
func (b *BinanceAdapter) GetHistoricalTicks(ctx context.Context, payload messages.AccountConnectTickDataPayload) error {
	if payload.Binance == nil {
		return fmt.Errorf("binance payload is required for historical tick data")
	}
	bp := payload.Binance
	if bp.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for historical tick data")
	}

	acct, err := b.registry.Get(bp.AccountID)
	if err != nil {
		return err
	}

	limit := 500
	if bp.Limit != nil {
		limit = *bp.Limit
		if limit > maxTickLimit {
			limit = maxTickLimit
		}
	}

	go func() {
		var ticks []messages.AccountConnectTick

		switch acct.accountType {
		case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
			if acct.spotClient == nil {
				log.Printf("no spot client for account %d", acct.id)
				return
			}
			trades, err := b.binanceConn.fetchAllSpotAggTrades(ctx, acct.spotClient, bp.SymbolName, bp.FromTimestamp, bp.ToTimestamp, limit)
			if err != nil {
				log.Printf("failed to fetch spot agg trades: %v", err)
				return
			}
			ticks = mappers.BinanceAggTradesToTicks(trades)

		case messages.BinanceAccountTypeFutures:
			if acct.futuresClient == nil {
				log.Printf("no futures client for account %d", acct.id)
				return
			}
			svc := acct.futuresClient.NewAggTradesService().Symbol(bp.SymbolName).Limit(limit)
			if bp.FromTimestamp != nil {
				svc = svc.StartTime(*bp.FromTimestamp)
			}
			if bp.ToTimestamp != nil {
				svc = svc.EndTime(*bp.ToTimestamp)
			}

			trades, err := b.binanceConn.fetchAllFuturesAggTrades(ctx, acct.futuresClient, bp.SymbolName, bp.FromTimestamp, bp.ToTimestamp, limit)
			// trades, err := svc.Do(ctx)
			if err != nil {
				log.Printf("failed to fetch futures agg trades: %v", err)
				return
			}
			ticks = mappers.BinanceFuturesAggTradesToTicks(trades)

		case messages.BinanceAccountTypeDelivery:
			log.Printf("historical tick data not yet supported for delivery accounts")
			return

		default:
			log.Printf("unsupported account type for historical tick data: %s", acct.accountType)
			return
		}

		res := messages.AccountConnectTickDataRes{
			Ticks:      ticks,
			HasMore:    len(ticks) == limit,
			SymbolName: bp.SymbolName,
		}
		resB, err := json.Marshal(res)
		if err != nil {
			log.Printf("failed to marshal tick data response: %v", err)
			return
		}

		msg := messageutils.CreateSuccessResponse(ctx, messages.TypeHistoricalTicks, messages.Binance, b.AccountConnClient.ID, resB)
		msgB, err := json.Marshal(msg)
		if err != nil {
			log.Printf("failed to marshal final message: %v", err)
			return
		}
		b.AccountConnClient.Send <- msgB
	}()

	return nil
}

func (b *BinanceAdapter) GetAccountInfo(ctx context.Context, payload messages.AccountConnectAccountInfoPayload) error {
	var info messages.AccountConnectBinanceAccountInfo
	var err error

	if payload.AccountID != nil {
		info, err = b.buildAccountInfoForID(*payload.AccountID)
	} else {
		info, err = b.buildAccountInfoFromRegistry()
	}
	if err != nil {
		return err
	}

	infoB, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal binance account info: %w", err)
	}
	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeTraderInfo, messages.Binance, b.AccountConnClient.ID, infoB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.AccountConnClient.Send <- msgB
	return nil
}

// buildAccountInfoFromRegistry builds info for every currently connected account.
func (b *BinanceAdapter) buildAccountInfoFromRegistry() (messages.AccountConnectBinanceAccountInfo, error) {
	info := messages.AccountConnectBinanceAccountInfo{
		Accounts: make(map[messages.BinanceAccountType]messages.AccountConnectBinanceAccount),
	}
	for _, acct := range b.registry.All() {
		entry, err := buildAccountEntry(acct)
		if err != nil {
			return info, err
		}
		info.Accounts[acct.accountType] = entry
	}
	return info, nil
}

// buildAccountInfoForID builds info for a single account, identified by id.
func (b *BinanceAdapter) buildAccountInfoForID(id messages.AccountID) (messages.AccountConnectBinanceAccountInfo, error) {
	acct, err := b.registry.Get(id)
	if err != nil {
		return messages.AccountConnectBinanceAccountInfo{}, err
	}
	entry, err := buildAccountEntry(acct)
	if err != nil {
		return messages.AccountConnectBinanceAccountInfo{}, err
	}
	return messages.AccountConnectBinanceAccountInfo{
		Accounts: map[messages.BinanceAccountType]messages.AccountConnectBinanceAccount{
			acct.accountType: entry,
		},
	}, nil
}

// buildAccountEntry converts a single registry account into its response shape.
func buildAccountEntry(acct *Naccount) (messages.AccountConnectBinanceAccount, error) {
	entry := messages.AccountConnectBinanceAccount{AccountID: int64(acct.id)}

	switch acct.accountType {
	case messages.BinanceAccountTypeSpot:
		entry.Spot = buildSpotInfo(acct.info.(*binance.Account))
	case messages.BinanceAccountTypeMargin:
		entry.Margin = buildMarginInfo(acct.info.(*binance.MarginAccount))
	case messages.BinanceAccountTypeFutures:
		entry.Futures = buildFuturesInfo(acct.info.(*futures.Account))
	case messages.BinanceAccountTypeDelivery:
		entry.Delivery = buildDeliveryInfo(acct.info.(*delivery.Account))
	default:
		return entry, fmt.Errorf("unsupported account type for account info: %s", acct.accountType)
	}

	return entry, nil
}

func (b *BinanceAdapter) GetSymbolTrendBars(ctx context.Context, trendbarsArgs messages.AccountConnectTrendBarsPayload) error {
	if trendbarsArgs.SymbolName == "" || trendbarsArgs.Period == "" {
		return fmt.Errorf("symbol name and period are required for trend bars")
	}
	if trendbarsArgs.FromTimestamp == nil || trendbarsArgs.ToTimestamp == nil {
		return fmt.Errorf("from and to timestamps are required for trend bars")
	}

	acct, err := b.registry.Get(trendbarsArgs.AccountID)
	if err != nil {
		return err
	}

	var ohlc []*binance.Kline

	switch acct.accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		if acct.spotClient == nil {
			return fmt.Errorf("no spot client for account %d", acct.id)
		}
		ohlc, err = acct.spotClient.NewKlinesService().
			Symbol(trendbarsArgs.SymbolName).
			Interval(trendbarsArgs.Period).
			StartTime(*trendbarsArgs.FromTimestamp).
			EndTime(*trendbarsArgs.ToTimestamp).
			Do(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch klines: %w", err)
		}

	case messages.BinanceAccountTypeFutures:
		if acct.futuresClient == nil {
			return fmt.Errorf("no futures client for account %d", acct.id)
		}
		futuresOhlc, err := acct.futuresClient.NewKlinesService().
			Symbol(trendbarsArgs.SymbolName).
			Interval(trendbarsArgs.Period).
			StartTime(*trendbarsArgs.FromTimestamp).
			EndTime(*trendbarsArgs.ToTimestamp).
			Do(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch futures klines: %w", err)
		}
		ohlc = mappers.FuturesKlinesToBinanceKlines(futuresOhlc)

	case messages.BinanceAccountTypeDelivery:
		if acct.deliveryClient == nil {
			return fmt.Errorf("no delivery client for account %d", acct.id)
		}
		deliveryOhlc, err := acct.deliveryClient.NewKlinesService().
			Symbol(trendbarsArgs.SymbolName).
			Interval(trendbarsArgs.Period).
			StartTime(*trendbarsArgs.FromTimestamp).
			EndTime(*trendbarsArgs.ToTimestamp).
			Do(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch delivery klines: %w", err)
		}
		ohlc = mappers.DeliveryKlinesToBinanceKlines(deliveryOhlc)

	default:
		return fmt.Errorf("unsupported account type for trend bars: %s", acct.accountType)
	}

	acctrendbars, err := mappers.BinanceKlineDataToAccountConnectTrendBar(ohlc)
	if err != nil {
		return err
	}

	acctrendbarsRes := messages.AccountConnectTrendBarRes{
		Trendbars: acctrendbars,
		Symbol:    trendbarsArgs.SymbolName,
		Period:    trendbarsArgs.Period,
	}
	acctrendbarsB, err := json.Marshal(acctrendbarsRes)
	if err != nil {
		return fmt.Errorf("failed to marshal trend bars: %w", err)
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeTrendBars, messages.Binance, b.AccountConnClient.ID, acctrendbarsB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	b.AccountConnClient.Send <- msgB
	return nil
}

// GetBinanceTradingSymbols retrieves tradable symbols for the specified account.
func (b *BinanceAdapter) GetBinanceTradingSymbols(ctx context.Context, id messages.AccountID) ([]messages.AccountConnectSymbol, error) {
	acct, err := b.registry.Get(id)
	if err != nil {
		return nil, err
	}

	var syms []messages.AccountConnectSymbol

	switch acct.accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		if acct.spotClient == nil {
			return nil, fmt.Errorf("no spot client for account %d", id)
		}
		exchangeInfo, err := acct.spotClient.NewExchangeInfoService().Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve binance spot trading symbols: %w", err)
		}
		syms = mappers.BinanceSymbolToAccountConnectSymbol(exchangeInfo.Symbols)

	case messages.BinanceAccountTypeFutures:
		if acct.futuresClient == nil {
			return nil, fmt.Errorf("no futures client for account %d", id)
		}
		exchangeInfo, err := acct.futuresClient.NewExchangeInfoService().Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve binance futures trading symbols: %w", err)
		}
		syms = mappers.FuturesSymbolToAccountConnectSymbol(exchangeInfo.Symbols)

	case messages.BinanceAccountTypeDelivery:
		if acct.deliveryClient == nil {
			return nil, fmt.Errorf("no delivery client for account %d", id)
		}
		exchangeInfo, err := acct.deliveryClient.NewExchangeInfoService().Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve binance delivery trading symbols: %w", err)
		}
		syms = mappers.DeliverySymbolToAccountConnectSymbol(exchangeInfo.Symbols)

	default:
		return nil, fmt.Errorf("unsupported account type for trading symbols: %s", acct.accountType)
	}

	b.enrichSymbolsWithAssetInfo(syms)

	return syms, nil
}

// enrichSymbolsWithAssetInfo attaches full asset names (e.g. "Bitcoin" for
// "BTC") from the shared asset metadata provider.
func (b *BinanceAdapter) enrichSymbolsWithAssetInfo(syms []messages.AccountConnectSymbol) {
	for i := range syms {
		if syms[i].Binance == nil {
			continue
		}
		bs := syms[i].Binance

		bs.AssetClass = binanceAssetClass

		if info, ok := b.assetProvider.GetAssetInfo(bs.BaseAsset); ok {
			bs.BaseAssetFullName = info.FullName
		}
		if info, ok := b.assetProvider.GetAssetInfo(bs.QuoteAsset); ok {
			bs.QuoteAssetFullName = info.FullName
		}

		switch {
		case bs.BaseAssetFullName != "" && bs.QuoteAssetFullName != "":
			syms[i].SymbolName = fmt.Sprintf("%s / %s", bs.BaseAssetFullName, bs.QuoteAssetFullName)
		default:
			syms[i].SymbolName = syms[i].SymbolAbbreviation
		}
	}
}

// StartCandlestickStream starts a real-time candlestick/kline stream for a given symbol and period.
func (b *BinanceAdapter) StartCandlestickStream(ctx context.Context, payload messages.AccountConnectCandlestickStreamPayload, strm chan []byte) error {
	b.wsServeMux.Lock()
	symbol := strings.ToLower(payload.SymbolName)
	streamKey := symbol + "_" + payload.Period
	doneChan := make(chan struct{})
	b.doneChans[streamKey] = doneChan
	b.wsServeMux.Unlock()

	wsURL := url.URL{
		Scheme: "wss",
		Host:   "stream.binance.com:443",
		Path:   fmt.Sprintf("/ws/%s@kline_%s", symbol, payload.Period),
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		b.wsServeMux.Lock()
		delete(b.doneChans, streamKey)
		b.wsServeMux.Unlock()
		close(doneChan)
		return fmt.Errorf("failed to connect candlestick WebSocket: %w", err)
	}

	go func() {
		defer conn.Close()

		cleanup := func() {
			b.wsServeMux.Lock()
			delete(b.doneChans, streamKey)
			b.wsServeMux.Unlock()
			close(doneChan)
		}

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
			case <-ctx.Done():
				log.Printf("Context cancelled for candlestick stream %s", streamKey)
				cleanup()
				return
			case err := <-errChan:
				log.Printf("Error in candlestick stream %s: %v", streamKey, err)
				cleanup()
				return
			case msg := <-msgChan:
				var event BinanceKlineEvent
				if err := json.Unmarshal(msg, &event); err != nil {
					log.Printf("Failed to unmarshal kline event for %s, skipping frame: %v", streamKey, err)
					continue
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
					Symbol:   payload.SymbolName,
					Interval: payload.Period,
				}
				barB, err := json.Marshal(barRes)
				if err != nil {
					log.Printf("Failed to marshal candlestick bar for %s, skipping frame: %v", streamKey, err)
					continue
				}
				select {
				case <-ctx.Done():
					cleanup()
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

func (b *BinanceAdapter) GetTickStream(ctx context.Context, payload messages.AccountConnectTickDataPayload) error {
	if payload.Binance == nil {
		return fmt.Errorf("binance payload is required for tick stream")
	}
	if payload.Binance.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for tick stream")
	}

	streamId := "ticks_" + strings.ToLower(payload.Binance.SymbolName)
	if err := b.binanceConn.AccountConnClient.AddStream(ctx, streamId); err != nil {
		return err
	}
	stream := b.binanceConn.AccountConnClient.Streams[streamId]

	go func() {
		for tickB := range stream {
			msg := messageutils.CreateSuccessResponse(ctx, messages.TypeLiveTicks, messages.Binance, b.binanceConn.AccountConnClient.ID, tickB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal tick stream message: %v", err)
				continue
			}
			b.binanceConn.AccountConnClient.Send <- msgB
		}
	}()

	return b.binanceConn.StartTickStream(ctx, payload.Binance.SymbolName, stream)
}

func (b *BinanceAdapter) GetAccountOrders(ctx context.Context, payload messages.AccountConnectOrderPayload) error {
	return nil
}

func (b *BinanceAdapter) GetCandlestickStream(ctx context.Context, payload messages.AccountConnectCandlestickStreamPayload) error {
	if payload.AccountID == 0 {
		return fmt.Errorf("account_id is required for binance candlestick stream")
	}
	if payload.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for binance candlestick stream")
	}
	if payload.Period == "" {
		return fmt.Errorf("period is required for binance candlestick stream")
	}

	if !validBinanceIntervals[payload.Period] {
		return fmt.Errorf("invalid period %q for binance candlestick stream — must be one of 1m,3m,5m,15m,30m,1h,2h,4h,6h,8h,12h,1d,3d,1w,1M", payload.Period)
	}

	if _, err := b.registry.Get(payload.AccountID); err != nil {
		return err
	}

	streamId := fmt.Sprintf("candlestick_binance_%d_%s", payload.AccountID, payload.SymbolName)
	if err := b.AccountConnClient.AddStream(ctx, streamId); err != nil {
		return err
	}
	stream := b.AccountConnClient.Streams[streamId]

	go func() {
		for barB := range stream {
			msg := messageutils.CreateSuccessResponse(ctx, messages.TypeCandlestickStream, messages.Binance, b.AccountConnClient.ID, barB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal candlestick stream message: %v", err)
				continue
			}
			b.AccountConnClient.Send <- msgB
		}
	}()
	return b.StartCandlestickStream(ctx, payload, stream)
}

func (b *BinanceAdapter) Disconnect(ctx context.Context) error {
	return nil
}

func (b *BinanceAdapter) GetOrderBookDepth(ctx context.Context, payload messages.AccountConnectDepthPayload) error {
	if payload.Binance == nil {
		return fmt.Errorf("binance payload is required for order book depth")
	}
	bp := payload.Binance
	if bp.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for order book depth")
	}

	acct, err := b.registry.Get(bp.AccountID)
	if err != nil {
		return err
	}

	limit := 100
	if bp.Limit != nil {
		limit = *bp.Limit
	}

	var res messages.AccountConnectDepthRes

	switch acct.accountType {
	case messages.BinanceAccountTypeSpot, messages.BinanceAccountTypeMargin:
		if acct.spotClient == nil {
			return fmt.Errorf("no spot client for account %d", acct.id)
		}
		depth, err := acct.spotClient.NewDepthService().Symbol(bp.SymbolName).Limit(limit).Do(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch spot order book depth: %w", err)
		}
		res = mappers.BinanceDepthToAccountConnectDepth(depth.Bids, depth.Asks, depth.LastUpdateID)

	case messages.BinanceAccountTypeFutures:
		if acct.futuresClient == nil {
			return fmt.Errorf("no futures client for account %d", acct.id)
		}
		depth, err := acct.futuresClient.NewDepthService().Symbol(bp.SymbolName).Limit(limit).Do(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch futures order book depth: %w", err)
		}
		res = mappers.BinanceDepthToAccountConnectDepth(depth.Bids, depth.Asks, depth.LastUpdateID)

	default:
		return fmt.Errorf("unsupported account type for order book depth: %s", acct.accountType)
	}

	res.SymbolName = bp.SymbolName
	resB, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("failed to marshal depth response: %w", err)
	}

	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeOrderBookDepth, messages.Binance, b.AccountConnClient.ID, resB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.AccountConnClient.Send <- msgB
	return nil
}

func (b *BinanceAdapter) GetDepthStream(ctx context.Context, payload messages.AccountConnectDepthPayload) error {
	if payload.Binance == nil {
		return fmt.Errorf("binance payload is required for depth stream")
	}
	bp := payload.Binance
	if bp.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for depth stream")
	}
	limit := 20
	if bp.Limit != nil {
		limit = *bp.Limit
	}
	if limit != 5 && limit != 10 && limit != 20 {
		return fmt.Errorf("depth stream limit must be 5, 10, or 20, got %d", limit)
	}

	acct, err := b.registry.Get(bp.AccountID)
	if err != nil {
		return err
	}

	streamId := fmt.Sprintf("depth_%s_%d", strings.ToLower(bp.SymbolName), limit)
	if err := b.AccountConnClient.AddStream(ctx, streamId); err != nil {
		return err
	}
	stream := b.AccountConnClient.Streams[streamId]

	go func() {
		for depthB := range stream {
			msg := messageutils.CreateSuccessResponse(ctx, messages.TypeDepthStream, messages.Binance, b.AccountConnClient.ID, depthB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal depth stream message: %v", err)
				continue
			}
			b.AccountConnClient.Send <- msgB
		}
	}()

	return b.binanceConn.StartDepthStream(ctx, acct.accountType, bp.SymbolName, limit, stream)
}

func (b *BinanceAdapter) GetBBOStream(ctx context.Context, payload messages.AccountConnectBBOPayload) error {
	if payload.Binance == nil {
		return fmt.Errorf("binance payload is required for BBO stream")
	}
	bp := payload.Binance

	acct, err := b.registry.Get(bp.AccountID)
	if err != nil {
		return err
	}

	symbolName := bp.SymbolName

	var streamId string
	if symbolName == "" {
		streamId = "bbo_all"
	} else {
		streamId = "bbo_" + strings.ToLower(symbolName)
	}

	if err := b.AccountConnClient.AddStream(ctx, streamId); err != nil {
		return err
	}
	stream := b.AccountConnClient.Streams[streamId]

	go func() {
		for bboB := range stream {
			msg := messageutils.CreateSuccessResponse(ctx, messages.TypeBBOStream, messages.Binance, b.AccountConnClient.ID, bboB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal BBO stream message: %v", err)
				continue
			}
			b.AccountConnClient.Send <- msgB
		}
	}()

	return b.binanceConn.StartBBOStream(ctx, acct.accountType, symbolName, stream)
}

func parseFloat(s string) float64 {
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		log.Printf("Failed to parse float from string %q: %v", s, err)
		return 0
	}
	return val
}

// parseDepthLevels converts Binance's [price, qty] string-pair arrays into typed levels.
func parseDepthLevels(raw [][]string) []messages.AccountConnectDepthLevel {
	levels := make([]messages.AccountConnectDepthLevel, 0, len(raw))
	for _, lvl := range raw {
		if len(lvl) != 2 {
			continue
		}
		levels = append(levels, messages.AccountConnectDepthLevel{
			Price:    parseFloat(lvl[0]),
			Quantity: parseFloat(lvl[1]),
		})
	}
	return levels
}
