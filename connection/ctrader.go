package connection

import (
	"account-connect/config"
	"account-connect/internal/applications"
	accdb "account-connect/persistence"
	"context"
	"fmt"
)

func EstablishCTraderConnection(ctraderConfig *config.CTraderConfig, accdb accdb.AccountConnectDb) (*applications.CTrader, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}

	trader := applications.NewCTrader(accdb, nil, ctraderConfig)
	trader.AccountId = &ctraderConfig.AccountID

	ctraderConfig.Endpoint = cfg.Servers.Ctrader.Endpoint
	ctraderConfig.Port = cfg.Servers.Ctrader.Port

	trader.AccessToken = ctraderConfig.AccessToken
	trader.ClientSecret = ctraderConfig.ClientSecret
	trader.ClientId = ctraderConfig.ClientID

	if err := trader.EstablishCtraderConnection(context.Background(), *ctraderConfig); err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	if err := trader.AuthorizeApplication(); err != nil {
		return nil, fmt.Errorf("authorization failed: %w", err)
	}

	return trader, nil
}
