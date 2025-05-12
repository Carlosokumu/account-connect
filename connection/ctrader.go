package connection

import (
	"account-connect/internal/applications"
	"fmt"

	"github.com/spf13/viper"
)

type CTraderConfig struct {
	ClientID     string
	ClientSecret string
	AccountID    int64
}

func NewCTraderConfig(acountId int64) *CTraderConfig {
	return &CTraderConfig{
		ClientID:     viper.GetString("platform.ctrader.client-id"),
		ClientSecret: viper.GetString("platform.ctrader.client-secret"),
		AccountID:    acountId,
	}
}

func EstablishCTraderConnection(cfg *CTraderConfig) (*applications.CTrader, error) {
	trader := applications.NewCTrader(cfg.ClientID, cfg.ClientSecret)
	trader.AccountId = &cfg.AccountID

	if err := trader.EstablishCtraderConnection(); err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	if err := trader.AuthorizeApplication(); err != nil {
		return nil, fmt.Errorf("authorization failed: %w", err)
	}

	return trader, nil
}
