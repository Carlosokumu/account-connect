package router

import (
	"account-connect/persistence"
	"encoding/json"
	"fmt"

	accountmsg "account-connect/messages"

	"github.com/gorilla/websocket"
)

type Router struct {
	handlers map[string]func(ws *websocket.Conn, accDb *persistence.AccountConnectDb, payload json.RawMessage) error
}

// NewRouter creates a new Router instance
func NewRouter() *Router {
	return &Router{
		handlers: make(map[string]func(*websocket.Conn, *persistence.AccountConnectDb, json.RawMessage) error),
	}
}

// Register a handler for a message type
func (r *Router) Handle(messageType string, handler func(*websocket.Conn, *persistence.AccountConnectDb, json.RawMessage) error) {
	r.handlers[messageType] = handler
}

// Route incoming messages to the correct handler
func (r *Router) Route(ws *websocket.Conn, accDb *persistence.AccountConnectDb, msg accountmsg.AccountConnectMsg) error {
	handler, exists := r.handlers[msg.Type]
	if !exists {
		ws.WriteJSON(map[string]string{
			"status":  "error",
			"message": fmt.Sprintf("no handler registered for message type: %s", msg.Type),
		})
		return fmt.Errorf("no handler for message type: %s", msg.Type)
	}
	return handler(ws, accDb, msg.Payload)
}
