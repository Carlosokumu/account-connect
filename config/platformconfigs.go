package config

import "account-connect/internal/messages"

type BinanceConfig struct {
	ApiKey      string
	SecretKey   string
	AccountType messages.BinanceAccountType
}

type CtraderConfig struct {
	ClientId     string
	ClientSecret string
	AccessToken  string
}

type PlatformConfigs struct {
	Binance BinanceConfig
	Ctrader CtraderConfig
}
