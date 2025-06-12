package applications

import (
	"account-connect/common"
	"account-connect/config"
	messages "account-connect/gen"
	"account-connect/internal/mappers"
	accdb "account-connect/persistence"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	MESSAGE_TYPE = websocket.BinaryMessage
)

type MessageHandler func(payload []byte) error

type CTrader struct {
	accDb           accdb.AccountConnectDb
	ClientSecret    string
	ClientId        string
	AccountId       *int64
	AccessToken     string
	PlatformConn    *websocket.Conn
	handlers        map[uint32]MessageHandler
	authCompleted   chan bool
	readyForAccount bool
	mutex           sync.Mutex
}

type CtraderAdapter struct {
	ctrader CTrader
}

func NewCtraderAdapter(accdb accdb.AccountConnectDb, ctraderconfig *config.CTraderConfig) *CtraderAdapter {
	return &CtraderAdapter{
		ctrader: *NewCTrader(accdb, ctraderconfig),
	}
}

// NewCTrader creates a new trader instance with the required fields to establish a ctrader connection
func NewCTrader(accdb accdb.AccountConnectDb, ctraderconfig *config.CTraderConfig) *CTrader {
	return &CTrader{
		accDb:         accdb,
		ClientId:      ctraderconfig.ClientID,
		AccountId:     &ctraderconfig.AccountID,
		ClientSecret:  ctraderconfig.ClientSecret,
		AccessToken:   ctraderconfig.AccessToken,
		handlers:      make(map[uint32]MessageHandler),
		authCompleted: make(chan bool, 1),
	}
}

// RegisterHandler registers a handler for expected protobuf messages.
func (t *CTrader) RegisterHandler(msgType uint32, handler MessageHandler) {
	t.handlers[msgType] = handler
}

func (t *CTrader) registerHandlers() {
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_APPLICATION_AUTH_RES), t.handleApplicationAuthResponse)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_ACCOUNT_AUTH_RES), t.handleAccountAuthResponse)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_ERROR_RES), t.handleErrorReponse)
	t.RegisterHandler(uint32(messages.ProtoPayloadType_HEARTBEAT_EVENT), t.handleHeartBeatMessage)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_DEAL_LIST_RES), t.handleAccountHistoricalDeals)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_REFRESH_TOKEN_RES), t.handleRefreshTokenResponse)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_TRADER_RES), t.handleTraderInfoResponse)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_GET_TRENDBARS_RES), t.handleTrendBarsResponse)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_SYMBOLS_LIST_RES), t.handleSymbolListResponse)
}

func (cta *CtraderAdapter) EstablishConnection(ctx context.Context, cfg PlatformConfigs) error {
	srvCfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("Failed to load configs in route: %v", err)
		return err
	}

	err = cta.ctrader.EstablishCtraderConnection(config.CTraderConfig{
		ClientID:     cfg.ClientId,
		ClientSecret: cfg.SecretKey,
		Endpoint:     srvCfg.Servers.Ctrader.Endpoint,
		Port:         srvCfg.Servers.Ctrader.Port,
		AccountID:    *cfg.AccountId,
	})
	if err != nil {
		log.Printf("Connection establishment to ctrader fail:%v", err)
		return err
	}
	err = cta.ctrader.AuthorizeApplication()
	if err != nil {
		log.Printf("Failed to authorize application: %v", err)
		return err
	}
	return err
}

func (cta *CtraderAdapter) GetTradingSymbols(ctx context.Context, payload mappers.AccountConnectSymbolsPayload) error {
	//Add additional check if the ctid a valid ctid
	return cta.ctrader.GetAccountTradingSymbols(payload.Ctid)
}

func (cta *CtraderAdapter) GetHistoricalTrades(ctx context.Context, payload mappers.AccountConnectHistoricalDealsPayload) error {
	//Add a check for  validity of the to and from timestamps
	return cta.ctrader.GetAccountHistoricalDeals(*payload.FromTimestamp, *payload.ToTimestamp)
}

func (cta *CtraderAdapter) GetTraderInfo(ctx context.Context, payload mappers.AccountConnectTraderInfoPayload) error {
	return cta.ctrader.GetAccountTraderInfo(payload.Ctid)
}

func (cta *CtraderAdapter) GetSymbolTrendBars(ctx context.Context, payload mappers.AccountConnectTrendBarsPayload) error {
	return cta.ctrader.GetChartTrendBars(payload)
}

func (cta *CtraderAdapter) Disconnect() error {
	return cta.ctrader.DisconnectPlatformConn()
}

