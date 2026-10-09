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
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	acount_connect_messages "account-connect/internal/messages"

	messageutils "account-connect/internal/accountconnectmessageutils"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	MESSAGE_TYPE             = websocket.BinaryMessage
	ctraderHeartbeatInterval = 10 * time.Second
	ctraderPriceScale        = 100000.0
)

type MessageHandler func(ctx context.Context, clientMsgId string, payload []byte) error

type pendingResponse struct {
	Payload []byte
	err     error
}

type appauthcredentials struct {
	ClientSecret string
	ClientId     string
	AccessToken  string
}

type SymCategory struct {
	name         string
	assetClassId int64
}

type AssetInfo struct {
	AssetId     int64
	Name        string
	DisplayName string
	Digits      int32
}

type pendingRequest struct {
	ctx         context.Context
	respCh      chan *pendingResponse
	err         error
	ctid        *int64
	symbolId    int64
	isReconnect bool
	silent      bool
	symbolName  string
	period      string
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

	candleBuilders   map[int64][]*candleBuilder // keyed by SymbolId
	symbolDigits     map[int64]int32            // cached scale, keyed by SymbolId
	builderMutex     sync.Mutex
	reqSeq           uint64
	tickListeners    map[int64][]chan []byte
	writeMu          sync.Mutex
	symbolCategories map[int64]*SymCategory // categoryId -> SymCategory
	assetClasses     map[int64]string       // assetClassId -> name
	categoryMutex    sync.Mutex             // guards both maps — see note below
	accountEnvs      map[int64]bool         // ctid -> Live (true = live account, false = demo)
	accountEnvMu     sync.Mutex
	currentEnv       string
	connMu           sync.Mutex         // guards PlatformConn + its lifecycle during reconnects
	connCancel       context.CancelFunc // stops the *current* connection's reader/heartbeat goroutines
	clientId         string             // stored so re-dials can re-run AuthorizeApplication without the caller re-supplying creds
	clientSecret     string
	hasConnectedOnce bool
	symbolNameToId   map[int64]map[string]int64 // ctid -> symbol name -> symbol id
	symbolMu         sync.Mutex
	assetsByCtid     map[int64]map[int64]*AssetInfo // ctid -> assetId -> AssetInfo
	assetInfoMu      sync.Mutex
}

func (t *CTrader) nextReqId(prefix string) string {
	n := atomic.AddUint64(&t.reqSeq, 1)
	return fmt.Sprintf("%s_%d", prefix, n)
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
		tickListeners:     make(map[int64][]chan []byte),
		symbolDigits:      make(map[int64]int32),
		symbolCategories:  make(map[int64]*SymCategory),
		assetClasses:      make(map[int64]string),
		accountEnvs:       make(map[int64]bool),
		symbolNameToId:    make(map[int64]map[string]int64),
		assetsByCtid:      make(map[int64]map[int64]*AssetInfo),
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
	t.RegisterHandler(uint32(common.TickDataRes), t.handleTickDataResponse)
	t.RegisterHandler(uint32(common.SymbolCategoryListRes), t.handleSymbolCategoryListResponse)
	t.RegisterHandler(uint32(common.AssetClassListRes), t.handleAssetClassListResponse)
	t.RegisterHandler(uint32(common.AssetListRes), t.handleAssetListResponse)
}

func (t *CTrader) currentEnvUnsafe() string {
	return t.currentEnv
}

func (t *CTrader) cacheSymbolNames(ctid int64, syms []acount_connect_messages.AccountConnectSymbol) {
	t.symbolMu.Lock()
	defer t.symbolMu.Unlock()

	m, ok := t.symbolNameToId[ctid]
	if !ok {
		m = make(map[string]int64)
		t.symbolNameToId[ctid] = m
	}

	for _, s := range syms {
		if s.Ctrader == nil {
			continue
		}
		m[s.Ctrader.SymbolName] = s.Ctrader.SymbolId
	}
}

func (t *CTrader) cacheAssets(ctid int64, assets []*gen_messages.ProtoOAAsset) {
	t.assetInfoMu.Lock()
	defer t.assetInfoMu.Unlock()

	m, ok := t.assetsByCtid[ctid]
	if !ok {
		m = make(map[int64]*AssetInfo)
		t.assetsByCtid[ctid] = m
	}

	for _, a := range assets {
		if a.AssetId == nil || a.Name == nil {
			continue
		}
		info := &AssetInfo{
			AssetId: *a.AssetId,
			Name:    *a.Name,
		}
		if a.DisplayName != nil {
			info.DisplayName = *a.DisplayName
		}
		if a.Digits != nil {
			info.Digits = *a.Digits
		}
		m[*a.AssetId] = info
	}
}

func (t *CTrader) getAssetInfo(ctid int64, assetId int64) (*AssetInfo, bool) {
	t.assetInfoMu.Lock()
	defer t.assetInfoMu.Unlock()
	info, ok := t.assetsByCtid[ctid][assetId]
	return info, ok
}

func (t *CTrader) getCategoryAndAssetClass(categoryId int64) (categoryName string, assetClassName string, ok bool) {
	t.categoryMutex.Lock()
	defer t.categoryMutex.Unlock()

	cat, exists := t.symbolCategories[categoryId]
	if !exists || cat == nil {
		return "", "", false
	}

	assetClassName = t.assetClasses[cat.assetClassId]
	return cat.name, assetClassName, true
}

func (t *CTrader) resolveSymbolId(ctx context.Context, ctid *int64, symbolName string) (int64, error) {
	if ctid == nil {
		return 0, fmt.Errorf("ctid is required to resolve symbol name %q", symbolName)
	}

	t.symbolMu.Lock()
	id, ok := t.symbolNameToId[*ctid][symbolName]
	t.symbolMu.Unlock()
	if ok {
		return id, nil
	}

	if err := t.fetchAndCacheSymbolList(ctx, ctid); err != nil {
		return 0, fmt.Errorf("failed to resolve symbol %q: %w", symbolName, err)
	}

	t.symbolMu.Lock()
	id, ok = t.symbolNameToId[*ctid][symbolName]
	t.symbolMu.Unlock()

	if !ok {
		return 0, fmt.Errorf("unknown symbol name %q for account %d", symbolName, *ctid)
	}
	return id, nil
}

