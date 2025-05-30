package applications

import (
	"account-connect/common"
	"account-connect/config"
	messages "account-connect/gen"
	accdb "account-connect/persistence"
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

type CTrader struct {
	accDb           accdb.AccountConnectDb
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

// NewCTrader creates a new trader instance with the required fields
func NewCTrader(accdb accdb.AccountConnectDb, ctraderconfig *config.CTraderConfig) *CTrader {
	return &CTrader{
		accDb:         accdb,
		ClientId:      ctraderconfig.ClientID,
		ClientSecret:  ctraderconfig.ClientSecret,
		AccessToken:   ctraderconfig.AccessToken,
		handlers:      make(map[uint32]MessageHandler),
		authCompleted: make(chan struct{}),
	}
}

// RegisterHandler registers a handler for expected protobuf messages.
func (t *CTrader) RegisterHandler(msgType uint32, handler MessageHandler) {
	t.handlers[msgType] = handler
}

func (t *CTrader) registerHandlers() {
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_APPLICATION_AUTH_RES), t.handleApplicationAuthResponse)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_ACCOUNT_AUTH_RES), t.handleAccountAuthResponse)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_ERROR_RES), t.handleErrorReponse)
	t.RegisterHandler(uint32(messages.ProtoPayloadType_HEARTBEAT_EVENT), t.handleHeartBeatMessage)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_DEAL_LIST_RES), t.handleAccountHistoricalDeals)
	t.RegisterHandler(uint32(messages.ProtoOAPayloadType_PROTO_OA_REFRESH_TOKEN_RES), t.handleRefreshTokenResponse)
}

// EstablishCtraderConnection  establishes a  new ctrader websocket connection
func (t *CTrader) EstablishCtraderConnection(ctraderConfig config.CTraderConfig) error {
	// Set up a dialer with the desired options
	dialer := websocket.DefaultDialer
	dialer.EnableCompression = true
	dialer.HandshakeTimeout = 10 * time.Second

	err := t.accDb.RegisterBucket("ctrader")
	if err != nil {
		return err
	}

	endpoint := ctraderConfig.Endpoint
	port := ctraderConfig.Port

	log.Printf("establishing connection to %s:%d", endpoint, port)

	if endpoint == "" {
		return fmt.Errorf("missing cTrader server endpoint in configuration")
	}
	if port == 0 {
		return fmt.Errorf("missing cTrader server port in configuration")
	}

	url := fmt.Sprintf("wss://%s:%d", endpoint, port)
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return err
	}
	t.conn = conn

	t.registerHandlers()
	go t.StartConnectionReader()

	return nil
}

// StartConnectionReader will start a goroutine whose work will be to continously read protobuf messages sent by ctrader
func (t *CTrader) StartConnectionReader() {
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

// AuthorizeApplication is a request  authorizing an application to work with the cTrader platform Proxies.
func (t *CTrader) AuthorizeApplication() error {
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
func (t *CTrader) AuthorizeAccount() error {
	t.mutex.Lock()
	if !t.readyForAccount {
		t.mutex.Unlock()
		return errors.New("application not yet authorized")
	}
	t.mutex.Unlock()

	if t.AccountId == nil {
		return errors.New("account id cannot be nil")
	}

	if len(strconv.FormatInt(*t.AccountId, 10)) < 8 {
		return errors.New("invalid Account id")
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

// GetRefreshToken is a request to refresh the access token using refresh token of granted trader's account.
func (t *CTrader) GetRefreshToken() error {
	refreshToken, err := t.accDb.Get("ctrader", "refresh_token")
	if err != nil {
		return err
	}
	refreshTokenStr := string(refreshToken)
	msgReq := &messages.ProtoOARefreshTokenReq{
		RefreshToken: &refreshTokenStr,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth refresh request: %w", err)
	}
	msgP := &messages.ProtoMessage{
		PayloadType: &common.RefreshTokenMsgType,
		Payload:     msgB,
		ClientMsgId: &common.REQ_REFRESH_TOKEN,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.conn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send refresh token request: %w", err)
	}
	return nil
}

// GetAccountHistoricalDeals is a request for getting Trader's deals historical data (execution details).
func (t *CTrader) GetAccountHistoricalDeals(fromTimestamp, toTimestamp int64) error {
	if t.AccountId == nil {
		return errors.New("account id cannot be nil")
	}

	if len(strconv.FormatInt(*t.AccountId, 10)) < 8 {
		return errors.New("invalid account id")
	}

	msgReq := &messages.ProtoOADealListReq{
		CtidTraderAccountId: t.AccountId,
		FromTimestamp:       &fromTimestamp,
		ToTimestamp:         &toTimestamp,
	}
	msgB, err := proto.Marshal(msgReq)
	if err != nil {
		return fmt.Errorf("failed to marshal auth request: %w", err)
	}
	msgP := &messages.ProtoMessage{
		PayloadType: &common.AccountHistoricalDeals,
		Payload:     msgB,
		ClientMsgId: &common.REQ_ACCOUNT_HISTORICAL_DEALS,
	}

	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.conn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to request account historical trades: %w", err)
	}

	return nil
}

func (t *CTrader) handleApplicationAuthResponse(payload []byte) error {
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

func (t *CTrader) handleHeartBeatMessage(payload []byte) error {
	msgP := &messages.ProtoMessage{
		PayloadType: &common.HeartBeatMsgType,
	}
	protoMessage, err := proto.Marshal(msgP)
	if err != nil {
		return fmt.Errorf("failed to marshal protocol message: %w", err)
	}

	err = t.conn.WriteMessage(MESSAGE_TYPE, protoMessage)
	if err != nil {
		return fmt.Errorf("failed to send back a heartbeat message: %w", err)
	}
	return nil
}

func (t *CTrader) handleAccountAuthResponse(payload []byte) error {
	var r messages.ProtoOAAccountAuthRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal account auth response: %w", err)
	}

	return nil
}

func (t *CTrader) handleRefreshTokenResponse(payload []byte) error {
	var r messages.ProtoOARefreshTokenRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal refresh token response: %w", err)
	}
	err := t.accDb.Put("ctrader", "refresh_token", *r.AccessToken)
	if err != nil {
		return err
	}
	return nil
}

func (t *CTrader) handleAccountHistoricalDeals(payload []byte) error {
	//TODO:
	// Handle account historical deals,maybe store in the db for specified user
	return nil
}

func (t *CTrader) handleErrorReponse(payload []byte) error {
	var r messages.ProtoOAErrorRes
	if err := proto.Unmarshal(payload, &r); err != nil {
		return fmt.Errorf("failed to unmarshal error response: %w", err)
	}
	log.Printf("Received an error response: %s", string(*r.Description))
	return nil
}
