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

type BinanceAccountType string

const (
	BinanceAccountTypeSpot     BinanceAccountType = "SPOT"
	BinanceAccountTypeMargin   BinanceAccountType = "MARGIN"
	BinanceAccountTypeFutures  BinanceAccountType = "FUTURES"  // USDT-M
	BinanceAccountTypeDelivery BinanceAccountType = "DELIVERY" // Coin-M
)

const (
	TypeConnect           MessageType = "connect"
	TypeAuthorizeAccount  MessageType = "authorize_account"
	TypeTraderInfo        MessageType = "trader_info"
	TypeHistoricalTrades  MessageType = "historical_trades"
	TypeAccountSymbols    MessageType = "account_symbols"
	TypeTrendBars         MessageType = "trend_bars"
	TypeError             MessageType = "error"
	TypeDisconnect        MessageType = "disconnect"
	TypeStream            MessageType = "stream_subscribe"
	TypeCandlestickStream MessageType = "candlestick_stream"
	TypeAccountOrders     MessageType = "account_orders"
)

// AccountConnectMsg is a base message structure that incoming client messages are expected to have.
type AccountConnectMsg struct {
	AccountConnectMessageType MessageType     `json:"messagetype" validate:"required,messagetype_enum"`
	TradeshareClientId        string          `json:"tradeshare_client_id" validate:"required"`
	Platform                  Platform        `json:"platform" validate:"required"`
	RequestId                 string          `json:"request_id" validate:"required"`
	Payload                   json.RawMessage `json:"payload" validate:"required"`
}

// AccountConnectMsgRes  is a base message structure that all outgoing client messages should have.
type AccountConnectMsgRes struct {
	AccountConnectMessageType MessageType     `json:"messagetype"`
	Status                    MessageStatus   `json:"status"`
	Platform                  Platform        `json:"platform"`
	TradeShareClientId        string          `json:"tradeshare_client_id"`
	RequestId                 string          `json:"request_id"`
	Payload                   json.RawMessage `json:"payload"`
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
	APIKey      string             `json:"api_key"`
	APISecret   string             `json:"api_secret"`
	AccountType BinanceAccountType `json:"account_type"`
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
	Ctid          *int64                         `json:"ctid"`
	FromTimestamp *int64                         `json:"fromTimestamp"`
	ToTimestamp   *int64                         `json:"toTimestamp"`
	MaxRows       *int32                         `json:"maxRows"`
	Binance       *BinanceHistoricalDealsPayload `json:"binance,omitempty"`
}

// BinanceHistoricalDealsPayload carries binance-specific fields for historical trade retrieval.
type BinanceHistoricalDealsPayload struct {
	// QuoteAssets optionally restricts which quote assets to pair nonzero balances against.
	// If nil or empty, defaults to ["USDT", "BUSD", "BTC", "ETH"].
	QuoteAssets []string `json:"quote_assets,omitempty"`
	// LimitPerSymbol caps the number of trades returned per symbol. Defaults to 500 (Binance max).
	LimitPerSymbol *int `json:"limit_per_symbol,omitempty"`
}

// AccountConnectHistoricalDealsRes is a wrapper response for historical deals,
// carrying platform-specific deal data.
type AccountConnectHistoricalDealsRes struct {
	Binance *BinanceAccountConnectDealsRes `json:"binance,omitempty"`
	Ctrader *CtraderAccountConnectDealsRes `json:"ctrader,omitempty"`
}

// BinanceAccountConnectDealsRes wraps a list of Binance trades with metadata.
type BinanceAccountConnectDealsRes struct {
	Trades []BinanceAccountConnectDeal `json:"trades"`
}

// BinanceAccountConnectDeal is a model message containing information about trades executed on Binance.
type BinanceAccountConnectDeal struct {
	Symbol          string  `json:"symbol"`
	TradeId         int64   `json:"trade_id"`
	OrderId         int64   `json:"order_id"`
	Price           float64 `json:"price"`
	Quantity        float64 `json:"quantity"`
	Commission      float64 `json:"commission"`
	CommissionAsset string  `json:"commission_asset"`
	Time            int64   `json:"time"`
	IsBuyer         bool    `json:"is_buyer"`
	IsMaker         bool    `json:"is_maker"`
	RealizedPnl     float64 `json:"realized_pnl,omitempty"`
	Side            string  `json:"side,omitempty"`
}

