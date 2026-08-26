package adapters

import (
	"account-connect/config"
	"account-connect/internal/messages"
	"context"
)

// ProvidersAdapter defines a set of method signatures that are common for all the data providers APIs and that are required by
// account-connect suported functionality
type ProvidersAdapter interface {
	EstablishConnection(ctxt context.Context, cfg config.PlatformConfigs) error
	AuthorizeAccount(ctx context.Context, payload messages.AccountConnectAuthorizeTradingAccountPayload) error
	GetUserAccounts(ctx context.Context) error
	GetHistoricalTrades(ctx context.Context, payload messages.AccountConnectHistoricalDealsPayload) error
	GetTraderInfo(ctx context.Context, payload messages.AccountConnectTraderInfoPayload) error
	GetSymbolTrendBars(ctx context.Context, payload messages.AccountConnectTrendBarsPayload) error
	GetTradingSymbols(ctx context.Context, payload messages.AccountConnectSymbolsPayload) error
	InitializeClientStream(ctx context.Context, payload messages.AccountConnectStreamPayload) error
	Disconnect(ctx context.Context) error
	GetCandlestickStream(ctx context.Context, payload messages.AccountConnectCandlestickStreamPayload) error
	GetAccountOrders(ctx context.Context, payload messages.AccountConnectOrderPayload) error
}
