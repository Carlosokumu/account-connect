package router

import (
	"account-connect/config"
	"account-connect/internal/applications"
	messages "account-connect/internal/messages"
	"account-connect/internal/models"
	"account-connect/internal/utils"
	"account-connect/persistence"
	db "account-connect/persistence"
	"context"
	"encoding/json"
	"fmt"
	"log"
)

type Router struct {
	db             persistence.AccountConnectDb
	ClientPlatform map[string]applications.Platform
	handlers       map[string]func(client *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error
}

// NewRouter creates a new Router instance
func NewRouter(accdb db.AccountConnectDb) *Router {
	return &Router{
		ClientPlatform: map[string]applications.Platform{},
		db:             accdb,
		handlers:       make(map[string]func(*models.AccountConnectClient, *persistence.AccountConnectDb, json.RawMessage) error),
	}
}

// Register a handler for a message type
func (r *Router) Handle(messageType string, handler func(*models.AccountConnectClient, *persistence.AccountConnectDb, json.RawMessage) error) {
	r.handlers[messageType] = handler
}

// Route  routes the different message types from clients to the right handler function
func (r *Router) Route(ctxt context.Context, client *models.AccountConnectClient, msg messages.AccountConnectMsg) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("Failed to load configs in route: %v", err)
		return err
	}

	handler := messageHandler{
		router: r,
		client: client,
		cfg:    cfg,
	}

	switch msg.Type {
	case messages.TypeConnect:
		return handler.handleConnect(ctxt, client, msg)
	case messages.TypeHistorical:
		return handler.handleHistorical(msg.Payload)
	case messages.TypeTraderInfo:
		return handler.handleTraderInfo(msg.Payload)
	case messages.TypeTrendBars:
		return handler.handleTrendBars(msg.Payload)
	case messages.TypeAccountSymbols:
		return handler.handleAccountSymbols(msg.Payload)
	case messages.TypeDisconnect:
		return handler.handleClientDisconnect(*client)
	default:
		return fmt.Errorf("unknown message type: %s", msg.Type)
	}
}

func (r *Router) handlePlatformConnectionStatus(client *models.AccountConnectClient, data []byte) {
	platformConnectStatusCh, ok := applications.ChannelRegistry["platform_connect_status"]
	if !ok {
		log.Printf("Failed to retrieve platform_connect_status channel from registry")
		return
	}
	<-platformConnectStatusCh
	client.Send <- data

}

// RequestHistoricalDeals requests  a trader's past trades from the underlying trading platform
func (r *Router) RequestHistoricalDeals(client *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	var res messages.AccountConnectHistoricalDealsPayload

	err := json.Unmarshal(payload, &res)
	if err != nil {
		log.Printf("Failed to unmarshal trader info payload request: %v", err)
		return err
	}

	cp, ok := r.ClientPlatform[client.ID]
	if !ok {
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}

	err = cp.GetHistoricalTrades(context.Background(), res)
	if err != nil {
		log.Printf("Failed to fetch account historical deals: %v", err)
		return err
	}
	return nil
}

// RequestTraderInfo will request the trader's information if  supported by the trading platform's api
func (r *Router) RequestTraderInfo(accclient *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	var res messages.AccountConnectTraderInfoPayload

	err := json.Unmarshal(payload, &res)
	if err != nil {
		log.Printf("Failed to unmarshal trader info payload request: %v", err)
		return err
	}

	cp, ok := r.ClientPlatform[accclient.ID]
	if !ok {
		return fmt.Errorf("Failed to find router client with id: %s", accclient.ID)
	}

	err = cp.GetTraderInfo(context.Background(), res)
	if err != nil {
		log.Printf("Failed to retreive trader info: %v", err)
		return err
	}

	return nil
}

// RequestAccountSymbols will fetch all of the available trading symbols for a given trading platform
func (r *Router) RequestAccountSymbols(client *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	var res messages.AccountConnectSymbolsPayload

	err := json.Unmarshal(payload, &res)
	if err != nil {
		return err
	}

	cp, ok := r.ClientPlatform[client.ID]
	if !ok {
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}

	err = cp.GetTradingSymbols(context.Background(), res)
	if err != nil {
		log.Printf("Failed to retrieve account symbols: %v", err)
		return err
	}

	return nil
}

// RequestTrendBars will request trendbars for a particular symbol(trading pair)
func (r *Router) RequestTrendBars(client *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	var res messages.AccountConnectTrendBarsPayload

	cp, ok := r.ClientPlatform[client.ID]
	if !ok {
		log.Printf("Client with id: %s not found", client.ID)
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}

	err := json.Unmarshal(payload, &res)
	if err != nil {
		log.Printf("Failed to unmarshal account connect trend bars requests: %v", err)
		return fmt.Errorf("Failed to unmarshal account connect trend bars requests: %w", err)
	}
	trendBarArgs := messages.AccountConnectTrendBarsPayload{
		SymbolId:      res.SymbolId,
		Ctid:          res.Ctid,
		Period:        res.Period,
		FromTimestamp: res.FromTimestamp,
		ToTimestamp:   res.ToTimestamp,
	}

	err = cp.GetSymbolTrendBars(context.Background(), trendBarArgs)
	if err != nil {
		log.Printf("Failed to retrieve chart trend bar: %v", err)
		return err
	}

	return nil
}

