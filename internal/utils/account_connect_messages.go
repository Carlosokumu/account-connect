package utils

import (
	"account-connect/internal/messages"
	"context"
	"encoding/json"
)

func CreateErrorResponse(clientID string, errData []byte) messages.AccountConnectMsgRes {
	errRes := messages.AccountConnectError{
		Description: string(errData),
	}

	errResB, err := json.Marshal(errRes)
	if err != nil {
		errResB = []byte(`{"description":"failed to process error message"}`)
	}

	return messages.AccountConnectMsgRes{
		Type:               messages.TypeError,
		Status:             messages.StatusFailure,
		TradeShareClientId: clientID,
		Payload:            errResB,
	}
}

func CreateSuccessResponse(ctx context.Context, msgType messages.MessageType, clientID string, payload []byte) messages.AccountConnectMsgRes {
	v, ok := ctx.Value(REQUEST_ID).(string)
	if !ok {
		v = ""
	}
	return messages.AccountConnectMsgRes{
		RequestId:          v,
		Type:               msgType,
		Status:             messages.StatusSuccess,
		TradeShareClientId: clientID,
		Payload:            payload,
	}
}