func (t *CTrader) fetchAndCacheSymbolList(ctx context.Context, ctid *int64) error {
	msgReq := &gen_messages.ProtoOASymbolsListReq{CtidTraderAccountId: ctid}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal symbol list request: %w", err)
	}

	reqKey := t.nextReqId(common.REQ_SYMBOL_LIST)
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountSymbolListMsgType,
		Payload:     msgB,
		ClientMsgId: &reqKey,
	}
	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	req := &pendingRequest{ctx: ctx, respCh: make(chan *pendingResponse, 1), silent: true}
	t.mutex.Lock()
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	if err := t.safeWriteMessage(protoMessage); err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
		t.mutex.Unlock()
		return fmt.Errorf("failed to send symbol list request: %w", err)
	}

	resp := <-req.respCh
	if resp.err != nil {
		return resp.err
	}

	var r gen_messages.ProtoOASymbolsListRes
	if err := proto.Unmarshal(resp.Payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal symbol list: %w", err)
	}

	syms := mappers.ProtoSymbolListResponseToAccountConnectSymbol(&r)
	t.cacheSymbolNames(*ctid, syms)
	return nil
}

// connectToEnvironment (re)establishes PlatformConn against the given
// environment ("demo" or "live").
func (t *CTrader) connectToEnvironment(ctx context.Context, env string) error {
	t.connMu.Lock()
	defer t.connMu.Unlock()

	if t.currentEnv == env && t.PlatformConn != nil {
		return nil
	}

	isReconnect := t.hasConnectedOnce

	// tear down the existing connection, if any
	if t.connCancel != nil {
		t.connCancel()
	}
	if t.PlatformConn != nil {
		t.PlatformConn.Close()
	}

	endpoint, port, err := config.EndpointForEnvironment(env)
	if err != nil {
		return err
	}
	if endpoint == "" || port == 0 {
		return fmt.Errorf("missing endpoint configuration for environment %q", env)
	}

	dialer := websocket.DefaultDialer
	dialer.EnableCompression = true
	dialer.HandshakeTimeout = 10 * time.Second

	url := fmt.Sprintf("wss://%s:%d", endpoint, port)
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to dial %s environment: %w", env, err)
	}

	connCtx, cancel := context.WithCancel(ctx)
	t.PlatformConn = conn
	t.connCancel = cancel
	t.currentEnv = env

	t.mutex.Lock()
	t.readyForAccount = false
	t.mutex.Unlock()

	t.registerHandlers()
	go t.StartConnectionReader(connCtx)
	go t.startHeartbeatTicker(connCtx)

	if err := t.AuthorizeApplication(connCtx, appauthcredentials{
		ClientId:     t.clientId,
		ClientSecret: t.clientSecret,
	}, isReconnect); err != nil {
		return fmt.Errorf("failed to re-authorize application on %s: %w", env, err)
	}

	select {
	case <-t.authCompleted:
		t.hasConnectedOnce = true
		return nil
	case <-connCtx.Done():
		return fmt.Errorf("context cancelled while waiting for application auth on %s", env)
	case <-time.After(15 * time.Second):
		return fmt.Errorf("timed out waiting for application auth on %s", env)
	}
}

func (cta *CtraderAdapter) EstablishConnection(ctx context.Context, cfg config.PlatformConfigs) error {
	if err := cta.ctrader.EstablishCtraderConnection(ctx, config.CtraderConfig{
		ClientId:     cfg.Ctrader.ClientId,
		ClientSecret: cfg.Ctrader.ClientSecret,
	}); err != nil {
		log.Printf("Connection establishment to ctrader fail: %v", err)
		return err
	}
	return nil
}

func (cta *CtraderAdapter) AuthorizeAccount(ctx context.Context, payload acount_connect_messages.AccountConnectAuthorizeTradingAccountPayload) error {
	return cta.ctrader.AuthorizeAccount(ctx, payload.AccountId)
}

func (cta *CtraderAdapter) GetUserAccounts(ctx context.Context) error {
	return nil
}

func (cta *CtraderAdapter) GetTradingSymbols(ctx context.Context, payload acount_connect_messages.AccountConnectSymbolsPayload) error {
	//Add additional check if the ctid is a valid ctid
	return cta.ctrader.GetAccountTradingSymbols(ctx, payload.AccountID)
}

func (cta *CtraderAdapter) GetHistoricalTrades(ctx context.Context, payload acount_connect_messages.AccountConnectHistoricalDealsPayload) error {
	if payload.Ctid == nil {
		return fmt.Errorf("ctid is required for historical deals")
	}
	return cta.ctrader.GetAccountHistoricalDeals(ctx, payload)
}

