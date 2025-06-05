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

// Route incoming messages to the correct handler
func (r *Router) Route(client *models.AccountConnectClient, msg messages.AccountConnectMsg) error {
	cfg, _ := config.LoadConfig()

	switch msg.Type {
	case "connect":
		var ctconnectmsg messages.CtConnectMsg
		err := json.Unmarshal(msg.Payload, &ctconnectmsg)
		if err != nil {
			return fmt.Errorf("Invalid connect message format: %w", err)
		}

		ctraderCfg := config.NewCTraderConfig(ctconnectmsg.AccountId)
		ctraderCfg.Endpoint = cfg.Servers.Ctrader.Endpoint
		ctraderCfg.Port = cfg.Servers.Ctrader.Port

		ctraderCfg.ClientID = ctconnectmsg.ClientId
		ctraderCfg.ClientSecret = ctconnectmsg.ClientSecret
		ctraderCfg.AccessToken = ctconnectmsg.AccessToken
		trader := applications.NewCTrader(r.db, ctraderCfg)

		trader.AccountId = &ctraderCfg.AccountID
		r.Clients[client.ID] = trader

		err = r.HandleConnect(client, *ctraderCfg)
		if err != nil {
			log.Printf("Error handling connection client id: %s connection: %v", client.ID, err)
		}
		accConnectRes := messages.AccountConnectMsgRes{
			Type:    "connect",
			Status:  "success",
			Payload: nil,
		}
		v, err := json.Marshal(accConnectRes)
		if err != nil {
			log.Printf("Failed to marshal account connect msg response")
		}
		go r.handlePlatformConnectionStatus(client, v)

	case "historical_deals":
		r.RequestHistoricalDeals(client, &r.db, msg.Payload)
	case "trader_info":
		r.RequestTraderInfo(client, &r.db, msg.Payload)
	case "trend_bars":
		r.RequestTrendBars(client, &r.db, msg.Payload)
	case "account_symbols":
		r.RequestAccountSymbols(client, &r.db, msg.Payload)

	default:
	}
	return nil
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
	trader, ok := r.Clients[client.ID]

	if !ok {
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}
	if err := trader.EstablishCtraderConnection(ctraderConfig); err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	if err := trader.AuthorizeApplication(); err != nil {
		return fmt.Errorf("authorization failed: %w", err)
	}

	return nil
}

func (r *Router) RequestHistoricalDeals(client *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	trader, ok := r.Clients[client.ID]
	if !ok {
		return fmt.Errorf("Failed to find router client with id: %s", client.ID)
	}

	err := trader.GetAccountHistoricalDeals(int64(1621526400), int64(1719072000))
	if err != nil {
		log.Printf("Failed to fetch account historical deals: %v", err)
	}
	return nil
}

func (r *Router) RequestTraderInfo(accclient *models.AccountConnectClient, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	var res mappers.AccountConnectCtId

	err := json.Unmarshal(payload, res)
	if err != nil {
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

	err := json.Unmarshal(payload, res)
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
	}

	return nil
}

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
		SymbolId:            res.SymbolId,
		CtidTraderAccountId: res.CtidTraderAccountId,
		Period:              res.Period,
		FromTimestamp:       res.FromTimestamp,
		ToTimestamp:         res.ToTimestamp,
	}

	err = trader.GetChartTrendBars(trendBarArgs)
	if err != nil {
		log.Printf("Failed to retrieve chart trend bar: %v", err)
	}

	return nil
}
