package router

import (
	requestutils "account-connect/internal/accountconnectrequestutils"
	"account-connect/internal/applications"
	messages "account-connect/internal/messages"
	"account-connect/internal/models"
	db "account-connect/persistence"
	"context"
	"encoding/json"
	"fmt"
	"log"
)

type Router struct {
	db             db.AccountConnectDb
	ClientPlatform map[string]applications.PlatformAdapter
	handlers       map[string]func(client *models.AccountConnectClient, accDb *db.AccountConnectDb, payload json.RawMessage) error
}

// NewRouter creates a new Router instance
func NewRouter(accdb db.AccountConnectDb) *Router {
	return &Router{
		ClientPlatform: map[string]applications.PlatformAdapter{},
		db:             accdb,
		handlers:       make(map[string]func(*models.AccountConnectClient, *db.AccountConnectDb, json.RawMessage) error),
	}
}

// Register a handler for a message type
func (r *Router) Handle(messageType string, handler func(*models.AccountConnectClient, *db.AccountConnectDb, json.RawMessage) error) {
	r.handlers[messageType] = handler
}

// Route  routes the different message types from clients to the right handler function
func (r *Router) Route(ctx context.Context, client *models.AccountConnectClient, msg messages.AccountConnectMsg) error {
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
		return handler.handleHistorical(ctx, msg)
	case messages.TypeTraderInfo:
		return handler.handleTraderInfo(ctx, msg)
	case messages.TypeTrendBars:
		return handler.handleTrendBars(ctx, msg)
	case messages.TypeAccountSymbols:
		return handler.handleAccountSymbols(ctx, msg)
	case messages.TypeDisconnect:
		return handler.handleClientDisconnect(ctx, *client)
	case messages.TypeStream:
		return handler.handleClientSubcribeToStream(ctx, msg)

	default:
		return fmt.Errorf("unknown message type: %s", msg.AccountConnectMessageType)
	}
}

// RequestHistoricalDeals requests  a trader's past trades from the underlying trading platform
func (r *Router) RequestHistoricalDeals(ctx context.Context, client *models.AccountConnectClient, accDb *db.AccountConnectDb, payload json.RawMessage) error {
	var res messages.AccountConnectHistoricalDealsPayload

	err := json.Unmarshal(payload, &res)
	if err != nil {
		log.Printf("Failed to unmarshal trader info payload request: %v", err)
		return err
	}

	cp, ok := r.ClientPlatform[client.ID]
	if !ok {
		return fmt.Errorf("failed to find router client with id: %s", client.ID)
	}

	err = cp.GetHistoricalTrades(ctx, res)
	if err != nil {
		log.Printf("Failed to fetch account historical deals: %v", err)
		return err
	}
	return nil
}

func (r *Router) AuthorizeAccount(ctx context.Context, accclient *models.AccountConnectClient, payload json.RawMessage) error {
	var req messages.AccountConnectAuthorizeTradingAccountPayload
	err := json.Unmarshal(payload, &req)
	if err != nil {
		log.Printf("Failed to unmarshal trader info payload request: %v", err)
		return err
	}

	cp, ok := r.ClientPlatform[accclient.ID]
	if !ok {
		return fmt.Errorf("failed to find router client with id: %s", accclient.ID)
	}
	err = cp.AuthorizeAccount(ctx, req)
	if err != nil {
		log.Printf("Failed to retreive trader info: %v", err)
		return err
	}

	return nil
}

// RequestTraderInfo will request the trader's information if  supported by the trading platform's api
func (r *Router) RequestTraderInfo(ctx context.Context, accclient *models.AccountConnectClient, accDb *db.AccountConnectDb, payload json.RawMessage) error {
	var res messages.AccountConnectTraderInfoPayload

	err := json.Unmarshal(payload, &res)
	if err != nil {
		log.Printf("Failed to unmarshal trader info payload request: %v", err)
		return err
	}

	cp, ok := r.ClientPlatform[accclient.ID]
	if !ok {
		return fmt.Errorf("failed to find router client with id: %s", accclient.ID)
	}

	err = cp.GetTraderInfo(ctx, res)
	if err != nil {
		log.Printf("Failed to retreive trader info: %v", err)
		return err
	}

	return nil
}

