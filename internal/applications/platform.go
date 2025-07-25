package applications

import (
	messages "account-connect/internal/messages"
	"context"
)

type PlatformConfigs struct {
	//Binance
	ApiKey    string
	SecretKey string

	//Ctrader
	AccountId    *int64
	ClientId     string
	ClientSecret string
	AccessToken  string
}

type PlatformConnectionStatus struct {
	Authorized bool
}

// Platform defines a set of method signatures that are common for all trading platforms APIs and that are required by
// account-connect suported functionality
type PlatformAdapter interface {
	EstablishConnection(ctxt context.Context, cfg PlatformConfigs) error
	AuthorizeAccount(ctx context.Context, payload messages.AccountConnectAuthorizeTradingAccountPayload) error
	GetUserAccounts(ctx context.Context) error
	GetHistoricalTrades(ctx context.Context, payload messages.AccountConnectHistoricalDealsPayload) error
	GetTraderInfo(ctx context.Context, payload messages.AccountConnectTraderInfoPayload) error
	GetSymbolTrendBars(ctx context.Context, payload messages.AccountConnectTrendBarsPayload) error
	GetTradingSymbols(ctx context.Context, payload messages.AccountConnectSymbolsPayload) error
	InitializeClientStream(ctx context.Context, payload messages.AccountConnectStreamPayload) error
	Disconnect() error
}
