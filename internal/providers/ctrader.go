package providers

import (
	"account-connect/common"
	"account-connect/config"
	gen_messages "account-connect/gen"
	"account-connect/internal/clients"
	"account-connect/internal/mappers"
	accdb "account-connect/persistence"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"sync"
	"time"

	acount_connect_messages "account-connect/internal/messages"

	messageutils "account-connect/internal/accountconnectmessageutils"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	MESSAGE_TYPE = websocket.BinaryMessage
)

type MessageHandler func(ctx context.Context, payload []byte) error

type pendingResponse struct {
	Payload []byte
	err     error
}

type appauthcredentials struct {
	ClientSecret string
	ClientId     string
	AccessToken  string
}

type pendingRequest struct {
	ctx    context.Context
	respCh chan *pendingResponse
}

type CTrader struct {
	accDb             accdb.AccountConnectCache
	AccountConnClient *clients.AccountConnectClient
	AccessToken       string
	PlatformConn      *websocket.Conn
	handlers          map[uint32]MessageHandler
	authCompleted     chan bool
	readyForAccount   bool
	pendingRequests   map[string]*pendingRequest
	mutex             sync.Mutex

	candleBuilders map[int64][]*candleBuilder // keyed by SymbolId
	symbolDigits   map[int64]float64          // cached scale, keyed by SymbolId
	builderMutex   sync.Mutex
}

type CtraderAdapter struct {
	ctrader CTrader
}

func NewCtraderAdapter(accdb accdb.AccountConnectCache, accountConnClient *clients.AccountConnectClient, ctraderconfig *config.CtraderConfig) *CtraderAdapter {
	return &CtraderAdapter{
		ctrader: *NewCTrader(accdb, accountConnClient, ctraderconfig),
	}
}

func NewCTrader(accdb accdb.AccountConnectCache, accountConnClient *clients.AccountConnectClient, ctraderconfig *config.CtraderConfig) *CTrader {
	return &CTrader{
		accDb:             accdb,
		AccountConnClient: accountConnClient,
		AccessToken:       ctraderconfig.AccessToken,
		handlers:          make(map[uint32]MessageHandler),
		authCompleted:     make(chan bool, 1),
		pendingRequests:   make(map[string]*pendingRequest),
		candleBuilders:    make(map[int64][]*candleBuilder),
		symbolDigits:      make(map[int64]float64),
	}
}

// RegisterHandler registers a handler for expected protobuf messages.
func (t *CTrader) RegisterHandler(msgType uint32, handler MessageHandler) {
	t.handlers[msgType] = handler
}

func (t *CTrader) registerHandlers() {
	t.RegisterHandler(uint32(common.ApplicationAthRes), t.handleApplicationAuthResponse)
	t.RegisterHandler(uint32(common.AccountAuthRes), t.handleAccountAuthResponse)
	t.RegisterHandler(uint32(common.ErrorRes), t.handleErrorReponse)
	t.RegisterHandler(uint32(common.HeartBeatRes), t.handleHeartBeatMessage)
	t.RegisterHandler(uint32(common.DealsRes), t.handleAccountHistoricalDeals)
	t.RegisterHandler(uint32(common.TokenRes), t.handleRefreshTokenResponse)
	t.RegisterHandler(uint32(common.TraderInfoRes), t.handleTraderInfoResponse)
	t.RegisterHandler(uint32(common.TrendBarsRes), t.handleTrendBarsResponse)
	t.RegisterHandler(uint32(common.SymbolListRes), t.handleSymbolListResponse)
	t.RegisterHandler(uint32(common.AccountSymbolInfoRes), t.handleSymbolsByIdResponse)
	t.RegisterHandler(uint32(common.AccountListRes), t.handleAccountListResponse)
	t.RegisterHandler(uint32(common.SpotEventMsgType), t.handleSpotEvent)
	t.RegisterHandler(uint32(common.AccountReconcileRes), t.handleAccountReconcileRes)
}

func (cta *CtraderAdapter) EstablishConnection(ctx context.Context, cfg config.PlatformConfigs) error {
	cendpoint := config.CtraderEndpoint
	cport := config.CtraderPort

	if cendpoint == "" || cport == 0 {
		return fmt.Errorf("missing required ctrader port or endpoint")
	}

	err := cta.ctrader.EstablishCtraderConnection(ctx, config.CtraderConfig{
		ClientId:     cfg.Ctrader.ClientId,
		ClientSecret: cfg.Ctrader.ClientSecret,
	})
	if err != nil {
		log.Printf("Connection establishment to ctrader fail: %v", err)
		return err
	}
	err = cta.ctrader.AuthorizeApplication(ctx, appauthcredentials{
		ClientSecret: cfg.Ctrader.ClientSecret,
		ClientId:     cfg.Ctrader.ClientId,
	})
	if err != nil {
		log.Printf("Failed to authorize application: %v", err)
		return err
	}
	return err
}

func (cta *CtraderAdapter) AuthorizeAccount(ctx context.Context, payload acount_connect_messages.AccountConnectAuthorizeTradingAccountPayload) error {
	return cta.ctrader.AuthorizeAccount(payload.AccountId)
}

func (cta *CtraderAdapter) GetUserAccounts(ctx context.Context) error {
	return nil
}

