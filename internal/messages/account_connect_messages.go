package messages

import (
	"encoding/json"
)

type Platform string

const (
	Ctrader Platform = "ctrader"
	Binance Platform = "binance"
)

type MessageStatus string

const (
	StatusSuccess MessageStatus = "success"
	StatusFailure MessageStatus = "failure"
	StatusPending MessageStatus = "pending"
)

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

// All incoming client messages are expected to have this payload structure.
type AccountConnectMsg struct {
	Type               MessageType     `json:"type" validate:"required"`
	TradeshareClientId string          `json:"tradeshare_client_id"`
	Platform           Platform        `json:"platform"`
	Payload            json.RawMessage `json:"payload"`
}

// All outgoing client messages should have this payload structure
type AccountConnectMsgRes struct {
	Type               MessageType     `json:"type"`
	Status             MessageStatus   `json:"status"`
	TradeShareClientId string          `json:"tradeshare_client_id"`
	Payload            json.RawMessage `json:"payload"`
}

// Payload that should be contained in the  payload field of AccountConnectMsg struct for ctrader connection.
type CTraderConnectPayload struct {
	AccountId    int64  `json:"account_id"`
	ClientId     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	AccessToken  string `json:"access_token"`
}

// Payload that should be contained in the  payload field of AccountConnectMsg struct for binance connection.
type BinanceConnectPayload struct {
	APIKey    string `json:"api_key"`
	APISecret string `json:"api_secret"`
}

// AccountConnectTrendBarsPayload  is a wrapper payload containing all of the possible fields for each of the supported platforms required to request a symbol's trend bars
type AccountConnectTrendBarsPayload struct {
	SymbolId      int64  `json:"symbol_id"`
	Ctid          *int64 `json:"ctid"`
	FromTimestamp *int64 `json:"fromTimestamp"`
	ToTimestamp   *int64 `json:"toTimestamp"`
	Period        string `json:"period"`
}

// AccountConnectCtId is wrapper payload containing all of the possible fields required  by  each of the supported platforms  to request a trader's information.
type AccountConnectCtId struct {
	Ctid *int64 `json:"ctid"`
}

// AccountConnectDeal  is model message containing information about a deal that happened for a particular trade
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

// AccountConnectError contains description of an error that occurred while processing a client's request
type AccountConnectError struct {
	Description string `json:"description"`
}

// AccountConnectTraderInfo is a model message containing trader's information.
type AccountConnectTraderInfo struct {
	CtidTraderAccountId *int64  `json:"account_id"`
	Login               *int64  `json:"login"`
	BrokerName          *string `json:"broker_name"`
	DepositAssetId      *int64  `json:"depositAssetId"`
}

// AccountConnectTrendBar  is model message providing the  OHLC values
type AccountConnectTrendBar struct {
	High                  float64 `json:"high"`
	Open                  float64 `json:"open"`
	Close                 float64 `json:"close"`
	Low                   float64 `json:"low"`
	UtcTimestampInMinutes float64 `json:"utcTimeStampInMinutes"`
	Volume                int64   `json:"volume"`
}

// AccountConnectSymbol  is model message containing trading pairs information
type AccountConnectSymbol struct {
	SymbolName *string `json:"name"` //E.g EUR/USD
	SymbolId   any     `json:"id"`
}

// AccountConnectHistoricalDealsPayload is a wrapper payload containing all of the possible fields  required by each of  the supported platforms to request past account trades.
type AccountConnectHistoricalDealsPayload struct {
	FromTimestamp *int64 `json:"fromTimestamp"`
	ToTimestamp   *int64 `json:"toTimestamp"`
}

// AccountConnectTraderInfoPayload is wrapper payload containing all of the possible fields  required by each of the supported platforms to request a trader's information.
type AccountConnectTraderInfoPayload struct {
	Ctid *int64 `json:"ctid"`
}

// AccountConnectSymbolsPayload is a wrapper payload containing all of the possible fields  required by each of  the supported platforms to request trading symbols
type AccountConnectSymbolsPayload struct {
	Ctid *int64 `json:"ctid"`
}

// AccountConnectSymbolInfoPayload is a wrapper payload containing all of the possible fields  required by each of  the supported platforms to request additional symbol information
type AccountConnectSymbolInfoPayload struct {
	Ctid     *int64  `json:"ctid"`
	SymbolId []int64 `json:"symbol_id"`
}
