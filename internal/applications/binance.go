package applications

import (
	messageutils "account-connect/internal/accountconnectmessageutils"
	"account-connect/internal/mappers"
	messages "account-connect/internal/messages"
	"account-connect/internal/models"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"

	"github.com/adshao/go-binance/v2"
	"github.com/gorilla/websocket"
)

// MiniTickerEvent represents a miniTicker WebSocket message
type MiniTickerEvent struct {
	EventType   string `json:"e"`
	EventTime   int64  `json:"E"`
	Symbol      string `json:"s"`
	ClosePrice  string `json:"c"`
	OpenPrice   string `json:"o"`
	HighPrice   string `json:"h"`
	LowPrice    string `json:"l"`
	Volume      string `json:"v"`
	QuoteVolume string `json:"q"`
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
	Client            *binance.Client
	AccountConnClient *models.AccountConnectClient
	wsServeMux        sync.Mutex
	doneChans         map[string]chan struct{}
}

func NewBinanceConnection(accountConnClient *models.AccountConnectClient) *BinanceConnection {
	return &BinanceConnection{
		AccountConnClient: accountConnClient,
		doneChans:         make(map[string]chan struct{}),
	}
}

func (b *BinanceConnection) Connect(apiKey, secretKey string) error {
	b.Client = binance.NewClient(apiKey, secretKey)
	return nil
}

func (b *BinanceConnection) GetHistoricalTrades(ctx context.Context) error {
	return fmt.Errorf("GetHistoricalTrades not implemented for  binance")
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

func (b *BinanceConnection) GetTraderInfo(ctx context.Context) error {
	return fmt.Errorf("GetTraderInfo not implemented for binance")
}

func (b *BinanceConnection) GetSymbolTrendBars(ctx context.Context, trendbarsArgs messages.AccountConnectTrendBarsPayload) ([]byte, error) {
	//TODO:
	//add check for fields sent in via trendbarsArgs
	kservice := b.Client.NewKlinesService()
	ohlc, err := kservice.Symbol(trendbarsArgs.SymbolName).Interval(trendbarsArgs.Period).StartTime(*trendbarsArgs.FromTimestamp).EndTime(*trendbarsArgs.ToTimestamp).Do(ctx)
	if err != nil {
		return nil, err
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
		log.Printf("Failed to marshal acctrendbars data : %v", err)
		return nil, err
	}

	return acctrendbarsB, nil
}

// GetBinanceTradingSymbols will retrieve all the available tradable symbols from binance
func (b *BinanceConnection) GetBinanceTradingSymbols(ctx context.Context) ([]byte, error) {
	exchangeInfo, err := b.Client.NewExchangeInfoService().Do(ctx)
	if err != nil {
		log.Printf("Failed to retrieve binance trading symbols")
		return nil, err
	}
	syms := mappers.BinanceSymbolToAccountConnectSymbol(exchangeInfo.Symbols)
	symsB, err := json.Marshal(syms)
	if err != nil {
		log.Printf("Failed to marshal trading symbols data info: %v", err)
		return nil, err
	}
	return symsB, nil
}

type BinanceAdapter struct {
	binanceConn *BinanceConnection
}

func NewBinanceAdapter(accountConnClient *models.AccountConnectClient) *BinanceAdapter {
	return &BinanceAdapter{
		binanceConn: NewBinanceConnection(accountConnClient),
	}
}

func (b *BinanceAdapter) EstablishConnection(ctx context.Context, cfg PlatformConfigs) error {
	apiKey := cfg.ApiKey
	secretKey := cfg.SecretKey

	conn := NewBinanceConnection(b.binanceConn.AccountConnClient)
	err := conn.Connect(apiKey, secretKey)

	if err != nil {
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
	msg := messageutils.CreateSuccessResponse(ctx, messages.TypeAccountSymbols, messages.Binance, b.binanceConn.AccountConnClient.ID, binanceSyms)

	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.binanceConn.AccountConnClient.Send <- msgB
	return nil
}

func (b *BinanceAdapter) GetHistoricalTrades(ctx context.Context, payload messages.AccountConnectHistoricalDealsPayload) error {
	return b.binanceConn.GetHistoricalTrades(ctx)
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

// InitializeClientStream will initialize a stream of real time market prices for the specified stream id for a particular symbol
func (b *BinanceAdapter) InitializeClientStream(ctx context.Context, payload messages.AccountConnectStreamPayload) error {
	streamType := payload.StreamType
	symbolId := payload.SymbolId
	if streamType == "" || symbolId == "" {
		return fmt.Errorf("required streamid or symbolid is missing")
	}
	streamId := streamType + "_" + symbolId

	err := b.binanceConn.AccountConnClient.AddStream(streamId)
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

func (b *BinanceAdapter) Disconnect() error {
	return nil
}