func (cta *CtraderAdapter) GetTradingSymbols(ctx context.Context, payload acount_connect_messages.AccountConnectSymbolsPayload) error {
	//Add additional check if the ctid is a valid ctid
	return cta.ctrader.GetAccountTradingSymbols(ctx, payload.Ctid)
}

func (cta *CtraderAdapter) GetHistoricalTrades(ctx context.Context, payload acount_connect_messages.AccountConnectHistoricalDealsPayload) error {
	if payload.Ctid == nil {
		return fmt.Errorf("ctid is required for historical deals")
	}
	return cta.ctrader.GetAccountHistoricalDeals(ctx, payload)
}

func (cta *CtraderAdapter) GetTraderInfo(ctx context.Context, payload acount_connect_messages.AccountConnectTraderInfoPayload) error {
	return cta.ctrader.GetAccountTraderInfo(ctx, payload.Ctid)
}

func (cta *CtraderAdapter) GetAccountOrders(ctx context.Context, accountConnectPayload acount_connect_messages.AccountConnectOrderPayload) error {

	if accountConnectPayload.Ctrader == nil {
		return fmt.Errorf("ctrader payload is required")
	}

	if err := cta.requireCtid(accountConnectPayload.Ctrader.CtID); err != nil {
		return err
	}
	return cta.ctrader.GetAccountOrders(ctx, *accountConnectPayload.Ctrader)

}

func (cta *CtraderAdapter) requireCtid(ctid *int64) error {
	if ctid == nil {
		return fmt.Errorf("ctid is required")
	}
	return nil
}

func (cta *CtraderAdapter) GetSymbolTrendBars(ctx context.Context, payload acount_connect_messages.AccountConnectTrendBarsPayload) error {
	return cta.ctrader.GetChartTrendBars(ctx, payload)
}

// GetCandlestickStream initializes and starts a real-time candlestick/kline stream for a given symbol and interval
func (cta *CtraderAdapter) GetCandlestickStream(ctx context.Context, payload acount_connect_messages.AccountConnectCandlestickStreamPayload) error {
	if payload.Ctrader == nil {
		return fmt.Errorf("missing ctrader payload for candlestick stream")
	}
	cp := payload.Ctrader
	if cp.Ctid == nil {
		return fmt.Errorf("ctid is required for ctrader candlestick stream")
	}
	if cp.SymbolId == 0 {
		return fmt.Errorf("symbol_id is required for ctrader candlestick stream")
	}
	if cp.Period == "" {
		return fmt.Errorf("period is required for ctrader candlestick stream")
	}

	fmt.Println("Getting candlestick stream..")

	// validate the period maps to a known duration before we do anything else
	if _, err := mappers.PeriodStrToDuration(cp.Period); err != nil {
		return fmt.Errorf("invalid period: %w", err)
	}

	streamId := fmt.Sprintf("candlestick_ctrader_%d_%s", cp.SymbolId, cp.Period)
	if err := cta.ctrader.AccountConnClient.AddStream(ctx, streamId); err != nil {
		return err
	}
	stream := cta.ctrader.AccountConnClient.Streams[streamId]

	go func() {
		for barB := range stream {
			msg := messageutils.CreateSuccessResponse(ctx, acount_connect_messages.TypeCandlestickStream, acount_connect_messages.Ctrader, cta.ctrader.AccountConnClient.ID, barB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal candlestick stream message: %v", err)
				continue
			}
			cta.ctrader.AccountConnClient.Send <- msgB
		}
	}()

	return cta.ctrader.StartCandlestickStream(ctx, *cp, stream)
}

func (cta *CtraderAdapter) InitializeClientStream(ctx context.Context, payload acount_connect_messages.AccountConnectStreamPayload) error {
	return nil
}

func (cta *CtraderAdapter) Disconnect(ctx context.Context) error {
	return cta.ctrader.DisconnectPlatformConn()
}

// EstablishCtraderConnection  establishes a  new ctrader websocket connection
func (t *CTrader) EstablishCtraderConnection(ctx context.Context, ctraderConfig config.CtraderConfig) error {
	// Set up a dialer with the desired options
	dialer := websocket.DefaultDialer
	dialer.EnableCompression = true
	dialer.HandshakeTimeout = 10 * time.Second

	endpoint := config.CtraderEndpoint
	port := config.CtraderPort

	log.Printf("establishing connection to %s:%d", endpoint, port)

	if endpoint == "" {
		return fmt.Errorf("missing cTrader server endpoint in configuration")
	}
	if port == 0 {
		return fmt.Errorf("missing cTrader server port in configuration")
	}

	url := fmt.Sprintf("wss://%s:%d", endpoint, port)
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return err
	}
	t.PlatformConn = conn

	t.registerHandlers()
	go t.StartConnectionReader(ctx)

	return nil
}

