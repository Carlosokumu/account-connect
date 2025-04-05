package applications

import (
	"account-connect/common"
	messages "account-connect/gen"
	"errors"
	"fmt"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	MESSAGE_TYPE = websocket.BinaryMessage
)

type Trader struct {
	ClientSecret string
	ClientId     string
}

// Request for the authorizing an application to work with the cTrader platform Proxies.
func (t *Trader) AuthorizeApplication(conn *websocket.Conn) error {
	if conn == nil {
		return errors.New("websocket connection cannot be nil")
	}
	if t.ClientId == "" || t.ClientSecret == "" {
		return errors.New("client credentials not set")
	}

	msgReq := &messages.ProtoOAApplicationAuthReq{
		ClientId:     &t.ClientId,
		ClientSecret: &t.ClientSecret,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth request: %w", err)
	}
	msgP := &messages.ProtoMessage{
		PayloadType: &common.AppAuthMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_APP_AUTH,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = conn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send auth request: %w", err)
	}
	return nil

}
