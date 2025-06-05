package mappers

import "encoding/json"

type AccountConnectMsg struct {
	Type     string          `json:"type"`
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
	SymbolId            int64  `json:"symbol_id"`
	CtidTraderAccountId *int64 `json:"client_id"`
	FromTimestamp       *int64 `json:"fromTimestamp"`
	ToTimestamp         *int64 `json:"toTimestamp"`
	Period              string `json:"period"`
}

type AccountConnectMsgRes struct {
	Type     string          `json:"type"`
	Status   string          `json:"status"`
	ClientId string          `json:"client-id"`
	Payload  json.RawMessage `json:"payload"`
}

type AccountConnectDeal struct {
	ExecutionPrice *float64 `json:"execution_price"`
	Commission     *int64   `json:"commission"`
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
