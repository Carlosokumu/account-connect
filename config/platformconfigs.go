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

type AlpacaConfig struct {
	ApiKey    string
	SecretKey string
	Paper     bool
}

type PlatformConfigs struct {
	Binance BinanceConfig
	Ctrader CtraderConfig
	Alpaca  AlpacaConfig
}