// DisconnectPlatformConnection  handles disconnection to underlying platform connection for the client
func (r *Router) DisconnectPlatformConnection(client *models.AccountConnectClient) error {
	cp, ok := r.ClientPlatform[client.ID]
	if !ok {
		log.Printf("Client with id: %s not found", client.ID)
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}
	return cp.Disconnect()
}

func (h *messageHandler) handleConnect(ctx context.Context, accountConnClient *models.AccountConnectClient, msg messages.AccountConnectMsg) error {
	switch msg.Platform {
	case messages.Binance:
		var binanceconnectmsg messages.BinanceConnectPayload
		if err := json.Unmarshal(msg.Payload, &binanceconnectmsg); err != nil {
			return fmt.Errorf("invalid connect message format: %w", err)
		}

		binanceAdapter := applications.NewBinanceAdapter(binanceconnectmsg.APIKey, binanceconnectmsg.APISecret, accountConnClient)
		h.router.ClientPlatform[h.client.ID] = binanceAdapter

		err := binanceAdapter.EstablishConnection(ctx, applications.PlatformConfigs{})
		if err != nil {
			return err
		}
		msgR := utils.CreateSuccessResponse(messages.TypeConnect, h.client.ID, nil)
		return h.writeClientMessage(msgR)
	case messages.Ctrader:
		var ctconnectmsg messages.CTraderConnectPayload
		if err := json.Unmarshal(msg.Payload, &ctconnectmsg); err != nil {
			return fmt.Errorf("invalid connect message format: %w", err)
		}

		ctraderCfg := config.NewCTraderConfig(ctconnectmsg.AccountId)
		ctraderCfg.Endpoint = h.cfg.Servers.Ctrader.Endpoint
		ctraderCfg.Port = h.cfg.Servers.Ctrader.Port

		ctraderCfg.ClientID = ctconnectmsg.ClientId
		ctraderCfg.ClientSecret = ctconnectmsg.ClientSecret
		ctraderCfg.AccessToken = ctconnectmsg.AccessToken

		ctraderAdapter := applications.NewCtraderAdapter(h.router.db, accountConnClient, ctraderCfg)

		err := ctraderAdapter.EstablishConnection(ctx, applications.PlatformConfigs{
			AccountId:    &ctconnectmsg.AccountId,
			ClientId:     ctconnectmsg.ClientId,
			ClientSecret: ctconnectmsg.ClientSecret,
			AccessToken:  ctconnectmsg.AccessToken,
		})
		if err != nil {
			return err
		}
		response := messages.AccountConnectMsgRes{
			Type:    messages.TypeConnect,
			Status:  messages.StatusSuccess,
			Payload: nil,
		}
		h.router.ClientPlatform[h.client.ID] = ctraderAdapter
		return h.writeClientMessage(response)
	default:
		return fmt.Errorf("Attempting connection to unsupported platform: %s", msg.Platform)

	}
}

func (h *messageHandler) handleClientDisconnect(client models.AccountConnectClient) error {
	_, ok := h.router.ClientPlatform[client.ID]
	if !ok {
		log.Printf("Client with id: %s not found when disconnecting", client.ID)
		return fmt.Errorf("Could not handle disconnect for client with id: %s as was client not found", client.ID)
	}
	return h.router.DisconnectPlatformConnection(&client)
}

func (h *messageHandler) handleHistorical(payload json.RawMessage) error {
	return h.router.RequestHistoricalDeals(h.client, &h.router.db, payload)
}

func (h *messageHandler) handleTraderInfo(payload json.RawMessage) error {
	if err := h.router.RequestTraderInfo(h.client, &h.router.db, payload); err != nil {
		return h.writeErrorResponse(messages.TypeTraderInfo, err)
	}
	return nil
}

func (h *messageHandler) handleTrendBars(payload json.RawMessage) error {
	if err := h.router.RequestTrendBars(h.client, &h.router.db, payload); err != nil {
		return h.writeErrorResponse(messages.TypeTrendBars, err)
	}
	return nil
}

func (h *messageHandler) handleAccountSymbols(payload json.RawMessage) error {
	if err := h.router.RequestAccountSymbols(h.client, &h.router.db, payload); err != nil {
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
		Type:    msgType,
		Status:  messages.StatusFailure,
		Payload: accErrB,
	}
	return h.writeClientMessage(response)
}

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
	cfg    *config.Config
}

func (r *Router) logError(context, clientID string, err error) {
	log.Printf("Error %s for client id: %s: %v", context, clientID, err)
}