// StartConnectionReader will start a goroutine whose work will be to continously read protobuf messages sent by ctrader through
// the PlatformConn
func (t *CTrader) StartConnectionReader(ctx context.Context) {

	for {
		_, msgB, err := t.PlatformConn.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			return
		}

		var msgP gen_messages.ProtoMessage
		if err := proto.Unmarshal(msgB, &msgP); err != nil {
			log.Printf("Failed to unmarshal protocol message: %v", err)
			continue
		}

		if handler, ok := t.handlers[msgP.GetPayloadType()]; ok {
			if err := handler(ctx, msgP.Payload); err != nil {
				log.Printf("Handler error for type %d: %v", msgP.GetPayloadType(), err)
			}
		} else {
			log.Printf("No handler for message type %d", msgP.GetPayloadType())
		}
	}
}

// AuthorizeApplication is a request  authorizing an application to work with the cTrader platform Proxies.
func (t *CTrader) AuthorizeApplication(ctx context.Context, creds appauthcredentials) error {
	if creds.ClientId == "" || creds.ClientSecret == "" {
		return errors.New("client credentials not set")
	}

	msgReq := &gen_messages.ProtoOAApplicationAuthReq{
		ClientId:     &creds.ClientId,
		ClientSecret: &creds.ClientSecret,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AppAuthMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_APP_AUTH,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[common.REQ_ACCOUNT_LIST] = req
	t.mutex.Unlock()

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, common.REQ_ACCOUNT_LIST)
		t.mutex.Unlock()
		return fmt.Errorf("failed to send auth request: %w", err)
	}

	return nil

}

// DisconnectPlatformConn will close the existing ctrader connection for the client
func (t *CTrader) DisconnectPlatformConn() error {
	if t.PlatformConn != nil {
		return t.PlatformConn.Close()
	}
	return fmt.Errorf("close failed for nil ctrader platform connection")
}

// AuthorizeAccount sends a request to authorize specified ctrader account id
func (t *CTrader) AuthorizeAccount(accountId *int64) error {
	t.mutex.Lock()
	if !t.readyForAccount {
		t.mutex.Unlock()
		return errors.New("application not yet authorized")
	}
	t.mutex.Unlock()

	if accountId == nil {
		return errors.New("account id cannot be nil")
	}

	if len(strconv.FormatInt(*accountId, 10)) < 8 {
		return errors.New("invalid Account id")
	}

	msgReq := &gen_messages.ProtoOAAccountAuthReq{
		CtidTraderAccountId: accountId,
		AccessToken:         &t.AccessToken,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountAuthMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_ACCOUNT_AUTH,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send auth request: %w", err)
	}

	return nil
}

// GetUserAcountListByAccessToken gets a list of granted trader's accounts for the access token
func (t *CTrader) GetUserAcountListByAccessToken(accessToken string) error {
	t.mutex.Lock()
	if !t.readyForAccount {
		t.mutex.Unlock()
		return errors.New("application not yet authorized")
	}
	t.mutex.Unlock()

	msgReq := &gen_messages.ProtoOAGetAccountListByAccessTokenReq{
		AccessToken: &accessToken,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal account list request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountListMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_ACCOUNT_LIST,
	}
	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send account list request: %w", err)
	}

	return nil
}

// GetRefreshToken is a request to refresh the access token using refresh token of granted trader's account.
func (t *CTrader) GetRefreshToken() error {
	// refreshToken, err := t.accDb.Get("ctrader", "refresh_token")
	// if err != nil {
	// 	return err
	// }
	// refreshTokenStr := string(refreshToken)
	// msgReq := &gen_messages.ProtoOARefreshTokenReq{
	// 	RefreshToken: &refreshTokenStr,
	// }
	// msgB, err := proto.Marshal(msgReq)
	// if err != nil {
	// 	return fmt.Errorf("failed to marshal auth refresh request: %w", err)
	// }
	// msgP := &gen_messages.ProtoMessage{
	// 	PayloadType: &common.RefreshTokenMsgType,
	// 	Payload:     msgB,
	// 	ClientMsgId: &common.REQ_REFRESH_TOKEN,
	// }

	// protoMessage, err := proto.Marshal(msgP)
	// if err != nil {
	// 	return fmt.Errorf("failed to marshal protocol message: %w", err)
	// }

	// err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	// if err != nil {
	// 	return fmt.Errorf("failed to send refresh token request: %w", err)
	// }
	return nil
}

// GetAccountTraderInfo will retrieve the trader's information for the specified ctidTraderAccountId
func (t *CTrader) GetAccountTraderInfo(ctx context.Context, ctidTraderAccountId *int64) error {
	msgReq := &gen_messages.ProtoOATraderReq{
		CtidTraderAccountId: ctidTraderAccountId,
	}

	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal prototrader  request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.TraderInfoMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_TRADER_INFO,
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[common.REQ_TRADER_INFO] = req
	t.mutex.Unlock()

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, common.REQ_TRADER_INFO)
		t.mutex.Unlock()
		return fmt.Errorf("failed to get trader info: %w", err)
	}
	return nil

}

// GetAccountTraderInfo will retrieve the trader's information for the specified ctidTraderAccountId
func (t *CTrader) GetAccountOrders(ctx context.Context, requestOPts acount_connect_messages.CtraderOrdersRequestPayload) error {
	msgReq := &gen_messages.ProtoOAReconcileReq{
		CtidTraderAccountId: requestOPts.CtID,
	}

	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal prototrader  request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountReconcileReq,
		Payload:     msgB,
		ClientMsgId: &common.REQ_ACCOUNT_ORDERS,
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[common.REQ_ACCOUNT_ORDERS] = req
	t.mutex.Unlock()

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, common.REQ_ACCOUNT_ORDERS)
		t.mutex.Unlock()
		return fmt.Errorf("failed to get account orders: %w", err)
	}
	return nil

}