func (cta *CtraderAdapter) GetAccountInfo(ctx context.Context, payload acount_connect_messages.AccountConnectAccountInfoPayload) error {
	return cta.ctrader.GetAccountInfo(ctx, (*int64)(payload.AccountID))
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

func (cta *CtraderAdapter) GetHistoricalTicks(ctx context.Context, payload acount_connect_messages.AccountConnectTickDataPayload) error {
	if payload.Ctrader == nil {
		return fmt.Errorf("ctrader payload is required for tick data")
	}
	if err := cta.requireCtid(payload.Ctrader.Ctid); err != nil {
		return err
	}
	return cta.ctrader.GetAccountHistoricalTicks(ctx, *payload.Ctrader)
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

func (cta *CtraderAdapter) GetCandlestickStream(ctx context.Context, payload acount_connect_messages.AccountConnectCandlestickStreamPayload) error {
	if payload.AccountID == 0 {
		return fmt.Errorf("account_id is required for ctrader candlestick stream")
	}
	if payload.SymbolName == "" {
		return fmt.Errorf("symbol_name is required for ctrader candlestick stream")
	}
	if payload.Period == "" {
		return fmt.Errorf("period is required for ctrader candlestick stream")
	}

	if _, err := mappers.PeriodStrToDuration(payload.Period); err != nil {
		return fmt.Errorf("invalid period: %w", err)
	}

	streamId := fmt.Sprintf("candlestick_ctrader_%d_%s", payload.AccountID, payload.SymbolName)
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

	return cta.ctrader.StartCandlestickStream(ctx, payload, stream)
}

func (cta *CtraderAdapter) GetTickStream(ctx context.Context, payload acount_connect_messages.AccountConnectTickDataPayload) error {
	if payload.Ctrader == nil {
		return fmt.Errorf("ctrader payload is required for tick stream")
	}
	cp := payload.Ctrader
	if err := cta.requireCtid(cp.Ctid); err != nil {
		return err
	}
	if cp.SymbolId == 0 {
		return fmt.Errorf("symbol_id is required for tick stream")
	}

	streamId := fmt.Sprintf("ticks_ctrader_%d", cp.SymbolId)
	if err := cta.ctrader.AccountConnClient.AddStream(ctx, streamId); err != nil {
		return err
	}
	stream := cta.ctrader.AccountConnClient.Streams[streamId]

	go func() {
		for tickB := range stream {
			msg := messageutils.CreateSuccessResponse(ctx, acount_connect_messages.TypeLiveTicks, acount_connect_messages.Ctrader, cta.ctrader.AccountConnClient.ID, tickB)
			msgB, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Failed to marshal tick stream message: %v", err)
				continue
			}
			cta.ctrader.AccountConnClient.Send <- msgB
		}
	}()

	return cta.ctrader.StartTickStream(ctx, cp.Ctid, cp.SymbolId, stream)
}

func (cta *CtraderAdapter) GetOrderBookDepth(ctx context.Context, payload acount_connect_messages.AccountConnectDepthPayload) error {
	return fmt.Errorf("order book depth not yet supported for ctrader")
}
func (cta *CtraderAdapter) GetDepthStream(ctx context.Context, payload acount_connect_messages.AccountConnectDepthPayload) error {
	return fmt.Errorf("live depth stream not yet supported for ctrader")
}

func (cta *CtraderAdapter) GetBBOStream(ctx context.Context, payload acount_connect_messages.AccountConnectBBOPayload) error {
	return fmt.Errorf("bbo stream not yet supported for ctrader")
}

func (cta *CtraderAdapter) Disconnect(ctx context.Context) error {
	return cta.ctrader.DisconnectPlatformConn()
}

func (t *CTrader) EstablishCtraderConnection(ctx context.Context, ctraderConfig config.CtraderConfig) error {
	if ctraderConfig.ClientId == "" || ctraderConfig.ClientSecret == "" {
		return fmt.Errorf("missing cTrader client credentials")
	}
	t.clientId = ctraderConfig.ClientId
	t.clientSecret = ctraderConfig.ClientSecret

	return t.connectToEnvironment(ctx, "demo")
}

// safeWriteMessage serializes all writes to PlatformConn. Gorilla forbids
// concurrent WriteMessage calls; with the heartbeat ticker running
// continuously alongside every request-issuing method, an unsynchronized
func (t *CTrader) safeWriteMessage(data []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.PlatformConn.WriteMessage(MESSAGE_TYPE, data)
}

// startHeartbeatTicker proactively sends a heartbeat to cTrader on a fixed interval
func (t *CTrader) startHeartbeatTicker(ctx context.Context) {
	ticker := time.NewTicker(ctraderHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msgP := &gen_messages.ProtoMessage{
				PayloadType: &common.HeartBeatMsgType,
			}
			protoMessage, err := proto.Marshal(msgP)
			if err != nil {
				log.Printf("failed to marshal heartbeat: %v", err)
				continue
			}

			if err := t.safeWriteMessage(protoMessage); err != nil {
				log.Printf("failed to send heartbeat, ctrader connection likely dead: %v", err)
				return
			}
		}
	}
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
			if err := handler(ctx, msgP.GetClientMsgId(), msgP.Payload); err != nil {
				log.Printf("Handler error for type %d: %v", msgP.GetPayloadType(), err)
			}

		} else {
			log.Printf("No handler for message type %d", msgP.GetPayloadType())
		}
	}
}

// AuthorizeApplication is a request  authorizing an application to work with the cTrader platform Proxies.
func (t *CTrader) AuthorizeApplication(ctx context.Context, creds appauthcredentials, isReconnect bool) error {
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
		ctx:         ctx,
		respCh:      make(chan *pendingResponse, 1),
		isReconnect: isReconnect,
	}

	t.mutex.Lock()
	t.pendingRequests[common.REQ_ACCOUNT_LIST] = req
	t.mutex.Unlock()

	err = t.safeWriteMessage(protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, common.REQ_ACCOUNT_LIST)
		t.mutex.Unlock()
		return fmt.Errorf("failed to send auth request: %w", err)
	}

	return nil

}

func (t *CTrader) DisconnectPlatformConn() error {
	t.connMu.Lock()
	defer t.connMu.Unlock()

	if t.connCancel != nil {
		t.connCancel()
		t.connCancel = nil
	}
	if t.PlatformConn != nil {
		err := t.PlatformConn.Close()
		t.PlatformConn = nil
		return err
	}
	return fmt.Errorf("close failed for nil ctrader platform connection")
}

