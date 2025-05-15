package config

import "github.com/spf13/viper"

type CTraderConfig struct {
	ClientID     string
	ClientSecret string
	AccessToken  string
	AccountID    int64
}

func NewCTraderConfig(acountId int64) *CTraderConfig {
	return &CTraderConfig{
		ClientID:     viper.GetString("platform.ctrader.client-id"),
		ClientSecret: viper.GetString("platform.ctrader.client-secret"),
		AccessToken:  viper.GetString("platform.ctrader.access-token"),
		AccountID:    acountId,
	}
}
