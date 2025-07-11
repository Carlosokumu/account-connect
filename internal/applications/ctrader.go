package applications

import (
	"account-connect/common"
	"account-connect/config"
	gen_messages "account-connect/gen"
	"account-connect/internal/mappers"
	"account-connect/internal/models"
	"account-connect/internal/utils"
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

type pendingRequest struct {
	ctx    context.Context
	respCh chan *pendingResponse
}

type CTrader struct {
	accDb             accdb.AccountConnectDb
	ClientSecret      string
	AccountConnClient *models.AccountConnectClient
	ClientId          string
	AccountId         *int64
	AccessToken       string
	PlatformConn      *websocket.Conn
	handlers          map[uint32]MessageHandler
	authCompleted     chan bool
	readyForAccount   bool
	pendingRequests   map[string]*pendingRequest
	mutex             sync.Mutex
}

type CtraderAdapter struct {
	ctrader CTrader
}

func NewCtraderAdapter(accdb accdb.AccountConnectDb, accountConnClient *models.AccountConnectClient, ctraderconfig *config.CTraderConfig) *CtraderAdapter {
	return &CtraderAdapter{
		ctrader: *NewCTrader(accdb, accountConnClient, ctraderconfig),
	}
}

// NewCTrader creates a new ctrader instance with the required fields to establish a ctrader connection
func NewCTrader(accdb accdb.AccountConnectDb, accountConnClient *models.AccountConnectClient, ctraderconfig *config.CTraderConfig) *CTrader {
	return &CTrader{
		accDb:             accdb,
		ClientId:          ctraderconfig.ClientID,
		AccountId:         &ctraderconfig.AccountID,
		ClientSecret:      ctraderconfig.ClientSecret,
		AccountConnClient: accountConnClient,
		AccessToken:       ctraderconfig.AccessToken,
		handlers:          make(map[uint32]MessageHandler),
		authCompleted:     make(chan bool, 1),
		pendingRequests:   make(map[string]*pendingRequest),
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

}

func (cta *CtraderAdapter) EstablishConnection(ctx context.Context, cfg PlatformConfigs) error {
	srvCfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("Failed to load configs in route: %v", err)
		return err
	}

	err = cta.ctrader.EstablishCtraderConnection(ctx, config.CTraderConfig{
		ClientID:     cfg.ClientId,
		ClientSecret: cfg.SecretKey,
		Endpoint:     srvCfg.Servers.Ctrader.Endpoint,
		Port:         srvCfg.Servers.Ctrader.Port,
		AccountID:    *cfg.AccountId,
	})
	if err != nil {
		log.Printf("Connection establishment to ctrader fail: %v", err)
		return err
	}
	err = cta.ctrader.AuthorizeApplication()
	if err != nil {
		log.Printf("Failed to authorize application: %v", err)
		return err
	}
	return err
}

func (cta *CtraderAdapter) GetTradingSymbols(ctx context.Context, payload acount_connect_messages.AccountConnectSymbolsPayload) error {
	//Add additional check if the ctid is a valid ctid
	return cta.ctrader.GetAccountTradingSymbols(ctx, payload.Ctid)
}

func (cta *CtraderAdapter) GetHistoricalTrades(ctx context.Context, payload acount_connect_messages.AccountConnectHistoricalDealsPayload) error {
	//Add a check for  validity of the to and from timestamps
	return cta.ctrader.GetAccountHistoricalDeals(ctx, *payload.FromTimestamp, *payload.ToTimestamp)
}

func (cta *CtraderAdapter) GetTraderInfo(ctx context.Context, payload acount_connect_messages.AccountConnectTraderInfoPayload) error {
	return cta.ctrader.GetAccountTraderInfo(ctx, payload.Ctid)
}

func (cta *CtraderAdapter) GetSymbolTrendBars(ctx context.Context, payload acount_connect_messages.AccountConnectTrendBarsPayload) error {
	return cta.ctrader.GetChartTrendBars(ctx, payload)
}

func (cta *CtraderAdapter) InitializeClientStream(ctx context.Context, payload acount_connect_messages.AccountConnectStreamPayload) error {
	return nil
}

func (cta *CtraderAdapter) Disconnect() error {
	return cta.ctrader.DisconnectPlatformConn()
}

// EstablishCtraderConnection  establishes a  new ctrader websocket connection
func (t *CTrader) EstablishCtraderConnection(ctx context.Context, ctraderConfig config.CTraderConfig) error {
	// Set up a dialer with the desired options
	dialer := websocket.DefaultDialer
	dialer.EnableCompression = true
	dialer.HandshakeTimeout = 10 * time.Second

	err := t.accDb.RegisterBucket("ctrader")
	if err != nil {
		return err
	}

	endpoint := ctraderConfig.Endpoint
	port := ctraderConfig.Port

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
func (t *CTrader) AuthorizeApplication() error {
	platformConnectStatusCh, ok := ChannelRegistry["platform_connect_status"]
	if !ok {
		return fmt.Errorf("failed to retrieve platform_connect_status channel from registry")
	}

	if t.ClientId == "" || t.ClientSecret == "" {
		return errors.New("client credentials not set")
	}

	msgReq := &gen_messages.ProtoOAApplicationAuthReq{
		ClientId:     &t.ClientId,
		ClientSecret: &t.ClientSecret,
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

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send auth request: %w", err)
	}

	<-platformConnectStatusCh

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
func (t *CTrader) AuthorizeAccount() error {
	t.mutex.Lock()
	if !t.readyForAccount {
		t.mutex.Unlock()
		return errors.New("application not yet authorized")
	}
	t.mutex.Unlock()

	if t.AccountId == nil {
		return errors.New("account id cannot be nil")
	}

	if len(strconv.FormatInt(*t.AccountId, 10)) < 8 {
		return errors.New("invalid Account id")
	}

	msgReq := &gen_messages.ProtoOAAccountAuthReq{
		CtidTraderAccountId: t.AccountId,
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

// GetRefreshToken is a request to refresh the access token using refresh token of granted trader's account.
func (t *CTrader) GetRefreshToken() error {
	refreshToken, err := t.accDb.Get("ctrader", "refresh_token")
	if err != nil {
		return err
	}
	refreshTokenStr := string(refreshToken)
	msgReq := &gen_messages.ProtoOARefreshTokenReq{
		RefreshToken: &refreshTokenStr,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth refresh request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.RefreshTokenMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_REFRESH_TOKEN,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send refresh token request: %w", err)
	}
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

// GetAccountHistoricalDeals is a request for getting Trader's deals historical data (execution details).
func (t *CTrader) GetAccountHistoricalDeals(ctx context.Context, fromTimestamp, toTimestamp int64) error {
	if t.AccountId == nil {
		return errors.New("account id cannot be nil")
	}

	if len(strconv.FormatInt(*t.AccountId, 10)) < 8 {
		return errors.New("invalid account id")
	}

	msgReq := &gen_messages.ProtoOADealListReq{
		CtidTraderAccountId: t.AccountId,
		FromTimestamp:       &fromTimestamp,
		ToTimestamp:         &toTimestamp,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth request: %w", err)
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

func (t *CTrader) handleApplicationAuthResponse(ctx context.Context, payload []byte) error {
	var r gen_messages.ProtoOAApplicationAuthRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal auth response: %w", err)
	}

	t.mutex.Lock()
	t.readyForAccount = true
	t.mutex.Unlock()

	return t.AuthorizeAccount()
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
	var r gen_messages.ProtoOAAccountAuthRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal account auth response: %w", err)
	}
	platformConnectStatusCh, ok := ChannelRegistry["platform_connect_status"]
	if !ok {
		return fmt.Errorf("failed to retrieve platform_connect_status channel from registry")
	}

	connectStatusB, err := json.Marshal(PlatformConnectionStatus{
		Authorized: false,
	})
	if err != nil {
		return err
	}
	platformConnectStatusCh <- connectStatusB

	return nil
}

func (t *CTrader) handleRefreshTokenResponse(ctx context.Context, payload []byte) error {
	var r gen_messages.ProtoOARefreshTokenRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal refresh token response: %w", err)
	}
	err := t.accDb.Put("ctrader", "refresh_token", *r.AccessToken)
	if err != nil {
		return err
	}
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

	msg := utils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeTraderInfo, t.AccountConnClient.ID, traderInfoB)
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
		Ctid:     t.AccountId,
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

	trendBarsB, err := json.Marshal(mappedTrendBars)
	if err != nil {
		log.Printf("Failed to marshal scaled trend bars: %v", err)
		return
	}

	msg := utils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeTrendBars, t.AccountConnClient.ID, trendBarsB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal final message: %v", err)
		return
	}

	t.AccountConnClient.Send <- msgB
}

func (t *CTrader) handleSymbolListResponse(ctx context.Context, payload []byte) error {
	var r gen_messages.ProtoOASymbolsListRes
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
	symsB, err := json.Marshal(syms)
	if err != nil {
		log.Printf("Failed to marshal symbol list data: %v", err)
		return err
	}

	msg := utils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeAccountSymbols, t.AccountConnClient.ID, symsB)
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
		msg := utils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeHistorical, t.AccountConnClient.ID, dealsB)
		msgB, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		t.AccountConnClient.Send <- msgB
		return nil
	}
	return fmt.Errorf("failed to find pending request for msg id: %v", common.REQ_ACCOUNT_HISTORICAL_DEALS)
}

