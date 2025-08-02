package config

type BinanceConfig struct {
	ApiKey    string
	SecretKey string
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