// EstablishCtraderConnection  establishes a  new ctrader websocket connection
func (t *CTrader) EstablishCtraderConnection(ctraderConfig config.CTraderConfig) error {
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
	go t.StartConnectionReader()

	return nil
}

// StartConnectionReader will start a goroutine whose work will be to continously read protobuf messages sent by ctrader through
// the PlatformConn
func (t *CTrader) StartConnectionReader() {

	for {
		_, msgB, err := t.PlatformConn.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			return
		}

		var msgP messages.ProtoMessage
		if err := proto.Unmarshal(msgB, &msgP); err != nil {
			log.Printf("Failed to unmarshal protocol message: %v", err)
			continue
		}

		if handler, ok := t.handlers[msgP.GetPayloadType()]; ok {
			if err := handler(msgP.Payload); err != nil {
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
		return fmt.Errorf("Failed to retrieve platform_connect_status channel from registry")
	}

	if t.ClientId == "" || t.ClientSecret == "" {
		return errors.New("client credentials not set")
	}

	msgReq := &messages.ProtoOAApplicationAuthReq{
		ClientId:     &t.ClientId,
		ClientSecret: &t.ClientSecret,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth request: %w", err)
	}
	msgP := &messages.ProtoMessage{
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
	return fmt.Errorf("Close failed for nil ctrader platform connection")
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

	msgReq := &messages.ProtoOAAccountAuthReq{
		CtidTraderAccountId: t.AccountId,
		AccessToken:         &t.AccessToken,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth request: %w", err)
	}
	msgP := &messages.ProtoMessage{
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
	msgReq := &messages.ProtoOARefreshTokenReq{
		RefreshToken: &refreshTokenStr,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth refresh request: %w", err)
	}
	msgP := &messages.ProtoMessage{
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

// GetAccountTraderInfo will retrieve the trader's information for a certain ctidTraderAccountId
func (t *CTrader) GetAccountTraderInfo(ctidTraderAccountId *int64) error {
	msgReq := &messages.ProtoOATraderReq{
		CtidTraderAccountId: ctidTraderAccountId,
	}

	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal prototrader  request: %w", err)
	}
	msgP := &messages.ProtoMessage{
		PayloadType: &common.TraderInfoMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_TRADER_INFO,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to get trader info: %w", err)
	}
	return nil

}

// GetAccountTradingSymbols will retrieve a list of trading symbols(trading pairs e.g EUR/USD) for a certain trading account
func (t *CTrader) GetAccountTradingSymbols(ctId *int64) error {
	msgReq := &messages.ProtoOASymbolsListReq{
		CtidTraderAccountId: ctId,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal symbol list  request: %w", err)
	}
	msgP := &messages.ProtoMessage{
		PayloadType: &common.AccountSymbolListMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_SYMBOL_LIST,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send trend bars request: %w", err)
	}
	return nil
}

// GetChartTrendBars will request trend bar series data as  requested by [trendbarsArgs]
func (t *CTrader) GetChartTrendBars(trendbarsArgs mappers.AccountConnectTrendBarsPayload) error {
	trendPeriod, err := mappers.PeriodStrToBarPeriod(trendbarsArgs.Period)
	if err != nil {
		return fmt.Errorf("invalid trend period: %w", err)
	}
	msgReq := &messages.ProtoOAGetTrendbarsReq{
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
	msgP := &messages.ProtoMessage{
		PayloadType: &common.TrendBarsMsyType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_TREND_BARS,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send trend bars request: %w", err)
	}
	return nil
}

// GetAccountHistoricalDeals is a request for getting Trader's deals historical data (execution details).
func (t *CTrader) GetAccountHistoricalDeals(fromTimestamp, toTimestamp int64) error {
	if t.AccountId == nil {
		return errors.New("account id cannot be nil")
	}

	if len(strconv.FormatInt(*t.AccountId, 10)) < 8 {
		return errors.New("invalid account id")
	}

	msgReq := &messages.ProtoOADealListReq{
		CtidTraderAccountId: t.AccountId,
		FromTimestamp:       &fromTimestamp,
		ToTimestamp:         &toTimestamp,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth request: %w", err)
	}
	msgP := &messages.ProtoMessage{
		PayloadType: &common.AccountHistoricalDeals,
		Payload:     msgB,
		ClientMsgId: &common.REQ_ACCOUNT_HISTORICAL_DEALS,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.PlatformConn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to request account historical trades: %w", err)
	}

	return nil
}

func (t *CTrader) handleApplicationAuthResponse(payload []byte) error {
	var r messages.ProtoOAApplicationAuthRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal auth response: %w", err)
	}

	t.mutex.Lock()
	t.readyForAccount = true
	t.mutex.Unlock()

	return t.AuthorizeAccount()
}

func (t *CTrader) handleHeartBeatMessage(payload []byte) error {
	msgP := &messages.ProtoMessage{
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

func (t *CTrader) handleAccountAuthResponse(payload []byte) error {
	var r messages.ProtoOAAccountAuthRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal account auth response: %w", err)
	}
	platformConnectStatusCh, ok := ChannelRegistry["platform_connect_status"]
	if !ok {
		return fmt.Errorf("Failed to retrieve platform_connect_status channel from registry")
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

func (t *CTrader) handleRefreshTokenResponse(payload []byte) error {
	var r messages.ProtoOARefreshTokenRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal refresh token response: %w", err)
	}
	err := t.accDb.Put("ctrader", "refresh_token", *r.AccessToken)
	if err != nil {
		return err
	}
	return nil
}

func (t *CTrader) handleTraderInfoResponse(payload []byte) error {
	traderInfoCh, ok := ChannelRegistry["trader_info"]
	if !ok {
		return fmt.Errorf("Failed to retrieve trader info channel from registry")
	}
	var r messages.ProtoOATraderRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal trader info response: %w", err)
	}
	traderInfo := mappers.ProtoOATraderToaccountConnectTrader(&r)
	traderInfoB, err := json.Marshal(traderInfo)
	if err != nil {
		log.Printf("Failed to marshal trader info: %v", err)
	}
	traderInfoCh <- traderInfoB

	return nil
}

func (t *CTrader) handleTrendBarsResponse(payload []byte) error {
	trendBarsCh, ok := ChannelRegistry["trend_bars"]
	if !ok {
		return fmt.Errorf("Failed to retrieve trend_bars channel from registry")
	}

	var r messages.ProtoOAGetTrendbarsRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal trend bars: %w", err)
	}
	trendBars := mappers.ProotoOAToTrendBars(&r)

	trendBarsB, err := json.Marshal(trendBars)
	if err != nil {
		log.Printf("Failed to marshal trend bar data info: %v", err)
	}
	trendBarsCh <- trendBarsB

	return nil
}

func (t *CTrader) handleSymbolListResponse(payload []byte) error {
	var r messages.ProtoOASymbolsListRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal symbol list: %w", err)
	}

	accountSymbolsCh, ok := ChannelRegistry["account_symbols"]
	if !ok {
		return fmt.Errorf("Failed to retrieve aacount symbols channel from registry")
	}
	syms := mappers.ProtoSymbolListResponseToAccountConnectSymbol(&r)
	symsB, err := json.Marshal(syms)
	if err != nil {
		log.Printf("Failed to marshal trend bar data info: %v", err)
	}
	accountSymbolsCh <- symsB

	return nil
}

func (t *CTrader) handleAccountHistoricalDeals(payload []byte) error {
	historicalDealsCh, ok := ChannelRegistry["historical_deals"]
	if !ok {
		return fmt.Errorf("Failed to retrieve historical deals from registry")
	}
	var r messages.ProtoOADealListRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal historical deals: %w", err)
	}

	deals := mappers.ProtoOADealToAccountConnectDeal(&r)
	dealsB, err := json.Marshal(deals)
	if err != nil {
		return fmt.Errorf("Failed to marshal deal: %s", err)
	}

	historicalDealsCh <- dealsB

	return nil
}

func (t *CTrader) handleErrorReponse(payload []byte) error {
	var r messages.ProtoOAErrorRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal error response: %w", err)
	}

	errorsCh, ok := ChannelRegistry["errors"]
	if !ok {
		return fmt.Errorf("Failed to retrieve errors channnel from registry")
	}
	platformConnectStatusCh, ok := ChannelRegistry["platform_connect_status"]
	if !ok {
		return fmt.Errorf("Failed to retrieve platform_connect_status channel from registry")
	}

	log.Printf("Received an error response: %s  with error code: %s", string(*r.Description), r.GetErrorCode())
	if r.GetErrorCode() == common.REQ_CLIENT_FAILURE {
		connectStatusB, _ := json.Marshal(PlatformConnectionStatus{
			Authorized: true,
		})
		platformConnectStatusCh <- connectStatusB
	}

	pErr := mappers.ProtoOAErrorResToError(&r)
	pErrB, err := json.Marshal(pErr)
	if err != nil {
		return fmt.Errorf("Failed to marshal error response: %s", err)
	}

	errorsCh <- pErrB

	return nil
}