func (t *CTrader) AuthorizeAccount(ctx context.Context, accountId *int64) error {
	if accountId == nil {
		return errors.New("account id cannot be nil")
	}
	if len(strconv.FormatInt(*accountId, 10)) < 8 {
		return errors.New("invalid Account id")
	}

	t.accountEnvMu.Lock()
	isLive, known := t.accountEnvs[*accountId]
	t.accountEnvMu.Unlock()
	if !known {
		return fmt.Errorf("account id %d not found in fetched account list — call connect first", *accountId)
	}

	wantEnv := "demo"
	if isLive {
		wantEnv = "live"
	}

	if err := t.connectToEnvironment(ctx, wantEnv); err != nil {
		return fmt.Errorf("failed to connect to %s environment for account %d: %w", wantEnv, *accountId, err)
	}

	t.mutex.Lock()
	ready := t.readyForAccount
	t.mutex.Unlock()
	if !ready {
		return errors.New("application not yet authorized")
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

	return t.safeWriteMessage(protoMessage)
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

	err = t.safeWriteMessage(protoMessage)
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

// GetAccountInfo will retrieve the trader's information for the specified ctidTraderAccountId
func (t *CTrader) GetAccountInfo(ctx context.Context, ctidTraderAccountId *int64) error {
	msgReq := &gen_messages.ProtoOATraderReq{
		CtidTraderAccountId: ctidTraderAccountId,
	}

	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal prototrader  request: %w", err)
	}

	reqKey := t.nextReqId(common.REQ_TRADER_INFO)

	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.TraderInfoMsgType,
		Payload:     msgB,
		ClientMsgId: &reqKey,
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.safeWriteMessage(protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
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

	reqKey := t.nextReqId(common.REQ_ACCOUNT_ORDERS)

	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountReconcileReq,
		Payload:     msgB,
		ClientMsgId: &reqKey,
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.safeWriteMessage(protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
		t.mutex.Unlock()
		return fmt.Errorf("failed to get account orders: %w", err)
	}
	return nil

}

func (t *CTrader) StartCandlestickStream(ctx context.Context, payload acount_connect_messages.AccountConnectCandlestickStreamPayload, strm chan []byte) error {
	ctid := int64(payload.AccountID)

	symbolId, err := t.resolveSymbolId(ctx, &ctid, payload.SymbolName)
	if err != nil {
		return err
	}

	digits, err := t.getSymbolDigits(ctx, &ctid, symbolId)
	if err != nil {
		return fmt.Errorf("failed to fetch symbol digits: %w", err)
	}

	builder, err := newCandleBuilder(symbolId, payload.SymbolName, payload.Period, digits, strm)
	if err != nil {
		return fmt.Errorf("invalid period: %w", err)
	}

	t.builderMutex.Lock()
	existingCandles := t.candleBuilders[symbolId]
	existingTicks := t.tickListeners[symbolId]
	spotAlreadySubscribed := len(existingCandles) > 0 || len(existingTicks) > 0
	t.candleBuilders[symbolId] = append(existingCandles, builder)
	t.builderMutex.Unlock()

	if !spotAlreadySubscribed {
		if err := t.subscribeSpots(ctx, &ctid, []int64{symbolId}); err != nil {
			return fmt.Errorf("failed to subscribe to spots: %w", err)
		}
	}

	if err := t.subscribeLiveTrendbar(ctx, &ctid, symbolId, payload.Period); err != nil {
		return fmt.Errorf("failed to subscribe to live trendbar: %w", err)
	}

	return nil
}


// StartTickStream subscribes strm to live bid/ask updates for a symbol.
func (t *CTrader) StartTickStream(ctx context.Context, ctid *int64, symbolId int64, strm chan []byte) error {
	if _, err := t.getSymbolDigits(ctx, ctid, symbolId); err != nil {
		return fmt.Errorf("failed to fetch symbol digits: %w", err)
	}

	t.builderMutex.Lock()
	existingCandles := len(t.candleBuilders[symbolId]) > 0
	existingTicks := len(t.tickListeners[symbolId]) > 0
	t.tickListeners[symbolId] = append(t.tickListeners[symbolId], strm)
	t.builderMutex.Unlock()

	if !existingCandles && !existingTicks {
		if err := t.subscribeSpots(ctx, ctid, []int64{symbolId}); err != nil {
			return fmt.Errorf("failed to subscribe to spots: %w", err)
		}
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

	if err := t.safeWriteMessage(protoMessage); err != nil {
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

	if err := t.safeWriteMessage(protoMessage); err != nil {
		return fmt.Errorf("failed to send subscribe live trendbar request: %w", err)
	}

	return nil
}

// GetAccountTradingSymbols  retrieves a list of trading for the specified ctid
func (t *CTrader) GetAccountTradingSymbols(ctx context.Context, accountId acount_connect_messages.AccountID) error {

	ctid := int64(accountId)
	msgReq := &gen_messages.ProtoOASymbolsListReq{
		CtidTraderAccountId: &ctid,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal symbol list  request: %w", err)
	}

	reqKey := t.nextReqId(common.REQ_SYMBOL_LIST)
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountSymbolListMsgType,
		Payload:     msgB,
		ClientMsgId: &reqKey,
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
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	err = t.safeWriteMessage(protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
		t.mutex.Unlock()
		return fmt.Errorf("failed to send trading account symbols request: %w", err)
	}
	return nil
}

func (t *CTrader) getSymbolListInformation(ctx context.Context, opts acount_connect_messages.AccountConnectSymbolInfoPayload) (string, error) {
	msgReq := &gen_messages.ProtoOASymbolByIdReq{
		CtidTraderAccountId: opts.Ctid,
		SymbolId:            opts.SymbolId,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal symbol list info request: %w", err)
	}

	reqKey := t.nextReqId(common.REQ_SYMBOL_INFO)
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountSymbolInfo,
		Payload:     msgB,
		ClientMsgId: &reqKey,
	}

	req := &pendingRequest{
		ctx:    ctx,
		respCh: make(chan *pendingResponse, 1),
	}

	t.mutex.Lock()
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return "", fmt.Errorf("failed to marshal protobuf message: %w", err)
	}

	if err := t.safeWriteMessage(protoMessage); err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
		t.mutex.Unlock()
		return "", fmt.Errorf("failed to send additional data request: %w", err)
	}

	return reqKey, nil
}

// GetChartTrendBars will request trend bar series data as  requested by [trendbarsArgs]
func (t *CTrader) GetChartTrendBars(ctx context.Context, trendbarsArgs acount_connect_messages.AccountConnectTrendBarsPayload) error {
	ctid := (*int64)(&trendbarsArgs.AccountID)

	symbolId, err := t.resolveSymbolId(ctx, ctid, trendbarsArgs.SymbolName)
	if err != nil {
		return err
	}

	trendPeriod, err := mappers.PeriodStrToBarPeriod(trendbarsArgs.Period)
	if err != nil {
		return fmt.Errorf("invalid trend period: %w", err)
	}

	msgReq := &gen_messages.ProtoOAGetTrendbarsReq{
		CtidTraderAccountId: ctid,
		Period:              &trendPeriod,
		SymbolId:            &symbolId,
		FromTimestamp:       trendbarsArgs.FromTimestamp,
		ToTimestamp:         trendbarsArgs.ToTimestamp,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal trend bars  request: %w", err)
	}

	reqKey := t.nextReqId(common.REQ_TREND_BARS)
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.TrendBarsMsyType,
		Payload:     msgB,
		ClientMsgId: &reqKey,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf message: %w", err)
	}

	req := &pendingRequest{
		ctx:        ctx,
		respCh:     make(chan *pendingResponse, 1),
		symbolName: trendbarsArgs.SymbolName,
		period:     trendbarsArgs.Period,
	}

	t.mutex.Lock()
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	err = t.safeWriteMessage(protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
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

	reqKey := t.nextReqId(common.REQ_ACCOUNT_HISTORICAL_DEALS)
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AccountHistoricalDeals,
		Payload:     msgB,
		ClientMsgId: &reqKey,
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
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	err = t.safeWriteMessage(protoMessage)
	if err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
		t.mutex.Unlock()
		return fmt.Errorf("failed to request account historical trades: %w", err)
	}

	return nil
}

