package applications

import (
	"account-connect/internal/mappers"
	messages "account-connect/internal/messages"
	"account-connect/internal/models"
	"account-connect/internal/utils"
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/adshao/go-binance/v2"
)

type BinanceConnection struct {
	Client            *binance.Client
	AccountConnClient *models.AccountConnectClient
}

func NewBinanceConnection(apiKey, secretKey string, accountConnClient *models.AccountConnectClient) *BinanceConnection {
	return &BinanceConnection{
		Client:            binance.NewClient(apiKey, secretKey),
		AccountConnClient: accountConnClient,
	}

}

func (b *BinanceConnection) EstablishBinanceConnection(apiKey, secretKey string) error {
	b.Client = binance.NewClient(apiKey, secretKey)
	return nil
}

func (b *BinanceConnection) GetHistoricalTrades(ctx context.Context) error {
	return fmt.Errorf("GetHistoricalTrades not implemented for  binance")
}

func (b *BinanceConnection) GetTraderInfo(ctx context.Context) error {
	return fmt.Errorf("GetTraderInfo not implemented for binance")
}

func (b *BinanceConnection) GetSymbolTrendBars(ctx context.Context, trendbarsArgs messages.AccountConnectTrendBarsPayload) error {
	return fmt.Errorf("GetSymbolTrendBars not implemented for binance")
}

func (b *BinanceConnection) GetBinanceTradingSymbols(ctx context.Context) ([]byte, error) {
	exchangeInfo, err := b.Client.NewExchangeInfoService().Do(ctx)
	if err != nil {
		log.Printf("Failed to retrieve binance trading symbols")
		return nil, err
	}
	syms := mappers.BinanceSymbolToAccountConnectSymbol(exchangeInfo.Symbols)
	symsB, err := json.Marshal(syms)
	if err != nil {
		log.Printf("Failed to marshal trading symbols data info: %v", err)
		return nil, err
	}
	return symsB, nil
}

type BinanceAdapter struct {
	binanceConn *BinanceConnection
}

func NewBinanceAdapter(apiKey, secretKey string, accountConnClient *models.AccountConnectClient) *BinanceAdapter {
	return &BinanceAdapter{
		binanceConn: NewBinanceConnection(apiKey, secretKey, accountConnClient),
	}
}

func (b *BinanceAdapter) EstablishConnection(ctx context.Context, cfg PlatformConfigs) error {
	apiKey := cfg.ApiKey
	secretKey := cfg.SecretKey

	return b.binanceConn.EstablishBinanceConnection(apiKey, secretKey)
}

func (b *BinanceAdapter) GetTradingSymbols(ctx context.Context, payload messages.AccountConnectSymbolsPayload) error {
	binanceSyms, err := b.binanceConn.GetBinanceTradingSymbols(ctx)
	if err != nil {
		return err
	}
	msg := utils.CreateSuccessResponse(messages.TypeAccountSymbols, b.binanceConn.AccountConnClient.ID, binanceSyms)

	msgB, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.binanceConn.AccountConnClient.Send <- msgB
	return nil
}

func (b *BinanceAdapter) GetHistoricalTrades(ctx context.Context, payload messages.AccountConnectHistoricalDealsPayload) error {
	return b.binanceConn.GetHistoricalTrades(ctx)
}

func (b *BinanceAdapter) GetTraderInfo(ctx context.Context, payload messages.AccountConnectTraderInfoPayload) error {
	return b.binanceConn.GetTraderInfo(ctx)
}

func (b *BinanceAdapter) GetSymbolTrendBars(ctx context.Context, payload messages.AccountConnectTrendBarsPayload) error {
	return fmt.Errorf("GetSymbolTrendBars not implemented for binance")
}

func (b *BinanceAdapter) Disconnect() error {
	return nil
}