// CtraderAccountConnectDealsRes wraps a list of cTrader deals with metadata.
type CtraderAccountConnectDealsRes struct {
	Deals []AccountConnectDeal `json:"deals"` // reuses existing flat cTrader deal type
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

type AccountConnectTradingAccountRes struct {
	CtTradingAccounts []AccountConnectCtraderTradingAccount `json:"ct_trading_accounts"`
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
	EntryPrice     *float64 `json:"entry_price"`
	EntryTime      *int64   `json:"entry_time"`
	Commission     *int64   `json:"commission"`
	Lots           *int64   `json:"lots"`
	ClosingPrice   *float64 `json:"closing_price"`
	Profit         *int64   `json:"profit"`
	Direction      string   `json:"direction"`
	Balance        *int64   `json:"balance"`
	Symbol         *int64   `json:"symbol"`
	DealId         *int64   `json:"deal_id"`
}

// AccountConnectSymbol  is model message containing trading pairs information
type AccountConnectSymbol struct {
	SymbolName *string `json:"name"` //E.g EUR/USD
	SymbolId   any     `json:"id"`
}

type AccountConnectSymbolRes struct {
	AccountConnectSymbols []AccountConnectSymbol `json:"symbols"`
}

// AccountConnectCryptoPrice is model message containing information about a crypto price.
type AccountConnectCryptoPrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

// AccountConnectCandlestickStreamPayload is a wrapper payload containing the platform-specific
// payload required to subscribe to a symbol's candlestick/kline stream.
type AccountConnectCandlestickStreamPayload struct {
	Binance *BinanceCandlestickStreamPayload `json:"binance,omitempty"`
	Ctrader *CtraderCandlestickStreamPayload `json:"ctrader,omitempty"`
}

// BinanceCandlestickStreamPayload is the binance-specific payload required to subscribe
// to a symbol's candlestick/kline stream.
type BinanceCandlestickStreamPayload struct {
	Symbol   string `json:"symbol"`
	Interval string `json:"interval"`
}

// CtraderCandlestickStreamPayload is the ctrader-specific payload required to subscribe
// to a symbol's candlestick/kline stream.
type CtraderCandlestickStreamPayload struct {
	Ctid     *int64 `json:"ctid"`
	SymbolId int64  `json:"symbol_id"`
	Period   string `json:"period"`
}

// AccountConnectCandlestickBar is a model message containing OHLCV values for a single candlestick/kline bar.
type AccountConnectCandlestickBar struct {
	OpenTime  int64   `json:"open_time"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	Volume    float64 `json:"volume"`
	CloseTime int64   `json:"close_time"`
	IsFinal   bool    `json:"is_final"`
}

// AccountConnectCandlestickBarRes is a wrapper model message for [AccountConnectCandlestickBar] containing additional metadata.
type AccountConnectCandlestickBarRes struct {
	Bars     []AccountConnectCandlestickBar `json:"bars"`
	Symbol   string                         `json:"symbol"`
	Interval string                         `json:"interval"`
}

// AccountConnectBinanceBalance is a model message for a single asset balance on a binance account.
type AccountConnectBinanceBalance struct {
	Asset  string `json:"asset"`
	Free   string `json:"free"`
	Locked string `json:"locked"`
}

// AccountConnectBinanceTraderInfo is a model message containing binance account information.
type AccountConnectBinanceTraderInfo struct {
	AccountType string                             `json:"account_type"`
	Spot        *AccountConnectBinanceSpotInfo     `json:"spot,omitempty"`
	Futures     *AccountConnectBinanceFuturesInfo  `json:"futures,omitempty"`
	Delivery    *AccountConnectBinanceDeliveryInfo `json:"delivery,omitempty"`
}

type AccountConnectBinanceSpotInfo struct {
	CanTrade        bool                           `json:"can_trade"`
	CanWithdraw     bool                           `json:"can_withdraw"`
	CanDeposit      bool                           `json:"can_deposit"`
	MakerCommission int64                          `json:"maker_commission"`
	TakerCommission int64                          `json:"taker_commission"`
	Balances        []AccountConnectBinanceBalance `json:"balances"`
}

type AccountConnectBinanceFuturesInfo struct {
	CanTrade              bool                           `json:"can_trade"`
	TotalWalletBalance    string                         `json:"total_wallet_balance"`
	TotalUnrealizedProfit string                         `json:"total_unrealized_profit"`
	TotalMarginBalance    string                         `json:"total_margin_balance"`
	Assets                []AccountConnectBinanceBalance `json:"assets"`
}

type AccountConnectBinanceDeliveryInfo struct {
	CanTrade           bool                           `json:"can_trade"`
	TotalWalletBalance string                         `json:"total_wallet_balance"`
	Assets             []AccountConnectBinanceBalance `json:"assets"`
}

type AccountConnectOrder struct {
	ExecutionPrice *float64 `json:"execution_price"`
	OrderStatus    string   `json:"entry_price"`
	OrderId        *int64   `json:"order_id"`
	OrderType      string   `json:"order_type"`
}

type AccountConnectOrderPayload struct {
	Ctrader *CtraderOrdersRequestPayload `json:"ctrader,omitempty"`
}

type CtraderOrdersRequestPayload struct {
	CtID                   *int64 `json:"ctid,omitempty"`
	ReturnOrdersProtection bool   `json:"returnprotectionorders,omitempty"`
}