func (t *CTrader) GetAccountHistoricalTicks(ctx context.Context, payload acount_connect_messages.CtraderTickDataRequestPayload) error {
	quoteType, err := mappers.QuoteTypeStrToProto(payload.QuoteType)
	if err != nil {
		return fmt.Errorf("invalid quote_type: %w", err)
	}

	reqKey := t.nextReqId(common.REQ_TICK_DATA)

	msgReq := &gen_messages.ProtoOAGetTickDataReq{
		CtidTraderAccountId: payload.Ctid,
		SymbolId:            &payload.SymbolId,
		Type:                &quoteType,
		FromTimestamp:       payload.FromTimestamp,
		ToTimestamp:         payload.ToTimestamp,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal tick data request: %w", err)
	}
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.TickDataMsgType,
		Payload:     msgB,
		ClientMsgId: &reqKey,
	}
	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	req := &pendingRequest{
		ctx:      ctx,
		respCh:   make(chan *pendingResponse, 1),
		ctid:     payload.Ctid,
		symbolId: payload.SymbolId,
	}

	t.mutex.Lock()
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	if err := t.safeWriteMessage(protoMessage); err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
		t.mutex.Unlock()
		return fmt.Errorf("failed to send tick data request: %w", err)
	}

	return nil
}

func (t *CTrader) GetSymbolCategories(ctx context.Context, ctid *int64) error {
	msgReq := &gen_messages.ProtoOASymbolCategoryListReq{
		CtidTraderAccountId: ctid,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal symbol category list request: %w", err)
	}

	reqKey := t.nextReqId(common.REQ_SYMBOL_CATEGORY_LIST)
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.SymbolCategoryListMsgType,
		Payload:     msgB,
		ClientMsgId: &reqKey,
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
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	if err := t.safeWriteMessage(protoMessage); err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
		t.mutex.Unlock()
		return fmt.Errorf("failed to send symbol category list request: %w", err)
	}

	resp := <-req.respCh
	if resp.err != nil {
		return resp.err
	}

	var catRes gen_messages.ProtoOASymbolCategoryListRes
	if err := proto.Unmarshal(resp.Payload, &catRes); err != nil {
		return fmt.Errorf("failed to unmarshal symbol category list: %w", err)
	}

	t.categoryMutex.Lock()
	for _, cat := range catRes.SymbolCategory {
		if cat.Id != nil && cat.AssetClassId != nil {
			t.symbolCategories[*cat.Id] = &SymCategory{
				assetClassId: *cat.AssetClassId,
				name:         *cat.Name,
			}
		}
	}
	t.categoryMutex.Unlock()

	return nil
}

// GetAssetList fetches and caches the asset list (display names, precision) for ctid.
// Blocking, same pattern as GetAssetClasses/GetSymbolCategories — meant to be preloaded
// once per account auth, not called per-request.
func (t *CTrader) GetAssetList(ctx context.Context, ctid *int64) error {
	msgReq := &gen_messages.ProtoOAAssetListReq{
		CtidTraderAccountId: ctid,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal asset list request: %w", err)
	}

	reqKey := t.nextReqId(common.REQ_ASSET_LIST)
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AssetListMsgType,
		Payload:     msgB,
		ClientMsgId: &reqKey,
	}
	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	req := &pendingRequest{ctx: ctx, respCh: make(chan *pendingResponse, 1)}
	t.mutex.Lock()
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	if err := t.safeWriteMessage(protoMessage); err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
		t.mutex.Unlock()
		return fmt.Errorf("failed to send asset list request: %w", err)
	}

	resp := <-req.respCh
	if resp.err != nil {
		return resp.err
	}

	var r gen_messages.ProtoOAAssetListRes
	if err := proto.Unmarshal(resp.Payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal asset list: %w", err)
	}
	if ctid != nil {
		t.cacheAssets(*ctid, r.Asset)
	}
	return nil
}

func (t *CTrader) GetAssetClasses(ctx context.Context, ctid *int64) error {
	msgReq := &gen_messages.ProtoOAAssetClassListReq{
		CtidTraderAccountId: ctid,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal asset class list request: %w", err)
	}

	reqKey := t.nextReqId(common.REQ_ASSET_CLASS_LIST)
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.AssetClassListMsgType,
		Payload:     msgB,
		ClientMsgId: &reqKey,
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
	t.pendingRequests[reqKey] = req
	t.mutex.Unlock()

	if err := t.safeWriteMessage(protoMessage); err != nil {
		t.mutex.Lock()
		delete(t.pendingRequests, reqKey)
		t.mutex.Unlock()
		return fmt.Errorf("failed to send asset class list request: %w", err)
	}

	resp := <-req.respCh
	if resp.err != nil {
		return resp.err
	}

	var acRes gen_messages.ProtoOAAssetClassListRes
	if err := proto.Unmarshal(resp.Payload, &acRes); err != nil {
		return fmt.Errorf("failed to unmarshal asset class list: %w", err)
	}

	t.categoryMutex.Lock()
	for _, ac := range acRes.AssetClass {
		if ac.Id != nil && ac.Name != nil {
			t.assetClasses[*ac.Id] = *ac.Name
		}
	}
	t.categoryMutex.Unlock()

	return nil
}

// getSymbolDigits fetches and caches the price scale (10^digits) for symbolId, used to convert
// raw integer spot prices into actual decimal prices.
func (t *CTrader) getSymbolDigits(ctx context.Context, ctid *int64, symbolId int64) (int32, error) {
	t.builderMutex.Lock()
	if scale, ok := t.symbolDigits[symbolId]; ok {
		t.builderMutex.Unlock()
		return scale, nil
	}
	t.builderMutex.Unlock()

	reqKey := t.nextReqId(common.REQ_SYMBOL_INFO)

	fmt.Println("AccountId:", ctid)

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

	if err := t.safeWriteMessage(protoMessage); err != nil {
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
	digits := *symbolRes.Symbol[0].Digits

	t.builderMutex.Lock()
	t.symbolDigits[symbolId] = digits
	t.builderMutex.Unlock()

	return digits, nil
}

func (t *CTrader) getTradingAccounts(
	ctx context.Context,
	accessToken string,
) ([]acount_connect_messages.CTraderTradingAccount, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://api.spotware.com/connect/tradingaccounts",
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trading accounts request: %w", err)
	}

	q := req.URL.Query()
	q.Set("access_token", accessToken)
	req.URL.RawQuery = q.Encode()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get trading accounts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"trading accounts request returned status %d",
			resp.StatusCode,
		)
	}

	var result acount_connect_messages.CTraderTradingAccountsResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf(
			"failed to decode trading accounts response: %w",
			err,
		)
	}
	return result.Data, nil
}

func (t *CTrader) handleApplicationAuthResponse(ctx context.Context, clientMsgId string, payload []byte) error {
	var r gen_messages.ProtoOAApplicationAuthRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal auth response: %w", err)
	}

	t.mutex.Lock()
	t.readyForAccount = true
	req, ok := t.pendingRequests[common.REQ_ACCOUNT_LIST]
	t.mutex.Unlock()

	select {
	case t.authCompleted <- true:
	default:
	}

	if !ok {
		return fmt.Errorf("failed to find pending request for account list to authorize")
	}

	if !req.isReconnect {
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
	}

	return t.GetUserAcountListByAccessToken(t.AccessToken)
}

