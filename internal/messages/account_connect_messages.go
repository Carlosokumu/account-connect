package messages

import (
	"encoding/json"

	"github.com/adshao/go-binance/v2/futures"
)

type Platform string

const (
	Ctrader Platform = "ctrader"
	Binance Platform = "binance"
	Alpaca  Platform = "alpaca"
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
	TypeHistoricalTicks   MessageType = "historical_ticks"
	TypeLiveTicks         MessageType = "live_ticks"
	TypeDepthStream       MessageType = "live_depth"
	TypeOrderBookDepth    MessageType = "order_book_depth"
	TypeBBOStream         MessageType = "bbo_stream"
)

const (
	AssetClassCrypto = "Crypto"
	AssetClassEquity = "Equity"
)

type AccountID int64

var accountIDCounter int64

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

type AccountConnectAccountInfoRes struct {
	CtraderAccounts *AccountConnectTradingAccountRes  `json:"ctrader_accounts,omitempty"`
	CtraderAccount  *AccountConnectCtraderAccountInfo `json:"ctrader_account,omitempty"`
	Binance         *AccountConnectBinanceAccountInfo `json:"binance,omitempty"`
	Alpaca          *AlpacaAccountInfo                `json:"account_info,omitempty"`
}

// CTraderConnectPayload  is payload structure defining fields  required to establish a ctrader connection.
type AccountConnectTradingAccount struct {
	BinanceTradingAccount BinanceTradingAccount
}

type BinanceTradingAccount struct {
}

// BinanceConnectPayload is payload structure defining fields  required to establish a binance connection.
type BinanceConnectPayload struct {
	APIKey      string             `json:"api_key"`
	APISecret   string             `json:"api_secret"`
	AccountType BinanceAccountType `json:"account_type"`
}

// AlpacaConnectPayload is payload structure defining fields  required to establish an alpaca connection.
type AlpacaConnectPayload struct {
	APIKey    string `json:"api_key"`
	APISecret string `json:"api_secret"`
	Paper     bool   `json:"paper"`
}

type AccountConnectTrendBarsPayload struct {
	AccountID  AccountID `json:"account_id"`
	SymbolName string    `json:"symbol_name"`
	// SymbolId      int64     `json:"symbol_id"`
	Period        string `json:"period"`
	FromTimestamp *int64 `json:"from_timestamp"`
	ToTimestamp   *int64 `json:"to_timestamp"`
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
	QuoteAssets    []string `json:"quote_assets,omitempty"`
	LimitPerSymbol *int     `json:"limit_per_symbol,omitempty"`
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
	Deals []AccountConnectDeal `json:"deals"`
}

// AccountConnectAuthorizeTradingAccountPayload is a wrapper payload containing all of the possible fields  required by each of  the supported platforms to authorize a trading account(s)
type AccountConnectAuthorizeTradingAccountPayload struct {
	AccountId *int64 `json:"account_id"`
}

// AccountConnectAccountInfoPayload is a wrapper payload containing all of the possible fields
// required by each of the supported platforms to request a trader's information.
type AccountConnectAccountInfoPayload struct {
	AccountID *AccountID `json:"account_id,omitempty"`
}

type AccountConnectSymbolsPayload struct {
	AccountID AccountID `json:"account_id"`
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
	AccountId                   *int64 `json:"account_id"`
	BrokerName                  string `json:"broker"`
	Balance                     int64  `json:"balance"`
	DepositCurrency             string `json:"depositCurrency"`
	TraderRegistrationTimestamp int64  `json:"traderRegistrationTimestamp"`
	TraderAccountType           string `json:"traderAccountType"`
	Leverage                    int64  `json:"leverage"`
	LeverageInCents             int64  `json:"leverageInCents"`
	SwapFree                    bool   `json:"swapFree"`
	MoneyDigits                 int    `json:"moneyDigits"`
	Live                        bool   `json:"live"`
}

