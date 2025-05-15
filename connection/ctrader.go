package connection

import (
	"account-connect/config"
	"account-connect/internal/applications"
	accdb "account-connect/persistence"
	"fmt"
)

func EstablishCTraderConnection(ctraderConfig *config.CTraderConfig) (*applications.CTrader, error) {
	accdb := accdb.AccountConnectDb{}
	err := accdb.Create()
	if err != nil {
		return nil, err
	}

	trader := applications.NewCTrader(accdb, ctraderConfig)
	trader.AccountId = &ctraderConfig.AccountID

	if err := trader.EstablishCtraderConnection(); err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	if err := trader.AuthorizeApplication(); err != nil {
		return nil, fmt.Errorf("authorization failed: %w", err)
	}

	defer accdb.Close()

	return trader, nil
}
