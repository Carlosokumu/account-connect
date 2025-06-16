package models

import (
	"github.com/gorilla/websocket"
)

type AccountConnectClient struct {
	ID           string
	Conn         *websocket.Conn
	PlatformConn *websocket.Conn
	Send         chan []byte
}
