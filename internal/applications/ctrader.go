package applications

import (
	"account-connect/common"
	"account-connect/config"
	messages "account-connect/gen"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	MESSAGE_TYPE = websocket.BinaryMessage
)

type MessageHandler func(payload []byte) error

type Trader struct {
	ClientSecret    string
	ClientId        string
	AccountId       *int64
	AccessToken     string
	conn            *websocket.Conn
	handlers        map[uint32]MessageHandler
	authCompleted   chan struct{}
	readyForAccount bool
	mutex           sync.Mutex
}

// NewTrader creates a new trader instance with the required fields
func NewTrader(clientId, clientSecret string) *Trader {
	return &Trader{
		ClientId:      clientId,
		ClientSecret:  clientSecret,
		handlers:      make(map[uint32]MessageHandler),
		authCompleted: make(chan struct{}),
	}
}

// RegisterHandler registers a handler for expected protobuf messages.
func (t *Trader) RegisterHandler(msgType uint32, handler MessageHandler) {
	t.handlers[msgType] = handler
}

func (t *Trader) registerHandlers() {
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_APPLICATION_AUTH_RES), t.handleApplicationAuthResponse)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_ACCOUNT_AUTH_RES), t.handleAccountAuthResponse)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_ERROR_RES), t.handleErrorReponse)
}

// EstablishCtraderConnection  establishes a  new ctrader websocket connection
func (t *Trader) EstablishCtraderConnection(cfg *config.Config) error {
	// Set up a dialer with the desired options
	dialer := websocket.DefaultDialer
	dialer.EnableCompression = true
	dialer.HandshakeTimeout = 10 * time.Second

	// Connect to the  Ctrader WebSocket endpoint
	url := fmt.Sprintf("wss://%s:%d", cfg.Server.Endpoint, cfg.Server.Port)
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return err
	}
	t.conn = conn

	t.registerHandlers()
	go t.StartConnectionReader()

	return nil
}

// StartConnectionReade will start a goroutine whose work will be to continously read protobuf messages sent by ctrader
func (t *Trader) StartConnectionReader() {
	defer close(t.authCompleted)

	for {
		_, msgB, err := t.conn.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			return
		}

		var msgP messages.ProtoMessage
		if err := proto.Unmarshal(msgB, &msgP); err != nil {
			log.Printf("Failed to unmarshal protocol message: %v", err)
			continue
		}

		if handler, ok := t.handlers[msgP.GetPayloadType()]; ok {
			if err := handler(msgP.Payload); err != nil {
				log.Printf("Handler error for type %d: %v", msgP.GetPayloadType(), err)
			}
		} else {
			log.Printf("No handler for message type %d", msgP.GetPayloadType())
		}
	}
}

// Request for the authorizing an application to work with the cTrader platform Proxies.
func (t *Trader) AuthorizeApplication() error {
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

	err = t.conn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send auth request: %w", err)
	}

	<-t.authCompleted

	return nil

}

// AuthorizeAccount sends a request to authorize specified ctrader account id
func (t *Trader) AuthorizeAccount() error {
	t.mutex.Lock()
	if !t.readyForAccount {
		t.mutex.Unlock()
		return errors.New("application not yet authorized")
	}
	t.mutex.Unlock()

	if t.AccountId == nil {
		return errors.New("Account id cannot be nil")
	}

	if len(strconv.FormatInt(*t.AccountId, 10)) < 8 {
		return errors.New("Invalid Account id")
	}

	msgReq := &messages.ProtoOAAccountAuthReq{
		CtidTraderAccountId: t.AccountId,
		AccessToken:         &t.AccessToken,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth request: %w", err)
	}
	msgP := &messages.ProtoMessage{
		PayloadType: &common.AccountAuthMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_ACCOUNT_AUTH,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.conn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send auth request: %w", err)
	}
	return nil
}

func (t *Trader) handleApplicationAuthResponse(payload []byte) error {
	var r messages.ProtoOAApplicationAuthRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal auth response: %w", err)
	}

	t.mutex.Lock()
	t.readyForAccount = true
	t.mutex.Unlock()

	// Signal that app auth is complete
	close(t.authCompleted)

	return t.AuthorizeAccount()
}

func (t *Trader) handleAccountAuthResponse(payload []byte) error {
	var r messages.ProtoOAAccountAuthRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal account auth response: %w", err)
	}
	return nil
}

func (t *Trader) handleErrorReponse(payload []byte) error {
	var r messages.ProtoOAErrorRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal error response: %w", err)
	}
	t.conn.Close()
	return nil
}
