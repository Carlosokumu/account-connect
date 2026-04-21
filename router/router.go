package router

import (
	"account-connect/config"
	requestutils "account-connect/internal/accountconnectrequestutils"
	"account-connect/internal/adapters"
	providers "account-connect/internal/providers"

	"account-connect/internal/clients"
	messages "account-connect/internal/messages"
	db "account-connect/persistence"
	"context"
	"encoding/json"
	"fmt"
	"log"
)

type Router struct {
	db      db.AccountConnectDb
	Clients map[string]*clients.AccountConnectClient
}

// NewRouter creates a new Router instance
func NewRouter(accdb db.AccountConnectDb) *Router {
	return &Router{
		Clients: make(map[string]*clients.AccountConnectClient),
		db:      accdb,
	}
}

// Route  routes the different message types from clients to the right handler function
func (r *Router) Route(ctx context.Context, client *clients.AccountConnectClient, msg messages.AccountConnectMsg) error {
	handler := messageHandler{
		router: r,
		client: client,
	}

	switch msg.AccountConnectMessageType {
	case messages.TypeConnect:
		return handler.handleConnect(ctx, client, msg)
	case messages.TypeAuthorizeAccount:
		return handler.handleAccountAuthorize(ctx, msg)
	case messages.TypeHistorical:
		return handler.handleHistoricalDeals(ctx, msg)
	case messages.TypeTraderInfo:
		return handler.handleTraderInfo(ctx, msg)
	case messages.TypeTrendBars:
		return handler.handleTrendBars(ctx, msg)
	case messages.TypeAccountSymbols:
		return handler.handleAccountSymbols(ctx, msg)
	case messages.TypeDisconnect:
		return handler.handleClientDisconnect(ctx)
	case messages.TypeStream:
		return handler.handleClientSubcribeToStream(ctx, msg)
	case messages.TypeCandlestickStream:
		return handler.handleCandlestickStream(ctx, msg)

	default:
		return fmt.Errorf("unknown message type: %s", msg.AccountConnectMessageType)
	}
}

// RequestHistoricalDeals requests  a trader's past trades from the underlying trading platform
func (r *Router) RequestHistoricalDeals(ctx context.Context, platformadapter adapters.ProvidersAdapter, payload json.RawMessage) error {
	var req messages.AccountConnectHistoricalDealsPayload

	err := json.Unmarshal(payload, &req)
	if err != nil {
		log.Printf("Failed to unmarshal trader info payload request: %v", err)
		return err
	}

	err = platformadapter.GetHistoricalTrades(ctx, req)
	if err != nil {
		log.Printf("Failed to fetch account historical deals: %v", err)
		return err
	}
	return nil
}

// AuthorizeAccount performs  any neccessary account-specific authorization if required by the data provider API
func (r *Router) AuthorizeAccount(ctx context.Context, platformadapter adapters.ProvidersAdapter, payload json.RawMessage) error {
	var req messages.AccountConnectAuthorizeTradingAccountPayload
	err := json.Unmarshal(payload, &req)
	if err != nil {
		log.Printf("Failed to unmarshal trader info payload request: %v", err)
		return err
	}

	err = platformadapter.AuthorizeAccount(ctx, req)
	if err != nil {
		log.Printf("Failed to retreive trader info: %v", err)
		return err
	}

	return nil
}

// RequestTraderInfo will request the trader's information if  supported by the trading platform's api
func (r *Router) RequestTraderInfo(ctx context.Context, platformadapter adapters.ProvidersAdapter, payload json.RawMessage) error {
	var req messages.AccountConnectTraderInfoPayload

	err := json.Unmarshal(payload, &req)
	if err != nil {
		log.Printf("Failed to unmarshal trader info payload request: %v", err)
		return err
	}

	err = platformadapter.GetTraderInfo(ctx, req)
	if err != nil {
		log.Printf("Failed to retreive trader info: %v", err)
		return err
	}

	return nil
}

// RequestAccountSymbols will fetch all of the available trading symbols(tradable assets) for a given trading platform
func (r *Router) RequestAccountSymbols(ctx context.Context, platformadapter adapters.ProvidersAdapter, payload json.RawMessage) error {
	var req messages.AccountConnectSymbolsPayload

	err := json.Unmarshal(payload, &req)
	if err != nil {
		return err
	}

	err = platformadapter.GetTradingSymbols(ctx, req)
	if err != nil {
		log.Printf("Failed to retrieve account symbols: %v", err)
		return err
	}

	return nil
}

