package clients

import (
	"account-connect/internal/applications"
	"account-connect/internal/mappers"
	messages "account-connect/internal/mappers"
	"account-connect/internal/models"
	"account-connect/router"
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	db "account-connect/persistence"

	"github.com/gorilla/websocket"
)

type AccountConnectClientManager struct {
	clients                map[string]*models.AccountConnectClient
	Broadcast              chan []byte
	IncomingClientMessages chan []byte
	Register               chan *models.AccountConnectClient
	Unregister             chan *models.AccountConnectClient
	msgRouter              *router.Router
	sync.RWMutex
}

// NewClientManager creates a new client manager instance
func NewClientManager(accdb db.AccountConnectDb) *AccountConnectClientManager {
	r := router.NewRouter(accdb)

	return &AccountConnectClientManager{
		clients:                make(map[string]*models.AccountConnectClient),
		Broadcast:              make(chan []byte),
		IncomingClientMessages: make(chan []byte),
		Register:               make(chan *models.AccountConnectClient),
		Unregister:             make(chan *models.AccountConnectClient),
		msgRouter:              r,
	}
}

// StartClientManagement  is responsible for handling client activities,
// from registering,unregistering clients and routing messages to the [Router.]
func (m *AccountConnectClientManager) StartClientManagement(ctx context.Context) {
	for {
		select {
		case client := <-m.Register:
			m.Lock()
			m.clients[client.ID] = client
			m.Unlock()
			go m.handleClientMessages(client)

		case client := <-m.Unregister:
			m.Lock()
			if _, ok := m.clients[client.ID]; ok {
				close(client.Send)
				delete(m.clients, client.ID)
			}
			m.Unlock()

		case message := <-m.Broadcast:
			m.RLock()
			for _, client := range m.clients {
				select {
				case client.Send <- message:
				default:
					close(client.Send)
					delete(m.clients, client.ID)
				}
			}
			m.RUnlock()
		case incomingMsg := <-m.IncomingClientMessages:
			var msg messages.AccountConnectMsg
			if err := json.Unmarshal(incomingMsg, &msg); err != nil {
				log.Printf("Invalid account-connect message %v:", err)
				continue
			}
			m.RLock()
			client, exists := m.clients[msg.ClientId]
			if !exists {
				m.RUnlock()
				log.Printf("Client id: %s not found", msg.ClientId)
				continue
			}
			m.RUnlock()
			m.msgRouter.Route(client, msg)

		}
	}
}

// handleClientMessages  will handle [AccountConnectMsgRes] messages sent through the send channel of the client
func (m *AccountConnectClientManager) handleClientMessages(client *models.AccountConnectClient) {
	ctx := context.Background()
	for msg := range client.Send {
		log.Printf("Message to the client %s: %s\n", client.ID, string(msg))
		var accountConnectMsgRes messages.AccountConnectMsgRes

		err := json.Unmarshal(msg, &accountConnectMsgRes)
		if err != nil {
			log.Printf("Failed to unmarshal account connect message: %v", err)
			return
		}
		if accountConnectMsgRes.Type == "connect" && accountConnectMsgRes.Status == "success" {
			var trader applications.CTrader
			err := json.Unmarshal(accountConnectMsgRes.Payload, &trader)
			if err != nil {
				log.Println("Failed to unmarshal ctrader %v", err)
			}
			msg := messages.AccountConnectMsgRes{
				Type:    "connect",
				Status:  "success",
				Payload: nil,
			}
			err = client.Conn.WriteJSON(msg)
			if err != nil {
				log.Printf("Failed to wrire account connect message: %v", err)
			}
			go m.handlePlatformMessages(client, ctx, &trader)
		}
	}
	log.Printf("Stopped listening to client %s (channel closed)\n", client.ID)
}

func (m *AccountConnectClientManager) handlePlatformMessages(client *models.AccountConnectClient, ctx context.Context, ctrader *applications.CTrader) {
	historicalDealsCh, ok := applications.ChannelRegistry["historical_deals"]
	if !ok {
		log.Println("Failed to retrieve historical deals channel")
		return
	}
	traderInfoCh, ok := applications.ChannelRegistry["trader_info"]
	if !ok {
		log.Println("Failed to retrieve trader info channel")
		return
	}
	trendBarsCh, ok := applications.ChannelRegistry["trend_bars"]
	if !ok {
		log.Println("Failed to retrieve trends bar channel")
		return
	}
	errorsCh, ok := applications.ChannelRegistry["errors"]
	if !ok {
		log.Println("Failed to retrieve error channel")
		return
	}

	accountSymbolsCh, ok := applications.ChannelRegistry["account_symbols"]
	if !ok {
		log.Println("Failed to retrieve account symbols channel")
		return
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down: context cancelled")
			return

		case deals := <-historicalDealsCh:
			var dealData []mappers.AccountConnectDeal
			err := json.Unmarshal(deals, &dealData)
			if err != nil {
				log.Printf("Failed to unmarshal deals: %v", err)
				continue
			}

			for _, deal := range dealData {
				if err := client.Conn.WriteJSON(deal); err != nil {
					log.Printf("Failed to write deal to WebSocket: %v", err)
					if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
						return
					}
					break
				}
			}

		case traderInfo := <-traderInfoCh:
			var r mappers.AccountConnectTraderInfo
			if err := json.Unmarshal(traderInfo, &r); err != nil {
				log.Printf("failed to unmarshal trader info response: %w", err)
			}
			msg := messages.AccountConnectMsgRes{
				Type:    "trader_info",
				Status:  "success",
				Payload: traderInfo,
			}
			if err := client.Conn.WriteJSON(msg); err != nil {
				log.Printf("Failed to write deal to WebSocket: %v", err)
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return
				}
				break
			}

		case trendsBar := <-trendBarsCh:
			var r []mappers.AccountConnectTrendBar
			if err := json.Unmarshal(trendsBar, &r); err != nil {
				log.Printf("failed to unmarshal trader info response: %w", err)
			}
			msg := messages.AccountConnectMsgRes{
				Type:    "trend_bars",
				Status:  "success",
				Payload: trendsBar,
			}
			if err := client.Conn.WriteJSON(msg); err != nil {
				log.Printf("Failed to write trend bars: %v", err)
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return
				}
				break
			}

		case symbols := <-accountSymbolsCh:
			msg := messages.AccountConnectMsgRes{
				Type:    "account_symbols",
				Status:  "success",
				Payload: symbols,
			}
			if err := client.Conn.WriteJSON(msg); err != nil {
				log.Printf("Failed to write account symbols: %v", err)
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return
				}
				break
			}
		case err := <-errorsCh:
			msg := messages.AccountConnectMsgRes{
				Type:    "error",
				Status:  "failure",
				Payload: err,
			}
			if err := client.Conn.WriteJSON(msg); err != nil {
				log.Printf("Failed to write error message: %v", err)
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return
				}
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					close(client.Send)
					return
				}
			}
			closeMsg := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "server error")
			deadline := time.Now().Add(5 * time.Second)

			if err := client.Conn.WriteControl(websocket.CloseMessage, closeMsg, deadline); err != nil {
				log.Printf("Failed to send close message: %v", err)
			}

			client.Conn.Close()
			close(client.Send)

		}

	}

}
