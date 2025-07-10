package clients

import (
	"account-connect/internal/applications"
	messages "account-connect/internal/messages"
	"account-connect/internal/models"
	"account-connect/internal/utils"
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
		case <-ctx.Done():
			m.Lock()
			for _, client := range m.clients {
				disconnectMsg := messages.AccountConnectMsg{
					Type:               messages.TypeDisconnect,
					Payload:            nil,
					TradeshareClientId: client.ID,
				}
				m.msgRouter.Route(ctx, client, disconnectMsg)
				close(client.Send)
			}
			m.clients = make(map[string]*models.AccountConnectClient)
			m.Unlock()
			return
		case client := <-m.Register:
			m.Lock()
			m.clients[client.ID] = client
			m.Unlock()
			go m.handleClientMessages(ctx, client)

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
				errR := utils.CreateErrorResponse(client.ID, []byte(err.Error()))
				errRB, err := json.Marshal(errR)
				if err != nil {
					log.Printf("Failed to marshal error response for client %s: %v", client.ID, err)
					return
				}
				select {
				case client.Send <- errRB:
				default:
					log.Printf("Failed to send error to client %s: channel blocked", client.ID)
				}
			}
		}
	}
}

// handleClientMessages  will handle [AccountConnectMsgRes] messages sent through the [Send] channel of the client
func (m *AccountConnectClientManager) handleClientMessages(ctx context.Context, client *models.AccountConnectClient) {
	var (
		err                  error
		accountConnectMsgRes messages.AccountConnectMsgRes
	)

	for msg := range client.Send {
		log.Printf("Message to the client %s: %s\n", client.ID, string(msg))

		err = json.Unmarshal(msg, &accountConnectMsgRes)
		if err != nil {
			log.Printf("Failed to unmarshal account connect message: %v", err)
			return
		}
		if accountConnectMsgRes.Type == messages.TypeConnect && accountConnectMsgRes.Status == messages.StatusSuccess {
			var trader applications.CTrader
			err = json.Unmarshal(accountConnectMsgRes.Payload, &trader)
			if err != nil {
				log.Printf("Failed to unmarshal ctrader %v", err)
				return
			}
			ctx = context.WithValue(ctx, utils.REQUEST_ID, accountConnectMsgRes.RequestId)
			err = m.writeClientConnMessage(ctx, client, messages.TypeConnect, nil)
			if err != nil {
				log.Printf("Client: %s message write fail: %v", client.ID, err)
				return
			}
		} else if accountConnectMsgRes.Status == messages.StatusFailure {
			err = m.handleClientError(client, accountConnectMsgRes.Payload)
			if err != nil {
				return
			}
		} else if accountConnectMsgRes.Status != messages.StatusFailure {
			ctx = context.WithValue(ctx, utils.REQUEST_ID, accountConnectMsgRes.RequestId)
			err = m.writeClientConnMessage(ctx, client, accountConnectMsgRes.Type, accountConnectMsgRes.Payload)
			if err != nil {
				log.Printf("Client: %s message write fail: %v", client.ID, err)
				return
			}
		}
	}
	log.Printf("Stopped listening to client %s (channel closed)\n", client.ID)
}

// writeClientConnMessage will write an AccountConnectMsgRes to the client's conn using writeJSONWithTimeout
func (m *AccountConnectClientManager) writeClientConnMessage(ctx context.Context, client *models.AccountConnectClient, msgType messages.MessageType, payload []byte) error {
	msg := utils.CreateSuccessResponse(
		ctx,
		msgType,
		client.ID,
		payload,
	)
	return m.writeJSONWithTimeout(client, msg)
}

func (m *AccountConnectClientManager) handleClientError(client *models.AccountConnectClient, errData []byte) error {
	msg := utils.CreateErrorResponse(client.ID, errData)

	if err := m.writeJSONWithTimeout(client, msg); err != nil {
		return err
	}
	return fmt.Errorf("error condition, closing connection")
}

func (m *AccountConnectClientManager) writeJSONWithTimeout(client *models.AccountConnectClient, v interface{}) error {
	//allow larger messages to complete writing to the connection.
	client.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

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
