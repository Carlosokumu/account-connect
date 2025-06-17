package utils

import (
	"account-connect/internal/messages"
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

func CreateSuccessResponse(msgType messages.MessageType, clientID string, payload []byte) messages.AccountConnectMsgRes {
	return messages.AccountConnectMsgRes{
		Type:               msgType,
		Status:             messages.StatusSuccess,
		TradeShareClientId: clientID,
		Payload:            payload,
	}
}
