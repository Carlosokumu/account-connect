package clients

import (
	"account-connect/internal/applications"
	"account-connect/internal/mappers"
	messages "account-connect/internal/mappers"
	"account-connect/internal/models"
	"account-connect/router"
	"context"
	"encoding/json"
	"fmt"
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
				disconnectMsg := messages.AccountConnectMsg{
					Type:               messages.TypeDisconnect,
					Payload:            nil,
					TradeshareClientId: client.ID,
				}
				m.msgRouter.Route(ctx, client, disconnectMsg)
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
			client, exists := m.clients[msg.TradeshareClientId]
			if !exists {
				m.RUnlock()
				log.Printf("Client id: %s not found", msg.TradeshareClientId)
				continue
			}
			m.RUnlock()
			err := m.msgRouter.Route(ctx, client, msg)
			if err != nil {
				errmsg := mappers.CreateErrorResponse(client.ID, []byte(err.Error()))
				errmsgB, err := json.Marshal(errmsg)
				if err != nil {
					log.Printf("Failed to marshal response for incoming message client: %v", err)
				}
				client.Send <- errmsgB
			}
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
		if accountConnectMsgRes.Type == messages.TypeConnect && accountConnectMsgRes.Status == messages.StatusSuccess {
			var trader applications.CTrader
			err := json.Unmarshal(accountConnectMsgRes.Payload, &trader)
			if err != nil {
				log.Println("Failed to unmarshal ctrader %v", err)
			}
			m.writeClientConnMessage(client, messages.TypeConnect, nil)
			go m.handlePlatformMessages(ctx, client, &trader)
		} else if accountConnectMsgRes.Status == messages.StatusFailure {
			m.handlePlatformError(client, accountConnectMsgRes.Payload)
		}
	}
	log.Printf("Stopped listening to client %s (channel closed)\n", client.ID)
}

// handlePlatformMessages will process all of the messages sent via the platform channels.
// All platform messages received  are written to associated client.
func (m *AccountConnectClientManager) handlePlatformMessages(ctx context.Context, client *models.AccountConnectClient, ctrader *applications.CTrader) {
	platformMessages := []string{
		"historical_deals",
		"trader_info",
		"trend_bars",
		"errors",
		"account_symbols",
	}

	platformMessagesChans := make(map[string]chan []byte)
	for _, name := range platformMessages {
		ch, ok := applications.ChannelRegistry[name]
		if !ok {
			log.Printf("Failed to retrieve %s channel for client %s", name, client.ID)
			return
		}
		platformMessagesChans[name] = ch
	}

	defer func() {
		client.Conn.Close()
		log.Printf("Client %s connection closed", client.ID)
	}()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Client %s: context cancelled", client.ID)
			return

		case deals := <-platformMessagesChans["historical_deals"]:
			if err := m.writeClientConnMessage(client, messages.TypeHistorical, deals); err != nil {
				return
			}

		case traderInfo := <-platformMessagesChans["trader_info"]:
			if err := m.writeClientConnMessage(client, messages.TypeTraderInfo, traderInfo); err != nil {
				return
			}

		case trendsBar := <-platformMessagesChans["trend_bars"]:
			if err := m.writeClientConnMessage(client, messages.TypeTrendBars, trendsBar); err != nil {
				return
			}

		case symbols := <-platformMessagesChans["account_symbols"]:
			if err := m.writeClientConnMessage(client, messages.TypeAccountSymbols, symbols); err != nil {
				return
			}
		case err := <-platformMessagesChans["errors"]:
			if err := m.handlePlatformError(client, err); err != nil {
				return
			}

		}

	}

}

// writeClientConnMessage will write an AccountConnectMsgRes to the client's conn using writeJSONWithTimeout
func (m *AccountConnectClientManager) writeClientConnMessage(client *models.AccountConnectClient, msgType messages.MessageType, payload []byte) error {
	msg := messages.CreateSuccessResponse(
		msgType,
		client.ID,
		payload,
	)
	return m.writeJSONWithTimeout(client, msg)
}

func (m *AccountConnectClientManager) handlePlatformError(client *models.AccountConnectClient, errData []byte) error {
	msg := messages.CreateErrorResponse(client.ID, errData)

	if err := m.writeJSONWithTimeout(client, msg); err != nil {
		return err
	}

	// Close connection on error
	closeMsg := websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "server error")
	deadline := time.Now().Add(5 * time.Second)
	_ = client.Conn.WriteControl(websocket.CloseMessage, closeMsg, deadline)
	return fmt.Errorf("error condition, closing connection")
}

func (m *AccountConnectClientManager) writeJSONWithTimeout(client *models.AccountConnectClient, v interface{}) error {
	err := client.Conn.WriteJSON(v)
	if err != nil {
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
			return fmt.Errorf("connection closing")
		}
		log.Printf("Client %s: write failed: %v", client.ID, err)
		return err
	}
	return nil
}