func (t *CTrader) StartCandlestickStream(ctx context.Context, payload acount_connect_messages.CtraderCandlestickStreamPayload, strm chan []byte) error {
	scale, err := t.getSymbolDigits(ctx, payload.Ctid, payload.SymbolId)
	if err != nil {
		return fmt.Errorf("failed to fetch symbol digits: %w", err)
	}

	builder, err := newCandleBuilder(payload.SymbolId, "", payload.Period, scale, strm)
	if err != nil {
		return fmt.Errorf("invalid period: %w", err)
	}

	t.builderMutex.Lock()
	existing := t.candleBuilders[payload.SymbolId]
	spotAlreadySubscribed := len(existing) > 0
	t.candleBuilders[payload.SymbolId] = append(existing, builder)
	t.builderMutex.Unlock()

	if !spotAlreadySubscribed {
		if err := t.subscribeSpots(ctx, payload.Ctid, []int64{payload.SymbolId}); err != nil {
			return fmt.Errorf("failed to subscribe to spots: %w", err)
		}
	}

	if err := t.subscribeLiveTrendbar(ctx, payload.Ctid, payload.SymbolId, payload.Period); err != nil {
		return fmt.Errorf("failed to subscribe to live trendbar: %w", err)
	}

	return nil
}

// subscribeSpots sends a request to subscribe to live spot price events for the given symbol IDs.
// This is fire-and-forget: cTrader does not send a meaningful synchronous response to correlate,
// since live ProtoOASpotEvent pushes are the de facto continuation of this request.
func (t *CTrader) subscribeSpots(ctx context.Context, ctid *int64, symbolIds []int64) error {
	msgReq := &gen_messages.ProtoOASubscribeSpotsReq{
		CtidTraderAccountId: ctid,
		SymbolId:            symbolIds,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal subscribe spots request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.SubscribeSpotsMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_SUBSCRIBE_SPOTS,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	if err := t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage); err != nil {
		return fmt.Errorf("failed to send subscribe spots request: %w", err)
	}
	return nil
}

// subscribeLiveTrendbar sends a request to subscribe to live trend bar pushes for the given
// symbol and period. Per cTrader's OpenAPI, this requires an existing spot subscription for
// the same symbol — callers must subscribeSpots first.
func (t *CTrader) subscribeLiveTrendbar(ctx context.Context, ctid *int64, symbolId int64, period string) error {
	trendPeriod, err := mappers.PeriodStrToBarPeriod(period)
	if err != nil {
		return fmt.Errorf("invalid period: %w", err)
	}

	fmt.Printf("Subscribing to trendbars period: %v and and symbol: %v", trendPeriod, symbolId)

	msgReq := &gen_messages.ProtoOASubscribeLiveTrendbarReq{
		CtidTraderAccountId: ctid,
		SymbolId:            &symbolId,
		Period:              &trendPeriod,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal subscribe live trendbar request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.SubscribeLiveTrendbarMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_SUBSCRIBE_LIVE_TRENDBAR,
	}
	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	if err := t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage); err != nil {
		return fmt.Errorf("failed to send subscribe live trendbar request: %w", err)
	}
	return nil
}

// GetAccountTradingSymbols will retrieve a list of trading symbols(trading pairs e.g EUR/USD) for a certain trading account
func (t *CTrader) GetAccountTradingSymbols(ctx context.Context, ctId *int64) error {
	msgReq := &gen_messages.ProtoOASymbolsListReq{
		CtidTraderAccountId: ctId,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal symbol list  request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountSymbolListMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_SYMBOL_LIST,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[common.REQ_SYMBOL_LIST] = req
	t.mutex.Unlock()

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, common.REQ_SYMBOL_LIST)
		t.mutex.Unlock()
		return fmt.Errorf("failed to send trading account symbols request: %w", err)
	}
	return nil
}

func (t *CTrader) getSymbolListInformation(ctx context.Context, opts acount_connect_messages.AccountConnectSymbolInfoPayload) error {
	msgReq := &gen_messages.ProtoOASymbolByIdReq{
		CtidTraderAccountId: opts.Ctid,
		SymbolId:            opts.SymbolId,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal symbol list info request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountSymbolInfo,
		Payload:     msgB,
		ClientMsgId: &common.REQ_SYMBOL_INFO,
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[common.REQ_SYMBOL_INFO] = req
	t.mutex.Unlock()

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, common.REQ_SYMBOL_INFO)
		t.mutex.Unlock()

		return fmt.Errorf("failed to send additional data request: %w", err)
	}

	return nil
}