type AccountConnectCtraderAccountInfo struct {
	CtidTraderAccountId   int64   `json:"ctid_trader_account_id"`
	Balance               float64 `json:"balance"` // scaled by 10^MoneyDigits
	ManagerBonus          float64 `json:"manager_bonus,omitempty"`
	IbBonus               float64 `json:"ib_bonus,omitempty"`
	NonWithdrawableBonus  float64 `json:"non_withdrawable_bonus,omitempty"`
	MoneyDigits           uint32  `json:"money_digits"` // kept in output for transparency/debugging
	DepositAssetId        int64   `json:"deposit_asset_id"`
	LeverageInCents       uint32  `json:"leverage_in_cents,omitempty"` // e.g. 5000 = 1:50
	MaxLeverage           uint32  `json:"max_leverage,omitempty"`
	SwapFree              bool    `json:"swap_free"`
	IsLimitedRisk         bool    `json:"is_limited_risk"`
	AccountType           string  `json:"account_type,omitempty"` // HEDGED/NETTED — needs enum→string mapping
	BrokerName            string  `json:"broker_name,omitempty"`
	TraderLogin           int64   `json:"trader_login,omitempty"`
	RegistrationTimestamp int64   `json:"registration_timestamp,omitempty"`
	AccessRights          string  `json:"access_rights,omitempty"` // enum→string
}