// RequestTrendBars will request trendbars for a particular symbol(trading pair)
func (r *Router) RequestTrendBars(ctx context.Context, platformadapter adapters.ProvidersAdapter, payload json.RawMessage) error {
	var req messages.AccountConnectTrendBarsPayload

	err := json.Unmarshal(payload, &req)
	if err != nil {
		log.Printf("Failed to unmarshal account connect trend bars requests: %v", err)
		return fmt.Errorf("failed to unmarshal account connect trend bars requests: %w", err)
	}
	trendBarArgs := messages.AccountConnectTrendBarsPayload{
		SymbolId:      req.SymbolId,
		SymbolName:    req.SymbolName,
		Ctid:          req.Ctid,
		Period:        req.Period,
		FromTimestamp: req.FromTimestamp,
		ToTimestamp:   req.ToTimestamp,
	}

	err = platformadapter.GetSymbolTrendBars(ctx, trendBarArgs)
	if err != nil {
		log.Printf("Failed to retrieve chart trend bar: %v", err)
		return err
	}

	return nil
}

// RequestCandlestickStream initializes a real-time candlestick/kline stream for a given symbol and interval
func (r *Router) RequestCandlestickStream(ctx context.Context, platformadapter adapters.ProvidersAdapter, payload json.RawMessage) error {
	var req messages.AccountConnectCandlestickStreamPayload

	err := json.Unmarshal(payload, &req)
	if err != nil {
		log.Printf("Failed to unmarshal candlestick stream request: %v", err)
		return fmt.Errorf("failed to unmarshal candlestick stream request: %w", err)
	}

	err = platformadapter.GetCandlestickStream(ctx, req)
	if err != nil {
		log.Printf("Failed to initialize candlestick stream: %v", err)
		return err
	}
	return nil
}

// InitializeClientStream initializes a stream of messages to be sent through the specified channel
func (r *Router) InitializeClientStream(ctx context.Context, platformadapter adapters.ProvidersAdapter, payload json.RawMessage) error {
	var req messages.AccountConnectStreamPayload

	err := json.Unmarshal(payload, &req)
	if err != nil {
		log.Printf("Failed to unmarshal account-connect stream request: %v", err)
		return fmt.Errorf("failed to unmarshal account connect trend bars requests: %w", err)
	}

	err = platformadapter.InitializeClientStream(ctx, req)
	if err != nil {
		log.Printf("Failed to initialize client's stream: %v", err)
		return err
	}
	return nil
}

// DisconnectPlatformConnection  handles graceful disconnection  of the underlying platform connection for the client
func (r *Router) DisconnectPlatformConnection(ctx context.Context, platformadapter adapters.ProvidersAdapter) error {
	return platformadapter.Disconnect(ctx)
}

func (h *messageHandler) handleConnect(ctx context.Context, accountConnClient *clients.AccountConnectClient, msg messages.AccountConnectMsg) error {
	var (
		err error
	)
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)

	switch msg.Platform {
	case messages.Binance:
		_, err = h.handleBinanceConnect(ctx, accountConnClient, msg.Payload)
	case messages.Ctrader:
		_, err = h.handleCtraderConnect(ctx, accountConnClient, msg.Payload)
	default:
		return fmt.Errorf("unsupported platform: %s", msg.Platform)
	}

	if err != nil {
		return err
	}
	h.router.Clients[h.client.ID] = accountConnClient
	return nil
}

func (h *messageHandler) handleAccountAuthorize(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)

	err := h.getClient(msg)
	if err != nil {
		return err
	}

	platformadapter, err := h.getAdapter(msg)
	if err != nil {
		return err
	}

	if err := h.router.AuthorizeAccount(ctx, platformadapter, payload); err != nil {
		return h.writeErrorResponse(messages.TypeTraderInfo, err)
	}
	return nil
}

// handleBinanceConnect will establish a connection to binance
func (h *messageHandler) handleBinanceConnect(
	ctx context.Context,
	accountConnClient *clients.AccountConnectClient,
	payload json.RawMessage,
) (adapters.ProvidersAdapter, error) {
	var binanceMsg messages.BinanceConnectPayload
	if err := json.Unmarshal(payload, &binanceMsg); err != nil {
		return nil, fmt.Errorf("invalid Binance payload: %w", err)
	}

	adapter := providers.NewBinanceAdapter(accountConnClient)
	if err := adapter.EstablishConnection(ctx, config.PlatformConfigs{}); err != nil {
		return nil, err
	}
	accountConnClient.PlatformConns[messages.Binance] = adapter
	return adapter, nil
}

// handleCtraderConnect will establish a connection to ctrader open api
func (h *messageHandler) handleCtraderConnect(
	ctx context.Context,
	accountConnClient *clients.AccountConnectClient,
	payload json.RawMessage,
) (adapters.ProvidersAdapter, error) {
	var ctraderMsg messages.CTraderConnectPayload
	if err := json.Unmarshal(payload, &ctraderMsg); err != nil {
		return nil, fmt.Errorf("invalid cTrader payload: %w", err)
	}

	cfg := config.CtraderConfig{
		ClientId:     ctraderMsg.ClientId,
		ClientSecret: ctraderMsg.ClientSecret,
		AccessToken:  ctraderMsg.AccessToken,
	}
	adapter := providers.NewCtraderAdapter(h.router.db, accountConnClient, &cfg)
	if err := adapter.EstablishConnection(ctx, config.PlatformConfigs{
		Ctrader: config.CtraderConfig{
			ClientId:     ctraderMsg.ClientId,
			ClientSecret: ctraderMsg.ClientSecret,
			AccessToken:  ctraderMsg.AccessToken,
		},
	}); err != nil {
		return nil, err
	}
	accountConnClient.PlatformConns[messages.Ctrader] = adapter

	return adapter, nil
}