// GetChartTrendBars will request trend bar series data as  requested by [trendbarsArgs]
func (t *CTrader) GetChartTrendBars(ctx context.Context, trendbarsArgs acount_connect_messages.AccountConnectTrendBarsPayload) error {
	trendPeriod, err := mappers.PeriodStrToBarPeriod(trendbarsArgs.Period)
	if err != nil {
		return fmt.Errorf("invalid trend period: %w", err)
	}
	msgReq := &gen_messages.ProtoOAGetTrendbarsReq{
		CtidTraderAccountId: trendbarsArgs.Ctid,
		Period:              &trendPeriod,
		SymbolId:            &trendbarsArgs.SymbolId,
		FromTimestamp:       trendbarsArgs.FromTimestamp,
		ToTimestamp:         trendbarsArgs.ToTimestamp,
	}

	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal trend bars  request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.TrendBarsMsyType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_TREND_BARS,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[common.REQ_TREND_BARS] = req
	t.mutex.Unlock()

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, common.REQ_TREND_BARS)
		t.mutex.Unlock()
		return fmt.Errorf("failed to send trend bars request: %w", err)
	}
	return nil
}

// GetAccountHistoricalDeals is a request for getting trader's deals historical data (execution details).
func (t *CTrader) GetAccountHistoricalDeals(ctx context.Context, payload acount_connect_messages.AccountConnectHistoricalDealsPayload) error {
	if payload.Ctid == nil {
		return errors.New("ctid cannot be nil")
	}
	if len(strconv.FormatInt(*payload.Ctid, 10)) < 8 {
		return errors.New("invalid ctid")
	}

	msgReq := &gen_messages.ProtoOADealListReq{
		CtidTraderAccountId: payload.Ctid,
		FromTimestamp:       payload.FromTimestamp,
		ToTimestamp:         payload.ToTimestamp,
		MaxRows:             payload.MaxRows,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal deal list request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountHistoricalDeals,
		Payload:     msgB,
		ClientMsgId: &common.REQ_ACCOUNT_HISTORICAL_DEALS,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[common.REQ_ACCOUNT_HISTORICAL_DEALS] = req
	t.mutex.Unlock()

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, common.REQ_ACCOUNT_HISTORICAL_DEALS)
		t.mutex.Unlock()
		return fmt.Errorf("failed to request account historical trades: %w", err)
	}

	return nil
}

// getSymbolDigits fetches and caches the price scale (10^digits) for symbolId, used to convert
// raw integer spot prices into actual decimal prices. If already cached, returns immediately
// without a round-trip to cTrader.
func (t *CTrader) getSymbolDigits(ctx context.Context, ctid *int64, symbolId int64) (float64, error) {
	t.builderMutex.Lock()
	if scale, ok := t.symbolDigits[symbolId]; ok {
		t.builderMutex.Unlock()
		return scale, nil
	}
	t.builderMutex.Unlock()

	reqKey := fmt.Sprintf("REQ_SYMBOL_INFO_STREAM_%d", symbolId)

	msgReq := &gen_messages.ProtoOASymbolByIdReq{
		CtidTraderAccountId: ctid,
		SymbolId:            []int64{symbolId},
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal symbol info request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountSymbolInfo,
		Payload:     msgB,
		ClientMsgId: &reqKey,
	}
	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	if err := t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage); err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
		t.mutex.Unlock()
		return 0, fmt.Errorf("failed to send symbol info request: %w", err)
	}

	resp := <-req.respCh
	if resp.err != nil {
		return 0, resp.err
	}

	var symbolRes gen_messages.ProtoOASymbolByIdRes
	if err := proto.Unmarshal(resp.Payload, &symbolRes); err != nil {
		return 0, fmt.Errorf("failed to unmarshal symbol info: %w", err)
	}
	if len(symbolRes.Symbol) == 0 || symbolRes.Symbol[0].Digits == nil {
		return 0, fmt.Errorf("symbol info missing digits for symbol_id %d", symbolId)
	}

	scale := math.Pow(10, float64(*symbolRes.Symbol[0].Digits))

	t.builderMutex.Lock()
	t.symbolDigits[symbolId] = scale
	t.builderMutex.Unlock()

	return scale, nil
}

func (t *CTrader) handleApplicationAuthResponse(ctx context.Context, payload []byte) error {
	var (
		r gen_messages.ProtoOAApplicationAuthRes
	)

	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal auth response: %w", err)
	}

	t.mutex.Lock()
	t.readyForAccount = true
	req, ok := t.pendingRequests[common.REQ_ACCOUNT_LIST]
	t.mutex.Unlock()

	if !ok {
		return fmt.Errorf("failed to find pending request for account list to authorize")
	}
	go func() {
		ch := <-req.respCh
		msg := messageutils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeConnect, acount_connect_messages.Ctrader, t.AccountConnClient.ID, ch.Payload)
		msgB, err := json.Marshal(msg)
		if err != nil {
			log.Printf("Failed to marshal application auth: %v", err)
			return
		}
		t.AccountConnClient.Send <- msgB
	}()

	return t.GetUserAcountListByAccessToken(t.AccessToken)
}