func (t *CTrader) handleAccountListResponse(ctx context.Context, clientMsgId string, payload []byte) error {
	var (
		r gen_messages.ProtoOAGetAccountListByAccessTokenRes
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

	if r.AccessToken == nil || *r.AccessToken == "" {
		return errors.New("account list response missing access token")
	}

	accounts, err := t.getTradingAccounts(ctx, *r.AccessToken)
	if err != nil {
		return fmt.Errorf(
			"failed to retrieve full trading account information: %w",
			err,
		)
	}

	t.accountEnvMu.Lock()
	for _, acc := range accounts {
		t.accountEnvs[acc.AccountID] = acc.Live
	}
	t.accountEnvMu.Unlock()

	tradingAccounts := make(
		[]acount_connect_messages.AccountConnectCtraderTradingAccount,
		0,
		len(accounts),
	)

	for _, account := range accounts {
		tradingAccounts = append(
			tradingAccounts,
			acount_connect_messages.AccountConnectCtraderTradingAccount{
				BrokerName:        account.BrokerName,
				DepositCurrency:   account.DepositCurrency,
				Balance:           account.Balance,
				Leverage:          account.Leverage,
				LeverageInCents:   account.LeverageInCents,
				MoneyDigits:       account.MoneyDigits,
				TraderAccountType: account.TraderAccountType,
				AccountId:         &account.AccountID,
				Live:              account.Live,
			},
		)
	}

	tradingaccountsres := acount_connect_messages.AccountConnectTradingAccountRes{
		CtTradingAccounts: tradingAccounts,
	}

	res := acount_connect_messages.AccountConnectAccountInfoRes{
		CtraderAccounts: &tradingaccountsres,
	}

	resB, err := json.Marshal(res)
	if err != nil {
		return err
	}
	req.respCh <- &pendingResponse{
		Payload: resB,
	}
	return nil
}

func (t *CTrader) handleHeartBeatMessage(ctx context.Context, clientMsgId string, payload []byte) error {
	msgP := &gen_messages.ProtoMessage{
		PayloadType: &common.HeartBeatMsgType,
	}
	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.safeWriteMessage(protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send back a heartbeat message: %w", err)
	}
	return nil
}

func (t *CTrader) handleAccountAuthResponse(ctx context.Context, clientMsgId string, payload []byte) error {
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

	go func() {
		if err := t.GetSymbolCategories(ctx, r.CtidTraderAccountId); err != nil {
			log.Printf("failed to preload symbol categories: %v", err)
		}
	}()
	go func() {
		if err := t.GetAssetClasses(ctx, r.CtidTraderAccountId); err != nil {
			log.Printf("failed to preload asset classes: %v", err)
		}
	}()

	return nil
}

func (t *CTrader) assetClassNameForCategory(categoryId int64) (string, bool) {
	t.categoryMutex.Lock()
	defer t.categoryMutex.Unlock()

	cat, ok := t.symbolCategories[categoryId]
	if !ok || cat == nil {
		return "", false
	}
	return cat.name, true
}

func (t *CTrader) handleRefreshTokenResponse(ctx context.Context, clientMsgId string, payload []byte) error {
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

func (t *CTrader) handleTraderInfoResponse(ctx context.Context, clientMsgId string, payload []byte) error {
	var r gen_messages.ProtoOATraderRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal trader info response: %w", err)
	}
	traderInfo := mappers.ProtoOATraderToaccountConnectTrader(&r)

	res := acount_connect_messages.AccountConnectAccountInfoRes{
		CtraderAccount: &traderInfo,
	}
	resB, err := json.Marshal(res)
	if err != nil {
		return err
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[clientMsgId]
	delete(t.pendingRequests, clientMsgId)
	t.mutex.Unlock()
	if !ok {
		return fmt.Errorf("failed to find pending request for key: %s", clientMsgId)
	}

	msg := messageutils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeTraderInfo, acount_connect_messages.Ctrader, t.AccountConnClient.ID, resB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	t.AccountConnClient.Send <- msgB
	return nil
}

func (t *CTrader) handleTrendBarsResponse(ctx context.Context, clientMsgId string, payload []byte) error {
	var res gen_messages.ProtoOAGetTrendbarsRes
	if err := proto.Unmarshal(payload, &res); err != nil {
		return fmt.Errorf("failed to unmarshal trend bars: %w", err)
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[clientMsgId]
	delete(t.pendingRequests, clientMsgId)
	t.mutex.Unlock()
	if !ok {
		return fmt.Errorf("failed to find pending request for key: %s", clientMsgId)
	}

	if res.SymbolId == nil || res.CtidTraderAccountId == nil {
		return fmt.Errorf("trend bars response missing symbol_id or ctid")
	}

	go func() {
		digits, err := t.getSymbolDigits(req.ctx, res.CtidTraderAccountId, *res.SymbolId)
		if err != nil {
			log.Printf("failed to fetch symbol digits for trend bars: %v", err)
			return
		}
		p := math.Pow(10, float64(digits))
		round := func(v float64) float64 { return math.Round(v*p) / p }

		trendBars := mappers.ProotoOAToTrendBars(&res)

		mappedTrendBars := make([]acount_connect_messages.AccountConnectTrendBar, 0, len(trendBars))
		for _, bar := range trendBars {
			mappedTrendBars = append(mappedTrendBars, acount_connect_messages.AccountConnectTrendBar{
				High:                  round(bar.High / ctraderPriceScale),
				Low:                   round(bar.Low / ctraderPriceScale),
				Close:                 round(bar.Close / ctraderPriceScale),
				Open:                  round(bar.Open / ctraderPriceScale),
				Volume:                bar.Volume,
				UtcTimestampInMinutes: bar.UtcTimestampInMinutes,
			})
		}

		trendBarsRes := acount_connect_messages.AccountConnectTrendBarRes{
			Trendbars: mappedTrendBars,
			Symbol:    req.symbolName,
			Period:    req.period,
		}
		trendBarsResB, err := json.Marshal(trendBarsRes)
		if err != nil {
			log.Printf("failed to marshal scaled trend bars: %v", err)
			return
		}

		msg := messageutils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeTrendBars, acount_connect_messages.Ctrader, t.AccountConnClient.ID, trendBarsResB)
		msgB, err := json.Marshal(msg)
		if err != nil {
			log.Printf("failed to marshal final message: %v", err)
			return
		}

		t.AccountConnClient.Send <- msgB
	}()

	return nil
}

func (t *CTrader) handleSymbolInfoForTrendBars(trendBars []acount_connect_messages.AccountConnectTrendBar, symInfoKey string) {
	t.mutex.Lock()
	req, ok := t.pendingRequests[symInfoKey]
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

func (t *CTrader) handleSymbolListResponse(ctx context.Context, clientMsgId string, payload []byte) error {
	var r gen_messages.ProtoOASymbolsListRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal symbol list: %w", err)
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[clientMsgId]
	delete(t.pendingRequests, clientMsgId)
	t.mutex.Unlock()
	if !ok {
		return fmt.Errorf("failed to find pending request for key: %s", clientMsgId)
	}

	if req.silent {
		req.respCh <- &pendingResponse{Payload: payload}
		return nil
	}

	syms := mappers.ProtoSymbolListResponseToAccountConnectSymbol(&r)

	if r.CtidTraderAccountId != nil {
		t.cacheSymbolNames(*r.CtidTraderAccountId, syms)
	}

	for i, lightSym := range r.Symbol {
		if lightSym.SymbolCategoryId == nil || syms[i].Ctrader == nil {
			continue
		}

		catName, assetClassName, ok := t.getCategoryAndAssetClass(*lightSym.SymbolCategoryId)
		if ok {
			syms[i].Ctrader.AssetClass = assetClassName

			syms[i].Ctrader.SymbolCategory = catName
		}
	}

	if r.CtidTraderAccountId != nil {
		t.enrichSymbolsWithAssetInfo(*r.CtidTraderAccountId, syms)
	}

	symbolIds := make([]int64, 0, len(syms))
	for _, s := range syms {
		if s.Ctrader == nil {
			continue
		}
		symbolIds = append(symbolIds, s.Ctrader.SymbolId)
	}
	if len(symbolIds) == 0 {
		return t.sendSymbolListResponse(req.ctx, syms)
	}

	symInfoKey, err := t.getSymbolListInformation(req.ctx, acount_connect_messages.AccountConnectSymbolInfoPayload{
		SymbolId: symbolIds,
		Ctid:     r.CtidTraderAccountId,
	})
	if err != nil {
		return err
	}

	go func() { t.handleSymbolInfoForSymbolList(req.ctx, syms, symInfoKey) }()
	return nil
}

// enrichSymbolsWithAssetInfo attaches base/quote asset display names to each
// symbol from the cached asset list.
func (t *CTrader) enrichSymbolsWithAssetInfo(ctid int64, syms []acount_connect_messages.AccountConnectSymbol) {
	for i := range syms {
		if syms[i].Ctrader == nil {
			continue
		}
		cs := syms[i].Ctrader

		if base, ok := t.getAssetInfo(ctid, cs.BaseAssetId); ok {
			cs.BaseAsset = &acount_connect_messages.CtraderAsset{
				AssetId:     base.AssetId,
				Name:        base.Name,
				DisplayName: base.DisplayName,
			}
		}
		if quote, ok := t.getAssetInfo(ctid, cs.QuoteAssetId); ok {
			cs.QuoteAsset = &acount_connect_messages.CtraderAsset{
				AssetId:     quote.AssetId,
				Name:        quote.Name,
				DisplayName: quote.DisplayName,
			}
		}
	}
}

func (t *CTrader) handleAssetListResponse(ctx context.Context, clientMsgId string, payload []byte) error {
	t.mutex.Lock()
	req, ok := t.pendingRequests[clientMsgId]
	delete(t.pendingRequests, clientMsgId)
	t.mutex.Unlock()
	if !ok {
		return fmt.Errorf("failed to find pending request for key: %s", clientMsgId)
	}
	req.respCh <- &pendingResponse{Payload: payload, err: nil}
	return nil
}

func (t *CTrader) handleSymbolInfoForSymbolList(ctx context.Context, syms []acount_connect_messages.AccountConnectSymbol, symInfoKey string) {
	t.mutex.Lock()
	req, ok := t.pendingRequests[symInfoKey]
	t.mutex.Unlock()
	if !ok {
		return
	}

	resp := <-req.respCh
	if resp.err != nil {
		log.Printf("failed to enrich symbol list: %v", resp.err)
		return
	}

	var symbolRes gen_messages.ProtoOASymbolByIdRes
	if err := proto.Unmarshal(resp.Payload, &symbolRes); err != nil {
		log.Printf("failed to unmarshal symbol info for enrichment: %v", err)
		return
	}

	// index by symbol id for O(1) merge
	details := make(map[int64]*gen_messages.ProtoOASymbol, len(symbolRes.Symbol))
	for _, s := range symbolRes.Symbol {
		if s.SymbolId != nil {
			details[*s.SymbolId] = s
		}
	}

	for i := range syms {
		if syms[i].Ctrader == nil {
			continue 
		}
		d, ok := details[syms[i].Ctrader.SymbolId]
		if !ok || d.Digits == nil {
			continue
		}
		syms[i].Ctrader.Digits = d.Digits
	}

	t.sendSymbolListResponse(ctx, syms)
}

func (t *CTrader) sendSymbolListResponse(ctx context.Context, syms []acount_connect_messages.AccountConnectSymbol) error {
	accconnectsyms := acount_connect_messages.AccountConnectSymbolRes{AccountConnectSymbols: syms}
	accconnectsymsB, err := json.Marshal(accconnectsyms)
	if err != nil {
		return fmt.Errorf("failed to marshal symbol list data: %w", err)
	}
	msg := messageutils.CreateSuccessResponse(ctx, acount_connect_messages.TypeAccountSymbols, acount_connect_messages.Ctrader, t.AccountConnClient.ID, accconnectsymsB)
	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	t.AccountConnClient.Send <- msgB
	return nil
}

func (t *CTrader) handleAccountHistoricalDeals(ctx context.Context, clientMsgId string, payload []byte) error {
	var r gen_messages.ProtoOADealListRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal historical deals: %w", err)
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[clientMsgId]
	delete(t.pendingRequests, clientMsgId)
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
	return fmt.Errorf("failed to find pending request for msg id: %v", clientMsgId)
}

func (t *CTrader) handleAccountReconcileRes(ctx context.Context, clientMsgId string, payload []byte) error {

	var r gen_messages.ProtoOAReconcileRes

	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal historical deals: %w", err)
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[clientMsgId]
	delete(t.pendingRequests, clientMsgId)
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
	return fmt.Errorf("failed to find pending request for msg id: %v", clientMsgId)

}

func (t *CTrader) handleSymbolsByIdResponse(ctx context.Context, clientMsgId string, payload []byte) error {
	var r gen_messages.ProtoOASymbolByIdRes
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal symbols info: %w", err)
	}

	if rqts, exists := t.pendingRequests[clientMsgId]; exists {
		rqts.respCh <- &pendingResponse{Payload: payload, err: nil}
		delete(t.pendingRequests, clientMsgId)
	}

	return nil
}

func (t *CTrader) handleTickDataResponse(ctx context.Context, clientMsgId string, payload []byte) error {
	var r gen_messages.ProtoOAGetTickDataRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal tick data: %w", err)
	}

	t.mutex.Lock()
	req, ok := t.pendingRequests[clientMsgId]
	delete(t.pendingRequests, clientMsgId)
	t.mutex.Unlock()

	if !ok {
		return fmt.Errorf("failed to find pending request for key: %s", clientMsgId)
	}

	go func() {
		digits, err := t.getSymbolDigits(req.ctx, req.ctid, req.symbolId)
		if err != nil {
			log.Printf("failed to fetch symbol digits for tick decode: %v", err)
			return
		}
		roundFactor := math.Pow(10, float64(digits))
		round := func(v float64) float64 { return math.Round(v*roundFactor) / roundFactor }

		ticks := make([]acount_connect_messages.AccountConnectTick, 0, len(r.TickData))
		var lastTimestamp, lastPrice int64
		for i, td := range r.TickData {
			if td.Timestamp == nil || td.Tick == nil {
				continue
			}
			if i == 0 {
				lastTimestamp, lastPrice = *td.Timestamp, *td.Tick
			} else {
				lastTimestamp += *td.Timestamp
				lastPrice += *td.Tick
			}
			ticks = append(ticks, acount_connect_messages.AccountConnectTick{
				Timestamp: lastTimestamp,
				Price:     round(float64(lastPrice) / ctraderPriceScale),
			})
		}

		res := acount_connect_messages.AccountConnectTickDataRes{
			Ticks:   ticks,
			HasMore: r.GetHasMore(),
			Symbol:  req.symbolId,
		}
		resB, err := json.Marshal(res)
		if err != nil {
			log.Printf("failed to marshal tick data response: %v", err)
			return
		}

		msg := messageutils.CreateSuccessResponse(req.ctx, acount_connect_messages.TypeHistoricalTicks, acount_connect_messages.Ctrader, t.AccountConnClient.ID, resB)
		msgB, err := json.Marshal(msg)
		if err != nil {
			log.Printf("failed to marshal final message: %v", err)
			return
		}
		t.AccountConnClient.Send <- msgB
	}()

	return nil
}

func (t *CTrader) handleSymbolCategoryListResponse(ctx context.Context, clientMsgId string, payload []byte) error {
	t.mutex.Lock()
	req, ok := t.pendingRequests[clientMsgId]
	delete(t.pendingRequests, clientMsgId)
	t.mutex.Unlock()
	if !ok {
		return fmt.Errorf("failed to find pending request for key: %s", clientMsgId)
	}
	req.respCh <- &pendingResponse{Payload: payload, err: nil}
	return nil
}

func (t *CTrader) handleAssetClassListResponse(ctx context.Context, clientMsgId string, payload []byte) error {
	t.mutex.Lock()
	req, ok := t.pendingRequests[clientMsgId]
	delete(t.pendingRequests, clientMsgId)
	t.mutex.Unlock()
	if !ok {
		return fmt.Errorf("failed to find pending request for key: %s", clientMsgId)
	}
	req.respCh <- &pendingResponse{Payload: payload, err: nil}
	return nil
}

func (t *CTrader) handleSpotEvent(ctx context.Context, clientMsgId string, payload []byte) error {
	var ev gen_messages.ProtoOASpotEvent
	if err := proto.Unmarshal(payload, &ev); err != nil {
		return fmt.Errorf("failed to unmarshal spot event: %w", err)
	}

	if ev.SymbolId == nil {
		return nil
	}

	// candle path
	if len(ev.Trendbar) > 0 {
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
	}

	// live tick path — relay raw bid/ask to any registered tick-stream listeners
	if ev.Bid != nil || ev.Ask != nil {
		t.builderMutex.Lock()
		listeners := t.tickListeners[*ev.SymbolId]
		digits := t.symbolDigits[*ev.SymbolId]
		t.builderMutex.Unlock()

		if len(listeners) > 0 && digits > 0 {
			p := math.Pow(10, float64(digits))
			round := func(v float64) float64 { return math.Round(v*p) / p }

			var bid, ask float64
			if ev.Bid != nil {
				bid = round(float64(*ev.Bid) / ctraderPriceScale)
			}
			if ev.Ask != nil {
				ask = round(float64(*ev.Ask) / ctraderPriceScale)
			}

			tick := acount_connect_messages.AccountConnectTickStreamMsg{
				SymbolId:  *ev.SymbolId,
				Price:     bid,
				Timestamp: time.Now().UnixMilli(),
				Bid:       bid,
				Ask:       ask,
			}

			tickB, err := json.Marshal(tick)
			if err != nil {
				log.Printf("Failed to marshal live tick for symbol %d: %v", *ev.SymbolId, err)
			} else {
				for _, ch := range listeners {
					select {
					case ch <- tickB:
					default:
						log.Printf("Tick stream channel full for symbol %d, dropping update", *ev.SymbolId)
					}
				}
			}
		}
	}

	return nil
}

func (t *CTrader) handleErrorReponse(ctx context.Context, clientMsgId string, payload []byte) error {
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
	digits     int32

	out chan []byte
}

func newCandleBuilder(symbolId int64, symbolName, period string, digits int32, out chan []byte) (*candleBuilder, error) {
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
		digits:        digits,
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

	round := func(v float64) float64 {
		p := math.Pow(10, float64(cb.digits))
		return math.Round(v*p) / p
	}

	low := round(float64(*tb.Low) / ctraderPriceScale)
	open, high, close := low, low, low
	if tb.DeltaOpen != nil {
		open = round((float64(*tb.Low) + float64(*tb.DeltaOpen)) / ctraderPriceScale)
	}
	if tb.DeltaHigh != nil {
		high = round((float64(*tb.Low) + float64(*tb.DeltaHigh)) / ctraderPriceScale)
	}
	if tb.DeltaClose != nil {
		close = round((float64(*tb.Low) + float64(*tb.DeltaClose)) / ctraderPriceScale)
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