func (t *CTrader) handleSymbolsByIdResponse(ctx context.Context, payload []byte) error {
	var r gen_messages.ProtoOASymbolByIdRes
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal symbols info: %w", err)
	}

	if rqts, exists := t.pendingRequests[common.REQ_SYMBOL_INFO]; exists {
		resp := &pendingResponse{
			Payload: payload,
			err:     nil,
		}
		rqts.respCh <- resp
		delete(t.pendingRequests, common.REQ_SYMBOL_INFO)
	}

	return nil
}

func (t *CTrader) handleErrorReponse(ctx context.Context, payload []byte) error {
	var r gen_messages.ProtoOAErrorRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal error response: %w", err)
	}

	log.Printf("Received an error response: %s  with error code: %s", string(*r.Description), r.GetErrorCode())
	if r.GetErrorCode() == common.REQ_CLIENT_FAILURE {
		connectStatusB, err := json.Marshal(PlatformConnectionStatus{
			Authorized: false,
		})
		if err != nil {
			return err
		}
		t.AccountConnClient.Send <- connectStatusB
	}

	pErr := mappers.ProtoOAErrorResToError(&r)
	pErrB, err := json.Marshal(pErr)
	if err != nil {
		return fmt.Errorf("failed to marshal error response: %s", err)
	}
	msg := utils.CreateErrorResponse(t.AccountConnClient.ID, pErrB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	t.AccountConnClient.Send <- msgB

	return nil
}