// RequestAccountSymbols will fetch all of the available trading symbols(tradable assets) for a given trading platform
func (r *Router) RequestAccountSymbols(ctx context.Context, client *models.AccountConnectClient, accDb *db.AccountConnectDb, payload json.RawMessage) error {
	var res messages.AccountConnectSymbolsPayload

	err := json.Unmarshal(payload, &res)
	if err != nil {
		return err
	}

	cp, ok := r.ClientPlatform[client.ID]
	if !ok {
		return fmt.Errorf("failed to find router client with id: %s", client.ID)
	}

	err = cp.GetTradingSymbols(ctx, res)
	if err != nil {
		log.Printf("Failed to retrieve account symbols: %v", err)
		return err
	}

	return nil
}

// RequestTrendBars will request trendbars for a particular symbol(trading pair)
func (r *Router) RequestTrendBars(ctx context.Context, client *models.AccountConnectClient, accDb *db.AccountConnectDb, payload json.RawMessage) error {
	var res messages.AccountConnectTrendBarsPayload

	cp, ok := r.ClientPlatform[client.ID]
	if !ok {
		log.Printf("Client with id: %s not found", client.ID)
		return fmt.Errorf("failed to find router client with id: %s", client.ID)
	}

	err := json.Unmarshal(payload, &res)
	if err != nil {
		log.Printf("Failed to unmarshal account connect trend bars requests: %v", err)
		return fmt.Errorf("failed to unmarshal account connect trend bars requests: %w", err)
	}
	trendBarArgs := messages.AccountConnectTrendBarsPayload{
		SymbolId:      res.SymbolId,
		SymbolName:    res.SymbolName,
		Ctid:          res.Ctid,
		Period:        res.Period,
		FromTimestamp: res.FromTimestamp,
		ToTimestamp:   res.ToTimestamp,
	}

	err = cp.GetSymbolTrendBars(ctx, trendBarArgs)
	if err != nil {
		log.Printf("Failed to retrieve chart trend bar: %v", err)
		return err
	}

	return nil
}

// InitializeClientStream initializes a stream of messages to be sent through the specified channel
func (r *Router) InitializeClientStream(ctx context.Context, client *models.AccountConnectClient, payload json.RawMessage) error {
	var req messages.AccountConnectStreamPayload

	err := json.Unmarshal(payload, &req)
	if err != nil {
		log.Printf("Failed to unmarshal account-connect stream request: %v", err)
		return fmt.Errorf("failed to unmarshal account connect trend bars requests: %w", err)
	}

	cp, ok := r.ClientPlatform[client.ID]
	if !ok {
		log.Printf("Client with id: %s not found", client.ID)
		return fmt.Errorf("failed to find router client with id: %s", client.ID)
	}

	err = cp.InitializeClientStream(ctx, req)
	if err != nil {
		log.Printf("Failed to initialize client's stream: %v", err)
		return err
	}
	return nil
}

// DisconnectPlatformConnection  handles graceful disconnection  of the to underlying platform connection for the client
func (r *Router) DisconnectPlatformConnection(client *models.AccountConnectClient) error {
	cp, ok := r.ClientPlatform[client.ID]
	if !ok {
		log.Printf("Client with id: %s not found", client.ID)
		return fmt.Errorf("failed to find router client with id: %s", client.ID)
	}
	return cp.Disconnect()
}

func (h *messageHandler) handleConnect(ctx context.Context, accountConnClient *models.AccountConnectClient, msg messages.AccountConnectMsg) error {
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)

	var adapter applications.PlatformAdapter
	var err error

	switch msg.Platform {
	case messages.Binance:
		adapter, err = h.handleBinanceConnect(ctx, accountConnClient, msg.Payload)
	case messages.Ctrader:
		adapter, err = h.handleCtraderConnect(ctx, accountConnClient, msg.Payload)
	default:
		return fmt.Errorf("unsupported platform: %s", msg.Platform)
	}

	if err != nil {
		return err
	}
	h.router.ClientPlatform[h.client.ID] = adapter
	return nil
}

