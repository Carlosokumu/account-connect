package models

import (
	"account-connect/internal/applications"

	"github.com/gorilla/websocket"
)
type AccountConnectClient struct {
	ID           string
	Conn         *websocket.Conn
	PlatformConn *websocket.Conn
	Send         chan []byte
	Ctrader      *applications.CTrader
}
