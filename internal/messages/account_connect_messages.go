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
	TypeConnect          MessageType = "connect"
	TypeAuthorizeAccount MessageType = "authorize_account"
	TypeTraderInfo       MessageType = "trader_info"
	TypeHistorical       MessageType = "historical_deals"
	TypeAccountSymbols   MessageType = "account_symbols"
	TypeTrendBars        MessageType = "trend_bars"
	TypeError            MessageType = "error"
	TypeDisconnect       MessageType = "disconnect"
	TypeStream           MessageType = "stream_subscribe"
)

// AccountConnectMsg is a base message structure that incoming client messages are expected to have.
type AccountConnectMsg struct {
	AccountConnectMessageType MessageType     `json:"messagetype" validate:"required"`
	TradeshareClientId        string          `json:"tradeshare_client_id"`
	Platform                  Platform        `json:"platform"`
	RequestId                 string          `json:"request_id"`
	Payload                   json.RawMessage `json:"payload"`
}

// AccountConnectMsgRes  is a base message structure that all outgoing client messages should have.
type AccountConnectMsgRes struct {
	Type               MessageType     `json:"type"`
	Status             MessageStatus   `json:"status"`
	Platform           Platform        `json:"platform"`
	TradeShareClientId string          `json:"tradeshare_client_id"`
	RequestId          string          `json:"request_id"`
	Payload            json.RawMessage `json:"payload"`
}

// CTraderConnectPayload  is payload structure defining fields  required to establish a ctrader connection.
type CTraderConnectPayload struct {
	AccountId    int64  `json:"account_id"`
	ClientId     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	AccessToken  string `json:"access_token"`
}

// BinanceConnectPayload is payload structure defining fields  required to establish a binance connection.
type BinanceConnectPayload struct {
	APIKey    string `json:"api_key"`
	APISecret string `json:"api_secret"`
}

// AccountConnectTrendBarsPayload  is a wrapper payload containing all of the possible fields for each of the supported platforms required to request a symbol's trend bars
type AccountConnectTrendBarsPayload struct {
	SymbolId      int64  `json:"symbol_id"`
	SymbolName    string `json:"symbol_name"`
	Ctid          *int64 `json:"ctid"`
	FromTimestamp *int64 `json:"fromTimestamp"`
	ToTimestamp   *int64 `json:"toTimestamp"`
	Period        string `json:"period"`
}

// AccountConnectStreamPayload wrapper payload required to initialize a stream of messages.
type AccountConnectStreamPayload struct {
	StreamType string `json:"stream_type"`
	SymbolId   string `json:"symbol_id"`
}

// AccountConnectCtId is wrapper payload containing all of the possible fields required  by  each of the supported platforms  to request a trader's information.
type AccountConnectCtId struct {
	Ctid *int64 `json:"ctid"`
}

// AccountConnectError contains description of an error that occurred while processing a client's request
type AccountConnectError struct {
	Description string `json:"description"`
}

// AccountConnectHistoricalDealsPayload is a wrapper payload containing all of the possible fields  required by each of  the supported platforms to request past account trades.
type AccountConnectHistoricalDealsPayload struct {
	FromTimestamp *int64 `json:"fromTimestamp"`
	ToTimestamp   *int64 `json:"toTimestamp"`
	AccountId     *int64 `json:"account_id"`
}

// AccountConnectAuthorizeTradingAccountPayload is a wrapper payload containing all of the possible fields  required by each of  the supported platforms to authorize a trading account(s)
type AccountConnectAuthorizeTradingAccountPayload struct {
	AccountId *int64 `json:"account_id"`
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

// AccountConnectTraderInfo is a model message containing trader's information.
type AccountConnectTraderInfo struct {
	CtidTraderAccountId *int64  `json:"account_id"`
	Login               *int64  `json:"login"`
	BrokerName          *string `json:"broker_name"`
	DepositAssetId      *int64  `json:"depositAssetId"`
}

type AccountConnectCtraderTradingAccount struct {
	AccountId  *uint64 `json:"account_id"`
	BrokerName string  `json:"broker"`
	Balance    string  `json:"balance"`
}

// AccountConnectTrendBar  is model message providing the  OHLC values
type AccountConnectTrendBar struct {
	High                  float64 `json:"high"`
	Open                  float64 `json:"open"`
	Close                 float64 `json:"close"`
	Low                   float64 `json:"low"`
	UtcTimestampInMinutes uint32  `json:"utcTimeStampInMinutes"`
	Volume                int64   `json:"volume"`
}

// AccountConnectTrendBar  is  wrapper model message for the [AccountConnectTrendBar] containing additional metadata.
type AccountConnectTrendBarRes struct {
	Trendbars []AccountConnectTrendBar `json:"trendbars"`
	Symbol    string                   `json:"symbol"`
	Period    string                   `json:"period"`
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

// AccountConnectSymbol  is model message containing trading pairs information
type AccountConnectSymbol struct {
	SymbolName *string `json:"name"` //E.g EUR/USD
	SymbolId   any     `json:"id"`
}

// AccountConnectCryptoPrice is model message containing information about a crypto price.
type AccountConnectCryptoPrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}