type AccountConnectTradingAccountRes struct {
	CtTradingAccounts []AccountConnectCtraderTradingAccount `json:"accounts"`
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

type AccountConnectSymbol struct {
	SymbolName         string `json:"symbol_name"` // universal — every platform has *a* name
	SymbolAbbreviation string `json:"symbol_abbreviation"`

	Ctrader *CtraderSymbolInfo `json:"ctrader,omitempty"`
	Binance *BinanceSymbolInfo `json:"binance,omitempty"`
	Alpaca  *AlpacaSymbolInfo  `json:"alpaca,omitempty"`
}

type CtraderSymbolInfo struct {
	SymbolId       int64  `json:"symbol_id"`
	Digits         *int32 `json:"digits,omitempty"`
	AssetClass     string `json:"asset_class,omitempty"`
	SymbolCategory string `json:"symbol_category,omitempty"`

	BaseAsset  *CtraderAsset `json:"base_asset,omitempty"`
	QuoteAsset *CtraderAsset `json:"quote_asset,omitempty"`

	SymbolName   string `json:"-"`
	BaseAssetId  int64  `json:"-"`
	QuoteAssetId int64  `json:"-"`
}

type CtraderAsset struct {
	AssetId     int64  `json:"asset_id"`
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type BinanceSymbolInfo struct {
	BaseAsset              string   `json:"base_asset,omitempty"`
	QuoteAsset             string   `json:"quote_asset,omitempty"`
	BaseAssetPrecision     int      `json:"base_asset_precision,omitempty"`
	QuoteAssetPrecision    int      `json:"quote_asset_precision,omitempty"`
	IsSpotTradingAllowed   bool     `json:"is_spot_trading_allowed"`
	IsMarginTradingAllowed bool     `json:"is_margin_trading_allowed"`
	Permissions            []string `json:"permissions,omitempty"`
	QuoteAssetFullName     string   `json:"quote_asset_fullname,omitempty"`
	BaseAssetFullName      string   `json:"base_asset_fullname,omitempty"`
	AssetClass             string   `json:"asset_class,omitempty"`
}

type AlpacaSymbolInfo struct {
	Symbol       string `json:"symbol"`
	Name         string `json:"name,omitempty"`
	Exchange     string `json:"exchange"`
	AssetClass   string `json:"asset_class"`
	Tradable     bool   `json:"tradable"`
	Marginable   bool   `json:"marginable,omitempty"`
	Shortable    bool   `json:"shortable,omitempty"`
	Fractionable bool   `json:"fractionable,omitempty"`

	BaseAsset  string `json:"base_asset,omitempty"`
	QuoteAsset string `json:"quote_asset,omitempty"`
}

type AccountConnectSymbolRes struct {
	AccountConnectSymbols []AccountConnectSymbol `json:"symbols"`
}

// AccountConnectCryptoPrice is model message containing information about a crypto price.
type AccountConnectCryptoPrice struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

// AccountConnectCandlestickStreamPayload is the unified payload for subscribing
// to a symbol's candlestick/kline stream, regardless of platform.
type AccountConnectCandlestickStreamPayload struct {
	AccountID  AccountID `json:"account_id"`
	SymbolName string    `json:"symbol_name"`
	Period     string    `json:"period"`
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

type AccountConnectBinanceAccountInfo struct {
	Accounts map[BinanceAccountType]AccountConnectBinanceAccount `json:"accounts"`
	Errors   map[BinanceAccountType]string                       `json:"errors,omitempty"`
}

type AccountConnectBinanceAccount struct {
	AccountID int64                              `json:"account_id"`
	Spot      *AccountConnectBinanceSpotInfo     `json:"spot,omitempty"`
	Margin    *AccountConnectBinanceMarginInfo   `json:"margin,omitempty"`
	Futures   *AccountConnectBinanceFuturesInfo  `json:"futures,omitempty"`
	Delivery  *AccountConnectBinanceDeliveryInfo `json:"delivery,omitempty"`
}

type AlpacaAccountInfo struct {
	AccountID        int64  `json:"account_id"`
	Status           string `json:"status"`
	Currency         string `json:"currency"`
	Cash             string `json:"cash"`
	Equity           string `json:"equity"`
	BuyingPower      string `json:"buying_power"`
	PatternDayTrader bool   `json:"pattern_day_trader"`
	TradingBlocked   bool   `json:"trading_blocked"`
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
	CanTrade              bool                       `json:"can_trade"`
	TotalWalletBalance    string                     `json:"total_wallet_balance"`
	TotalUnrealizedProfit string                     `json:"total_unrealized_profit"`
	TotalMarginBalance    string                     `json:"total_margin_balance"`
	Assets                []*futures.AccountAsset    `json:"assets"`
	Positions             []*futures.AccountPosition `json:"positions"`
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

type AccountConnectTickDataPayload struct {
	Ctrader *CtraderTickDataRequestPayload `json:"ctrader,omitempty"`
	Binance *BinanceTickDataRequestPayload `json:"binance,omitempty"`
}

type CtraderTickDataRequestPayload struct {
	Ctid          *int64 `json:"ctid"`
	SymbolId      int64  `json:"symbol_id"`
	QuoteType     string `json:"quote_type"` // "bid" | "ask"
	FromTimestamp *int64 `json:"from_timestamp"`
	ToTimestamp   *int64 `json:"to_timestamp"`
}

type BinanceTickDataRequestPayload struct {
	AccountID     AccountID `json:"account_id"`
	SymbolName    string    `json:"symbol_name"`
	Limit         *int      `json:"limit,omitempty"`
	FromTimestamp *int64    `json:"from_timestamp,omitempty"`
	ToTimestamp   *int64    `json:"to_timestamp,omitempty"`
}

type AccountConnectTick struct {
	Timestamp        int64   `json:"timestamp"`
	Price            float64 `json:"price"`
	Quantity         float64 `json:"quantity,omitempty"`
	QuoteQty         float64 `json:"quote_qty,omitempty"`
	IsBuyerMaker     bool    `json:"is_buyer_maker"`
	IsBestPriceMatch bool    `json:"is_best_price_match"`
}

type AccountConnectTickDataRes struct {
	Ticks      []AccountConnectTick `json:"ticks"`
	HasMore    bool                 `json:"has_more"`
	Symbol     int64                `json:"symbol_id"`
	Quote      string               `json:"quote_type"`
	SymbolName string               `json:"symbol_name,omitempty"`
}

type AccountConnectTickStreamMsg struct {
	SymbolName string  `json:"symbol_name,omitempty"`
	SymbolId   int64   `json:"symbol_id,omitempty"`
	Price      float64 `json:"price,omitempty"`
	Bid        float64 `json:"bid,omitempty"`
	Ask        float64 `json:"ask,omitempty"`
	Quantity   float64 `json:"quantity,omitempty"`
	Timestamp  int64   `json:"timestamp"`
}

type BinanceDepthRequestPayload struct {
	AccountID  AccountID `json:"account_id"`
	SymbolName string    `json:"symbol_name"`
	Limit      *int      `json:"limit,omitempty"`
}

type BinanceBBORequestPayload struct {
	AccountID  AccountID `json:"account_id"`
	SymbolName string    `json:"symbol_name,omitempty"` // empty means all-symbols
}

type AccountConnectDepthPayload struct {
	Binance *BinanceDepthRequestPayload `json:"binance,omitempty"`
}

type AccountConnectDepthLevel struct {
	Price    float64 `json:"price"`
	Quantity float64 `json:"quantity"`
}

type AccountConnectDepthRes struct {
	Bids         []AccountConnectDepthLevel `json:"bids"`
	Asks         []AccountConnectDepthLevel `json:"asks"`
	LastUpdateId int64                      `json:"last_update_id"`
	SymbolName   string                     `json:"symbol_name,omitempty"`
}

type AccountConnectBBO struct {
	SymbolName string  `json:"symbol_name,omitempty"`
	BidPrice   float64 `json:"bid_price"`
	BidQty     float64 `json:"bid_qty"`
	AskPrice   float64 `json:"ask_price"`
	AskQty     float64 `json:"ask_qty"`
	UpdateId   int64   `json:"update_id,omitempty"`
}

type AccountConnectBBOPayload struct {
	Binance *BinanceBBORequestPayload `json:"binance,omitempty"`
}

type CTraderTradingAccountsResponse struct {
	Data []CTraderTradingAccount `json:"data"`
}

type CTraderTradingAccount struct {
	AccountID                   int64   `json:"accountId"`
	AccountNumber               int64   `json:"accountNumber"`
	Live                        bool    `json:"live"`
	BrokerName                  string  `json:"brokerName"`
	BrokerTitle                 string  `json:"brokerTitle"`
	DepositCurrency             string  `json:"depositCurrency"`
	TraderRegistrationTimestamp int64   `json:"traderRegistrationTimestamp"`
	TraderAccountType           string  `json:"traderAccountType"`
	Leverage                    int64   `json:"leverage"`
	LeverageInCents             int64   `json:"leverageInCents"`
	Balance                     int64   `json:"balance"`
	Deleted                     bool    `json:"deleted"`
	AccountStatus               string  `json:"accountStatus"`
	SwapFree                    bool    `json:"swapFree"`
	MoneyDigits                 int     `json:"moneyDigits"`
	BrokerAccountDisplayName    *string `json:"brokerAccountDisplayName"`
}

type AccountConnectBinanceMarginInfo struct {
	BorrowEnabled     bool                               `json:"borrow_enabled"`
	TradeEnabled      bool                               `json:"trade_enabled"`
	TransferEnabled   bool                               `json:"transfer_enabled"`
	MarginLevel       string                             `json:"margin_level"`
	TotalAssetBTC     string                             `json:"total_asset_btc"`
	TotalLiabilityBTC string                             `json:"total_liability_btc"`
	TotalNetAssetBTC  string                             `json:"total_net_asset_btc"`
	Assets            []AccountConnectBinanceMarginAsset `json:"assets"`
}

type AccountConnectBinanceMarginAsset struct {
	Asset    string `json:"asset"`
	Free     string `json:"free"`
	Locked   string `json:"locked"`
	Borrowed string `json:"borrowed"`
	Interest string `json:"interest"`
	NetAsset string `json:"net_asset"`
}

type AccountConnectBinanceFuturesAsset struct {
	Asset            string `json:"asset"`
	WalletBalance    string `json:"wallet_balance"`
	UnrealizedProfit string `json:"unrealized_profit"`
	MarginBalance    string `json:"margin_balance"`
	AvailableBalance string `json:"available_balance"`
}

type AccountConnectBinanceFuturesPosition struct {
	Symbol           string `json:"symbol"`
	PositionAmt      string `json:"position_amt"`
	EntryPrice       string `json:"entry_price"`
	UnrealizedProfit string `json:"unrealized_profit"`
	PositionSide     string `json:"position_side"`
}

type AccountConnectBinanceDeliveryAsset struct {
	Asset            string `json:"asset"`
	WalletBalance    string `json:"wallet_balance"`
	AvailableBalance string `json:"available_balance"`
}

type AccountConnectBinanceDeliveryPosition struct {
	Symbol           string `json:"symbol"`
	PositionAmt      string `json:"position_amt"`
	EntryPrice       string `json:"entry_price"`
	UnrealizedProfit string `json:"unrealized_profit"`
	PositionSide     string `json:"position_side"`
}
