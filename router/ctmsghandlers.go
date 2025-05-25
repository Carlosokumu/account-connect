package router

import (
	"account-connect/config"
	"account-connect/connection"
	"account-connect/messages"
	"account-connect/persistence"
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"
)

// HandleConnect will trigger a connection to ctrader when a new client connection is established
func HandleConnect(ws *websocket.Conn, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	var ctconnectmsg messages.CtConnectMsg
	err := json.Unmarshal(payload, &ctconnectmsg)
	if err != nil {
		_ = ws.WriteJSON(map[string]string{
			"event":   "connect",
			"status":  "error",
			"message": "Invalid connect message format",
		})
		return fmt.Errorf("Invalid connect message format: %w", err)
	}

	ctraderCfg := config.NewCTraderConfig(ctconnectmsg.AccountId)
	ctraderCfg.ClientID = ctconnectmsg.ClientId
	ctraderCfg.ClientSecret = ctconnectmsg.ClientSecret
	ctraderCfg.AccessToken = ctconnectmsg.AccessToken

	_, err = connection.EstablishCTraderConnection(ctraderCfg, *accDb)
	if err != nil {
		_ = ws.WriteJSON(map[string]string{
			"event":   "connect",
			"status":  "error",
			"message": fmt.Sprintf("CTrader connection failed: %v", err),
		})
		return fmt.Errorf("CTrader initialization failed: %w", err)
	}

	return ws.WriteJSON(map[string]string{
		"event":   "connect",
		"status":  "success",
		"message": "You are now connected to ctrader",
	})
}

// HandleHistoricalDeals will request historical deals from ctrader when a connection has been  authorized to do so
func HandleHistoricalDeals(ws *websocket.Conn, accDb *persistence.AccountConnectDb, payload json.RawMessage) error {
	return ws.WriteJSON(map[string]string{
		"event":   "historical_deals",
		"status":  "success",
		"message": "These are your historical deals",
	})
}
