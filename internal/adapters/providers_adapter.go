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
	GetAccountInfo(ctx context.Context, payload messages.AccountConnectAccountInfoPayload) error
	GetSymbolTrendBars(ctx context.Context, payload messages.AccountConnectTrendBarsPayload) error
	GetTradingSymbols(ctx context.Context, payload messages.AccountConnectSymbolsPayload) error
	Disconnect(ctx context.Context) error
	GetCandlestickStream(ctx context.Context, payload messages.AccountConnectCandlestickStreamPayload) error
	GetAccountOrders(ctx context.Context, payload messages.AccountConnectOrderPayload) error
	GetHistoricalTicks(ctx context.Context, payload messages.AccountConnectTickDataPayload) error
	GetTickStream(ctx context.Context, payload messages.AccountConnectTickDataPayload) error
	GetOrderBookDepth(ctx context.Context, payload messages.AccountConnectDepthPayload) error
	GetDepthStream(ctx context.Context, payload messages.AccountConnectDepthPayload) error
	GetBBOStream(ctx context.Context, payload messages.AccountConnectBBOPayload) error
}
