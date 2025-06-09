package mappers

import "encoding/json"

type MessageType string

const (
	TypeConnect        MessageType = "connect"
	TypeTraderInfo     MessageType = "trader_info"
	TypeHistorical     MessageType = "historical_deals"
	TypeAccountSymbols MessageType = "account_symbols"
	TypeTrendBars      MessageType = "trend_bars"
	TypeError          MessageType = "error"
	TypeDisconnect     MessageType = "disconnect"
)

type MessageStatus string

const (
	StatusSuccess MessageStatus = "success"
	StatusFailure MessageStatus = "failure"
	StatusPending MessageStatus = "pending"
)

func CreateErrorResponse(clientID string, errData []byte) AccountConnectMsgRes {
	return AccountConnectMsgRes{
		Type:     TypeError,
		Status:   StatusFailure,
		ClientId: clientID,
		Payload:  errData,
	}
}

func CreateSuccessResponse(msgType MessageType, clientID string, payload []byte) AccountConnectMsgRes {
	return AccountConnectMsgRes{
		Type:     msgType,
		Status:   StatusSuccess,
		ClientId: clientID,
		Payload:  payload,
	}
}

type AccountConnectMsg struct {
	Type     MessageType     `json:"type"`
	ClientId string          `json:"client_id"`
	Payload  json.RawMessage `json:"payload"`
}

type CtConnectMsg struct {
	AccountId    int64  `json:"account_id"`
	ClientId     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	AccessToken  string `json:"access_token"`
}

type AccountConnectTrendBars struct {
	SymbolId      int64  `json:"symbol_id"`
	Ctid          *int64 `json:"ctid"`
	FromTimestamp *int64 `json:"fromTimestamp"`
	ToTimestamp   *int64 `json:"toTimestamp"`
	Period        string `json:"period"`
}

type AccountConnectMsgRes struct {
	Type     MessageType     `json:"type"`
	Status   MessageStatus   `json:"status"`
	ClientId string          `json:"client-id"`
	Payload  json.RawMessage `json:"payload"`
}

type AccountConnectDeal struct {
	ExecutionPrice *float64 `json:"execution_price"`
	Commission     *int64   `json:"commission"`
	Lots           *int64   `json:"lots"`
	ClosingPrice   *int64   `json:"closing_price"`
	Profit         *int64   `json:"profit"`
	Direction      string   `json:"direction"`
	Balance        *int64   `json:"balance"`
	Symbol         *int64   `json:"symbol"`
}

type AccountConnectError struct {
	Description string `json:"description"`
}

type AccountConnectTraderInfo struct {
	CtidTraderAccountId *int64  `json:"account_id"`
	Login               *int64  `json:"login"`
	BrokerName          *string `json:"broker_name"`
	DepositAssetId      *int64  `json:"depositAssetId"`
}

// OHLC values
type AccountConnectTrendBar struct {
	High                  int64 `json:"high"`
	Open                  int64 `json:"open"`
	Close                 int64 `json:"close"`
	Low                   int64 `json:"low"`
	UtcTimestampInMinutes int64 `json:"utcTimeStampInMinutes"`
}

type AccountConnectSymbol struct {
	SymbolName *string `json:"name"` //E.g EUR/USD
	SymbolId   *int64  `json:"id"`
}

type AccountConnectCtId struct {
	Ctid *int64 `json:"ctid"`
}

type AccountConnectHistoricalDeals struct {
	FromTimestamp *int64 `json:"fromTimestamp"`
	ToTimestamp   *int64 `json:"toTimestamp"`
}