func (t *CTrader) handleAccountListResponse(ctx context.Context, payload []byte) error {
	var (
		r               gen_messages.ProtoOAGetAccountListByAccessTokenRes
		tradingaccounts []acount_connect_messages.AccountConnectCtraderTradingAccount
	)

	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal auth response: %w", err)
	}
	t.mutex.Lock()
	t.readyForAccount = true
	req, ok := t.pendingRequests[common.REQ_ACCOUNT_LIST]
	t.mutex.Unlock()

	if !ok {
		return fmt.Errorf("failed to find pending request for account list to authorize")
	}

	for _, acc := range r.CtidTraderAccount {
		tradingaccounts = append(tradingaccounts, acount_connect_messages.AccountConnectCtraderTradingAccount{
			AccountId:  acc.CtidTraderAccountId,
			BrokerName: *acc.BrokerTitleShort,
		})
	}

	tradingaccountsres := acount_connect_messages.AccountConnectTradingAccountRes{
		CtTradingAccounts: tradingaccounts,
	}

	tradingaccountsresB, err := json.Marshal(tradingaccountsres)
	if err != nil {
		return err
	}
	req.respCh <- &pendingResponse{
		Payload: tradingaccountsresB,
	}
	return nil
}

func (t *CTrader) handleHeartBeatMessage(ctx context.Context, payload []byte) error {
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.HeartBeatMsgType,
	}
	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send back a heartbeat message: %w", err)
	}
	return nil
}

func (t *CTrader) handleAccountAuthResponse(ctx context.Context, payload []byte) error {
	var (
		r gen_messages.ProtoOAAccountAuthRes
	)
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal account auth response: %w", err)
	}

	accauthresB, err := json.Marshal(map[string]any{
		"message":    "account authorized",
		"account_id": r.CtidTraderAccountId,
	})

	if err != nil {
		return err
	}
	msg := messageutils.CreateSuccessResponse(ctx, acount_connect_messages.TypeAuthorizeAccount, acount_connect_messages.Ctrader, t.AccountConnClient.ID, accauthresB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	t.AccountConnClient.Send <- msgB

	return nil
}

func (t *CTrader) handleRefreshTokenResponse(ctx context.Context, payload []byte) error {
	var r gen_messages.ProtoOARefreshTokenRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal refresh token response: %w", err)
	}
	// err := t.accDb.Put("ctrader", "refresh_token", *r.AccessToken)
	// if err != nil {
	// 	return err
	// }
	return nil
}

func (t *CTrader) handleTraderInfoResponse(ctx context.Context, payload []byte) error {
	var r gen_messages.ProtoOATraderRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal trader info response: %w", err)
	}
	traderInfo := mappers.ProtoOATraderToaccountConnectTrader(&r)
	traderInfoB, err := json.Marshal(traderInfo)
	if err != nil {
		log.Printf("Failed to marshal trader info: %v", err)
		return err
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[common.REQ_TRADER_INFO]
	t.mutex.Unlock()

	if !ok {
		return fmt.Errorf("failed to find pending request for key: %s", common.REQ_TRADER_INFO)
	}

	msg := messageutils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeTraderInfo, acount_connect_messages.Ctrader, t.AccountConnClient.ID, traderInfoB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	t.AccountConnClient.Send <- msgB

	return nil
}

func (t *CTrader) handleTrendBarsResponse(ctx context.Context, payload []byte) error {
	var res gen_messages.ProtoOAGetTrendbarsRes
	if err := proto.Unmarshal(payload, &res); err != nil {
		return fmt.Errorf("failed to unmarshal trend bars: %w", err)
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[common.REQ_TREND_BARS]
	t.mutex.Unlock()

	if !ok {
		return fmt.Errorf("failed to find  pending request with key: %s", common.REQ_TREND_BARS)
	}

	trendBars := mappers.ProotoOAToTrendBars(&res)
	if err := t.getSymbolListInformation(req.ctx, acount_connect_messages.AccountConnectSymbolInfoPayload{
		SymbolId: []int64{*res.SymbolId},
		Ctid:     res.CtidTraderAccountId,
	}); err != nil {
		return err
	}

	go func() { t.handleSymbolInfoForTrendBars(trendBars) }()

	return nil
}

func (t *CTrader) handleSymbolInfoForTrendBars(trendBars []acount_connect_messages.AccountConnectTrendBar) {
	t.mutex.Lock()
	req, ok := t.pendingRequests[common.REQ_SYMBOL_INFO]
	t.mutex.Unlock()

	if !ok {
		return
	}

	resp := <-req.respCh
	if resp.err != nil {
		return
	}
	symInfoB := resp.Payload

	var symbolRes gen_messages.ProtoOASymbolByIdRes
	if err := proto.Unmarshal(symInfoB, &symbolRes); err != nil {
		log.Printf("Failed to unmarshal symbol info: %v", err)
		return
	}

	if len(symbolRes.Symbol) == 0 || symbolRes.Symbol[0].Digits == nil {
		log.Println("Invalid symbol info: Digits missing")
		return
	}

	digits := float64(*symbolRes.Symbol[0].Digits)
	scale := math.Pow(10, digits)

	var mappedTrendBars []acount_connect_messages.AccountConnectTrendBar
	for _, bar := range trendBars {
		mappedTrendBars = append(mappedTrendBars, acount_connect_messages.AccountConnectTrendBar{
			High:                  bar.High / scale,
			Low:                   bar.Low / scale,
			Close:                 bar.Close / scale,
			Open:                  bar.Open / scale,
			Volume:                bar.Volume,
			UtcTimestampInMinutes: bar.UtcTimestampInMinutes,
		})
	}

	trendBarsRes := acount_connect_messages.AccountConnectTrendBarRes{
		Trendbars: mappedTrendBars,
	}
	trendBarsResB, err := json.Marshal(trendBarsRes)
	if err != nil {
		log.Printf("Failed to marshal scaled trend bars: %v", err)
		return
	}

	msg := messageutils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeTrendBars, acount_connect_messages.Ctrader, t.AccountConnClient.ID, trendBarsResB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal final message: %v", err)
		return
	}

	t.AccountConnClient.Send <- msgB
}

