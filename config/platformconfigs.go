package config

type BinanceConfig struct {
	ApiKey    string
	SecretKey string
	Paper     bool
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
