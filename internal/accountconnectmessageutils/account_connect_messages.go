package accountconnectmessageutils

import (
	messageutils "account-connect/internal/accountconnectrequestutils"
	"account-connect/internal/messages"
	"context"
	"encoding/json"
)

// CreateErrorResponse creates a new [AccountConnectMsgRes] response of message type error
func CreateErrorResponse(clientID string, errData []byte) messages.AccountConnectMsgRes {
	errRes := messages.AccountConnectError{
		Description: string(errData),
	}

	errResB, err := json.Marshal(errRes)
	if err != nil {
		errResB = []byte(`{"description":"failed to process error message"}`)
	}

	return messages.AccountConnectMsgRes{
		AccountConnectMessageType: messages.TypeError,
		Status:                    messages.StatusFailure,
		TradeShareClientId:        clientID,
		Payload:                   errResB,
	}
}

// CreateFailureResponse builds an error response for work that fails after the
// request handler has already returned (background goroutines).
func CreateFailureResponse(ctx context.Context, msgType messages.MessageType, provider messages.Provider, clientID string, cause error) messages.AccountConnectMsgRes {
	body, err := json.Marshal(messages.AccountConnectError{Description: cause.Error()})
	if err != nil {
		body = []byte(`{"description":"failed to process error message"}`)
	}
	v, ok := ctx.Value(messageutils.REQUEST_ID).(string)
	if !ok {
		v = ""
	}
	return messages.AccountConnectMsgRes{
		AccountConnectMessageType: msgType,
		Provider:                  provider,
		RequestId:                 v,
		TradeShareClientId:        clientID,
		Status:                    messages.StatusFailure,
		Payload:                   body,
	}
}

// CreateSuccessResponse creates a new [AccountConnectMsgRes] response of message type success
func CreateSuccessResponse(ctx context.Context, msgType messages.MessageType, platform messages.Provider, clientID string, payload []byte) messages.AccountConnectMsgRes {
	v, ok := ctx.Value(messageutils.REQUEST_ID).(string)
	if !ok {
		v = ""
	}
	return messages.AccountConnectMsgRes{
		RequestId:                 v,
		AccountConnectMessageType: msgType,
		Provider:                  platform,
		Status:                    messages.StatusSuccess,
		TradeShareClientId:        clientID,
		Payload:                   payload,
	}
}