func (t *CTrader) handleSymbolListResponse(ctx context.Context, payload []byte) error {
	var (
		r gen_messages.ProtoOASymbolsListRes
	)
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal symbol list: %w", err)
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[common.REQ_SYMBOL_LIST]
	t.mutex.Unlock()

	if !ok {
		return fmt.Errorf("failed to find pending request for key: %s", common.REQ_SYMBOL_LIST)
	}

	syms := mappers.ProtoSymbolListResponseToAccountConnectSymbol(&r)
	accconnectsyms := acount_connect_messages.AccountConnectSymbolRes{
		AccountConnectSymbols: syms,
	}

	accconnectsymsB, err := json.Marshal(accconnectsyms)
	if err != nil {
		log.Printf("Failed to marshal symbol list data: %v", err)
		return err
	}

	msg := messageutils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeAccountSymbols, acount_connect_messages.Ctrader, t.AccountConnClient.ID, accconnectsymsB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	t.AccountConnClient.Send <- msgB

	return nil
}

func (t *CTrader) handleAccountHistoricalDeals(ctx context.Context, payload []byte) error {
	var r gen_messages.ProtoOADealListRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal historical deals: %w", err)
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[common.REQ_ACCOUNT_HISTORICAL_DEALS]
	t.mutex.Unlock()

	if ok {
		deals := mappers.ProtoOADealToAccountConnectDeal(&r)
		dealsB, err := json.Marshal(deals)
		if err != nil {
			return fmt.Errorf("failed to marshal deal: %s", err)
		}
		msg := messageutils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeHistoricalTrades, acount_connect_messages.Ctrader, t.AccountConnClient.ID, dealsB)
		msgB, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		t.AccountConnClient.Send <- msgB
		return nil
	}
	return fmt.Errorf("failed to find pending request for msg id: %v", common.REQ_ACCOUNT_HISTORICAL_DEALS)
}

func (t *CTrader) handleAccountReconcileRes(ctx context.Context, payload []byte) error {

	var r gen_messages.ProtoOAReconcileRes

	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal historical deals: %w", err)
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[common.REQ_ACCOUNT_ORDERS]
	t.mutex.Unlock()

	if ok {
		orders := mappers.ProtoOAReconcileToAccountConnectOrder(&r)
		ordersB, err := json.Marshal(orders)
		if err != nil {
			return fmt.Errorf("failed to marshal deal: %s", err)
		}
		msg := messageutils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeAccountOrders, acount_connect_messages.Ctrader, t.AccountConnClient.ID, ordersB)
		msgB, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		t.AccountConnClient.Send <- msgB
		return nil
	}
	return fmt.Errorf("failed to find pending request for msg id: %v", common.REQ_ACCOUNT_ORDERS)

}

// func (t *CTrader) handleSymbolsByIdResponse(ctx context.Context, payload []byte) error {
// 	var r gen_messages.ProtoOASymbolByIdRes
// 	t.mutex.Lock()
// 	defer t.mutex.Unlock()

// 	if err := proto.Unmarshal(payload, &r); err != nil {
// 		return fmt.Errorf("failed to unmarshal symbols info: %w", err)
// 	}

// 	if rqts, exists := t.pendingRequests[common.REQ_SYMBOL_INFO]; exists {
// 		resp := &pendingResponse{
// 			Payload: payload,
// 			err:     nil,
// 		}
// 		rqts.respCh <- resp
// 		delete(t.pendingRequests, common.REQ_SYMBOL_INFO)
// 	}

// 	return nil
// }

func (t *CTrader) handleSymbolsByIdResponse(ctx context.Context, payload []byte) error {
	var r gen_messages.ProtoOASymbolByIdRes
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal symbols info: %w", err)
	}

	// existing trend-bars flow: single fixed key
	if rqts, exists := t.pendingRequests[common.REQ_SYMBOL_INFO]; exists {
		rqts.respCh <- &pendingResponse{Payload: payload, err: nil}
		delete(t.pendingRequests, common.REQ_SYMBOL_INFO)
	}

	// streaming flow: per-symbol key(s) — a single response can carry multiple symbols
	for _, sym := range r.Symbol {
		if sym.SymbolId == nil {
			continue
		}
		reqKey := fmt.Sprintf("REQ_SYMBOL_INFO_STREAM_%d", *sym.SymbolId)
		if rqts, exists := t.pendingRequests[reqKey]; exists {
			rqts.respCh <- &pendingResponse{Payload: payload, err: nil}
			delete(t.pendingRequests, reqKey)
		}
	}

	return nil
}

