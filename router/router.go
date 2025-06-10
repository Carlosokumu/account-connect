package router

import (
	"account-connect/config"
	"account-connect/internal/applications"
	"account-connect/internal/mappers"
	messages "account-connect/internal/mappers"
	"account-connect/internal/models"
	"account-connect/persistence"
	db "account-connect/persistence"
	"encoding/json"
	"fmt"
	"log"
)

type Router struct {
	db       persistence.AccountConnectDb
	Clients  map[string]*applications.CTrader
	handlers map[string]func(client *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error
}

// NewRouter creates a new Router instance
func NewRouter(accdb db.AccountConnectDb) *Router {
	return &Router{
		Clients:  map[string]*applications.CTrader{},
		db:       accdb,
		handlers: make(map[string]func(*models.AccountConnectClient, *persistence.AccountConnectDb, json.RawMessage) error),
	}
}

// Register a handler for a message type
func (r *Router) Handle(messageType string, handler func(*models.AccountConnectClient, *persistence.AccountConnectDb, json.RawMessage) error) {
	r.handlers[messageType] = handler
}

func (r *Router) Route(client *models.AccountConnectClient, msg messages.AccountConnectMsg) error {
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
	case mappers.TypeConnect:
		return handler.handleConnect(msg.Payload)
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

// HandleConnect will trigger a connection to ctrader when a new client connection is established
func (r *Router) HandleConnect(client *models.AccountConnectClient, ctraderConfig config.CTraderConfig) error {
	var accountConnectMsgRes messages.AccountConnectMsgRes

	trader, ok := r.Clients[client.ID]
	if !ok {
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}
	if err := trader.EstablishCtraderConnection(ctraderConfig); err != nil {
		accErr := messages.AccountConnectError{
			Description: err.Error(),
		}
		accErrB, err := json.Marshal(accErr)
		if err != nil {
			log.Printf("Failed to marshal acc err response: %v", err)
			return err
		}
		accountConnectMsgRes = messages.AccountConnectMsgRes{
			Type:    "connect",
			Status:  "failure",
			Payload: accErrB,
		}
		accountConnectMsgResB, err := json.Marshal(accountConnectMsgRes)
		if err != nil {
			log.Printf("Failed to marshal acc err response: %w", err)
			return err
		}

		client.Send <- accountConnectMsgResB

		return fmt.Errorf("connection failed: %w", err)
	}

	if err := trader.AuthorizeApplication(); err != nil {
		accErr := messages.AccountConnectError{
			Description: err.Error(),
		}
		v, err := json.Marshal(accErr)
		if err != nil {
			log.Printf("Failed to marshal acc err response: %v", err)
		}
		client.Send <- v
		return fmt.Errorf("authorization failed: %w", err)
	}

	return nil
}

func (r *Router) RequestHistoricalDeals(client *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	var res mappers.AccountConnectHistoricalDeals

	err := json.Unmarshal(payload, &res)
	if err != nil {
		log.Printf("Failed to unmarshal trader info payload request: %v", err)
		return err
	}
	frmTimeStamp := res.FromTimestamp
	toTimestamp := res.ToTimestamp

	trader, ok := r.Clients[client.ID]
	if !ok {
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}

	err = trader.GetAccountHistoricalDeals(*frmTimeStamp, *toTimestamp)
	if err != nil {
		log.Printf("Failed to fetch account historical deals: %v", err)
		return err
	}
	return nil
}

func (r *Router) RequestTraderInfo(accclient *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	var res mappers.AccountConnectCtId

	err := json.Unmarshal(payload, &res)
	if err != nil {
		log.Printf("Failed to unmarshal trader info payload request: %v", err)
		return err
	}

	client, ok := r.Clients[accclient.ID]
	if !ok {
		return fmt.Errorf("Failed to find router client with id: %s", accclient.ID)
	}

	cid := res.Ctid
	err = client.GetAccountTraderInfo(cid)
	if err != nil {
		log.Printf("Failed to retreive trader info: %v", err)
		return err
	}

	return nil
}

func (r *Router) RequestAccountSymbols(client *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	var res mappers.AccountConnectCtId

	err := json.Unmarshal(payload, &res)
	if err != nil {
		return err
	}

	trader, ok := r.Clients[client.ID]
	if !ok {
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}

	cid := res.Ctid
	err = trader.GetAccountTradingSymbols(cid)
	if err != nil {
		log.Printf("Failed to retrieve account symbols: %v", err)
		return err
	}

	return nil
}

// RequestTrendBars will request trendbars for a particular symbol(trading pair)
func (r *Router) RequestTrendBars(client *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	var res mappers.AccountConnectTrendBars

	trader, ok := r.Clients[client.ID]
	if !ok {
		log.Printf("Client with id: %s not found", client.ID)
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}

	err := json.Unmarshal(payload, res)
	if err != nil {
		log.Printf("Failed to unmarshal account connect trend bars requests: %v", err)
		return fmt.Errorf("Failed to unmarshal account connect trend bars requests: %w", err)
	}
	trendBarArgs := mappers.AccountConnectTrendBars{
		SymbolId:      res.SymbolId,
		Ctid:          res.Ctid,
		Period:        res.Period,
		FromTimestamp: res.FromTimestamp,
		ToTimestamp:   res.ToTimestamp,
	}

	err = trader.GetChartTrendBars(trendBarArgs)
	if err != nil {
		log.Printf("Failed to retrieve chart trend bar: %v", err)
	}

	return nil
}

func (r *Router) DisconnectPlatformConnection(client *models.AccountConnectClient) error {
	trader, ok := r.Clients[client.ID]
	if !ok {
		log.Printf("Client with id: %s not found", client.ID)
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}
	return trader.DisconnectPlatformConn()
}

func (h *messageHandler) handleConnect(payload json.RawMessage) error {
	var ctconnectmsg messages.CtConnectMsg
	if err := json.Unmarshal(payload, &ctconnectmsg); err != nil {
		return fmt.Errorf("invalid connect message format: %w", err)
	}

	ctraderCfg := config.NewCTraderConfig(ctconnectmsg.AccountId)
	ctraderCfg.Endpoint = h.cfg.Servers.Ctrader.Endpoint
	ctraderCfg.Port = h.cfg.Servers.Ctrader.Port
	ctraderCfg.ClientID = ctconnectmsg.ClientId
	ctraderCfg.ClientSecret = ctconnectmsg.ClientSecret
	ctraderCfg.AccessToken = ctconnectmsg.AccessToken

	trader := applications.NewCTrader(h.router.db, ctraderCfg)
	trader.AccountId = &ctraderCfg.AccountID
	h.router.Clients[h.client.ID] = trader

	if err := h.router.HandleConnect(h.client, *ctraderCfg); err != nil {
		h.router.logError("connection", h.client.ID, err)
		return err
	}

	response := messages.AccountConnectMsgRes{
		Type:    mappers.TypeConnect,
		Status:  messages.StatusSuccess,
		Payload: nil,
	}
	return h.writeClientMessage(response)
}

func (h *messageHandler) handleClientDisconnect(client models.AccountConnectClient) error {
	_, ok := h.router.Clients[client.ID]
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