func (h *messageHandler) handleAccountAuthorize(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)
	if err := h.router.AuthorizeAccount(ctx, h.client, payload); err != nil {
		return h.writeErrorResponse(messages.TypeTraderInfo, err)
	}
	return nil
}

// handleBinanceConnect will establish a connection to binance
func (h *messageHandler) handleBinanceConnect(
	ctx context.Context,
	accountConnClient *models.AccountConnectClient,
	payload json.RawMessage,
) (applications.PlatformAdapter, error) {
	var binanceMsg messages.BinanceConnectPayload
	if err := json.Unmarshal(payload, &binanceMsg); err != nil {
		return nil, fmt.Errorf("invalid Binance payload: %w", err)
	}

	adapter := applications.NewBinanceAdapter(accountConnClient)
	if err := adapter.EstablishConnection(ctx, applications.PlatformConfigs{}); err != nil {
		return nil, err
	}

	return adapter, nil
}

// handleCtraderConnect will establish a connection to ctrader open api
func (h *messageHandler) handleCtraderConnect(
	ctx context.Context,
	accountConnClient *models.AccountConnectClient,
	payload json.RawMessage,
) (applications.PlatformAdapter, error) {
	var ctraderMsg messages.CTraderConnectPayload
	if err := json.Unmarshal(payload, &ctraderMsg); err != nil {
		return nil, fmt.Errorf("invalid cTrader payload: %w", err)
	}

	cfg := applications.CtraderConfig{
		ClientId:     ctraderMsg.ClientId,
		ClientSecret: ctraderMsg.ClientSecret,
		AccessToken:  ctraderMsg.AccessToken,
	}
	adapter := applications.NewCtraderAdapter(h.router.db, accountConnClient, &cfg)
	if err := adapter.EstablishConnection(ctx, applications.PlatformConfigs{
		Ctrader: applications.CtraderConfig{
			ClientId:     ctraderMsg.ClientId,
			ClientSecret: ctraderMsg.ClientSecret,
			AccessToken:  ctraderMsg.AccessToken,
		},
	}); err != nil {
		return nil, err
	}

	return adapter, nil
}

func (h *messageHandler) handleClientDisconnect(ctx context.Context, client models.AccountConnectClient) error {
	_, ok := h.router.ClientPlatform[client.ID]
	if !ok {
		log.Printf("Client with id: %s not found when disconnecting", client.ID)
		return fmt.Errorf("could not handle disconnect for client with id: %s as client has no active connections", client.ID)
	}
	return h.router.DisconnectPlatformConnection(&client)
}

func (h *messageHandler) handleHistorical(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)
	return h.router.RequestHistoricalDeals(ctx, h.client, &h.router.db, payload)
}

func (h *messageHandler) handleTraderInfo(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)
	if err := h.router.RequestTraderInfo(ctx, h.client, &h.router.db, payload); err != nil {
		return h.writeErrorResponse(messages.TypeTraderInfo, err)
	}
	return nil
}

func (h *messageHandler) handleClientSubcribeToStream(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)
	if err := h.router.InitializeClientStream(ctx, h.client, payload); err != nil {
		return h.writeErrorResponse(messages.TypeTraderInfo, err)
	}
	return nil
}

func (h *messageHandler) handleTrendBars(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)
	if err := h.router.RequestTrendBars(ctx, h.client, &h.router.db, payload); err != nil {
		return h.writeErrorResponse(messages.TypeTrendBars, err)
	}
	return nil
}

func (h *messageHandler) handleAccountSymbols(ctx context.Context, msg messages.AccountConnectMsg) error {
	payload := msg.Payload
	ctx = context.WithValue(ctx, requestutils.REQUEST_ID, msg.RequestId)
	if err := h.router.RequestAccountSymbols(ctx, h.client, &h.router.db, payload); err != nil {
		return h.writeErrorResponse(messages.TypeAccountSymbols, err)
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

type messageHandler struct {
	router *Router
	client *models.AccountConnectClient
}

func (r *Router) logError(context, clientID string, err error) {
	log.Printf("Error %s for client id: %s: %v", context, clientID, err)
}