func (t *CTrader) handleSpotEvent(ctx context.Context, payload []byte) error {
	var ev gen_messages.ProtoOASpotEvent
	if err := proto.Unmarshal(payload, &ev); err != nil {
		return fmt.Errorf("failed to unmarshal spot event: %w", err)
	}

	if ev.SymbolId == nil || len(ev.Trendbar) == 0 {
		return nil
	}

	t.builderMutex.Lock()
	builders := t.candleBuilders[*ev.SymbolId]
	t.builderMutex.Unlock()

	for _, tb := range ev.Trendbar {
		if tb.Period == nil {
			continue
		}
		for _, b := range builders {
			if *tb.Period == b.periodEnum {
				b.onTrendbar(tb)
			}
		}
	}

	return nil
}

func (t *CTrader) handleErrorReponse(ctx context.Context, payload []byte) error {
	var r gen_messages.ProtoOAErrorRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal error response: %w", err)
	}

	log.Printf("Received an error response: %s  with error code: %s", string(*r.Description), r.GetErrorCode())
	pErr := mappers.ProtoOAErrorResToError(&r)
	pErrB, err := json.Marshal(pErr)
	if err != nil {
		return fmt.Errorf("failed to marshal error response: %s", err)
	}
	msg := messageutils.CreateErrorResponse(t.AccountConnClient.ID, pErrB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	t.AccountConnClient.Send <- msgB

	return nil
}

// candleBuilder holds the per-(symbol, period) state needed to convert cTrader's live
// ProtoOATrendbar pushes into AccountConnectCandlestickBar messages and relay them onto
// the client's output stream. cTrader does the OHLC aggregation server-side; this type
// tracks scale and the last fully-seen bar so that, on rollover to a new timestamp, it can
// emit a clean IsFinal snapshot of the bar that just closed before relaying the new one.
type candleBuilder struct {
	symbolId      int64
	symbolName    string
	period        string                             // string form, used for output Interval field
	periodEnum    gen_messages.ProtoOATrendbarPeriod // enum form, used for matching incoming pushes
	periodSeconds int64
	scale         float64

	lastBar    acount_connect_messages.AccountConnectCandlestickBar
	hasSeenBar bool

	out chan []byte
}

func newCandleBuilder(symbolId int64, symbolName, period string, scale float64, out chan []byte) (*candleBuilder, error) {
	d, err := mappers.PeriodStrToDuration(period)
	if err != nil {
		return nil, err
	}
	periodEnum, err := mappers.PeriodStrToBarPeriod(period)
	if err != nil {
		return nil, err
	}
	return &candleBuilder{
		symbolId:      symbolId,
		symbolName:    symbolName,
		period:        period,
		periodEnum:    periodEnum,
		periodSeconds: int64(d.Seconds()),
		scale:         scale,
		out:           out,
	}, nil
}

// onTrendbar converts a live ProtoOATrendbar push into an AccountConnectCandlestickBar.
// If the push's timestamp differs from the last one seen, the previous bar is first emitted
// as final (using its own last-known OHLC, untouched by this new push), then the new bar is
// emitted as the start of an in-progress candle. Same-timestamp pushes just relay the
// updated in-progress bar.
func (cb *candleBuilder) onTrendbar(tb *gen_messages.ProtoOATrendbar) {
	if tb.Low == nil || tb.UtcTimestampInMinutes == nil {
		return
	}

	low := float64(*tb.Low) / cb.scale
	open, high, close := low, low, low
	if tb.DeltaOpen != nil {
		open = low + float64(*tb.DeltaOpen)/cb.scale
	}
	if tb.DeltaHigh != nil {
		high = low + float64(*tb.DeltaHigh)/cb.scale
	}
	if tb.DeltaClose != nil {
		close = low + float64(*tb.DeltaClose)/cb.scale
	}

	var volume int64
	if tb.Volume != nil {
		volume = *tb.Volume
	}

	ts := int64(*tb.UtcTimestampInMinutes) * 60

	newBar := acount_connect_messages.AccountConnectCandlestickBar{
		OpenTime:  ts,
		Open:      open,
		High:      high,
		Low:       low,
		Close:     close,
		Volume:    float64(volume),
		CloseTime: ts + cb.periodSeconds,
		IsFinal:   false,
	}

	if cb.hasSeenBar && cb.lastBar.OpenTime != ts {
		finalBar := cb.lastBar
		finalBar.IsFinal = true
		cb.emitBar(finalBar)
	}

	cb.lastBar = newBar
	cb.hasSeenBar = true
	cb.emitBar(newBar)
}

func (cb *candleBuilder) emitBar(bar acount_connect_messages.AccountConnectCandlestickBar) {
	barRes := acount_connect_messages.AccountConnectCandlestickBarRes{
		Bars:     []acount_connect_messages.AccountConnectCandlestickBar{bar},
		Symbol:   cb.symbolName,
		Interval: cb.period,
	}
	barB, err := json.Marshal(barRes)
	if err != nil {
		log.Printf("Failed to marshal candlestick bar for symbol %d: %v", cb.symbolId, err)
		return
	}
	select {
	case cb.out <- barB:
	default:
		log.Printf("Stream channel full for symbol %d period %s, dropping update", cb.symbolId, cb.period)
	}
}