func (h *messageHandler) handleClientDisconnect(ctx context.Context) error {
	var disconnecterr error
	client := h.client

	for _, platformadapter := range client.PlatformConns {
		err := h.router.DisconnectPlatformConnection(ctx, platformadapter)
		if err != nil {
			return err
		}
	}

	return disconnecterr
}

func (h *messageHandler) handleHistoricalDeals(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)

	err := h.getClient(msg)
	if err != nil {
		return err
	}

	platformadapter, err := h.getAdapter(msg)
	if err != nil {
		return err
	}

	return h.router.RequestHistoricalDeals(ctx, platformadapter, payload)
}

func (h *messageHandler) handleTraderInfo(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload

	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)

	err := h.getClient(msg)
	if err != nil {
		return err
	}

	platformadapter, err := h.getAdapter(msg)
	if err != nil {
		return err
	}

	if err := h.router.RequestTraderInfo(ctx, platformadapter, payload); err != nil {
		return h.writeErrorResponse(messages.TypeTraderInfo, err)
	}

	return nil
}

func (h *messageHandler) handleClientSubcribeToStream(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)

	err := h.getClient(msg)
	if err != nil {
		return err
	}

	platformadapter, err := h.getAdapter(msg)
	if err != nil {
		return err
	}

	if err := h.router.InitializeClientStream(ctx, platformadapter, payload); err != nil {
		return h.writeErrorResponse(messages.TypeTraderInfo, err)
	}
	return nil
}

func (h *messageHandler) handleTrendBars(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload

	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)
	err := h.getClient(msg)
	if err != nil {
		return err
	}

	platformadapter, err := h.getAdapter(msg)
	if err != nil {
		return err
	}

	if err := h.router.RequestTrendBars(ctx, platformadapter, payload); err != nil {
		return h.writeErrorResponse(messages.TypeTrendBars, err)
	}
	return nil
}

func (h *messageHandler) handleAccountSymbols(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)

	err := h.getClient(msg)
	if err != nil {
		return err
	}

	platformadapter, err := h.getAdapter(msg)
	if err != nil {
		return err
	}

	if err := h.router.RequestAccountSymbols(ctx, platformadapter, payload); err != nil {
		return h.writeErrorResponse(messages.TypeAccountSymbols, err)
	}
	return nil
}

func (h *messageHandler) handleCandlestickStream(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)

	if err := h.getClient(msg); err != nil {
		return err
	}

	platformadapter, err := h.getAdapter(msg)
	if err != nil {
		return err
	}

	if err := h.router.RequestCandlestickStream(ctx, platformadapter, payload); err != nil {
		return h.writeErrorResponse(messages.TypeCandlestickStream, err)
	}
	return nil
}

func (h *messageHandler) writeErrorResponse(msgType messages.MessageType, err error) error {
	accErr := messages.AccountConnectError{
		Description: err.Error(),
	}

	accErrB, err := json.Marshal(accErr)
	if err != nil {
		log.Printf("Failed to marshal acc err: %v", err)
		return err
	}
	response := messages.AccountConnectMsgRes{
		AccountConnectMessageType: msgType,
		Status:                    messages.StatusFailure,
		Payload:                   accErrB,
	}
	return h.writeClientMessage(response)
}

// writeClientMessage writes a message to the  client's [Send] channel
func (h *messageHandler) writeClientMessage(response messages.AccountConnectMsgRes) error {
	responseB, err := json.Marshal(response)
	if err != nil {
		h.router.logError("marshal response", h.client.ID, err)
		return err
	}

	h.client.Send <- responseB
	return nil
}

func (h *messageHandler) getClient(msg messages.AccountConnectMsg) error {
	_, ok := h.router.Clients[msg.TradeshareClientId]
	if !ok {
		return fmt.Errorf("failed to find client with id: %s registered by the router", h.client.ID)
	}
	return nil
}

func (h *messageHandler) getAdapter(msg messages.AccountConnectMsg) (adapters.ProvidersAdapter, error) {
	adapter, ok := h.client.PlatformConns[msg.Platform]
	if !ok {
		return nil, fmt.Errorf("no adapter for platform %s on client %s", msg.Platform, h.client.ID)
	}
	return adapter, nil
}

type messageHandler struct {
	router *Router
	client *clients.AccountConnectClient
}

func (r *Router) logError(context, clientID string, err error) {
	log.Printf("Error %s for client id: %s: %v", context, clientID, err)
}
