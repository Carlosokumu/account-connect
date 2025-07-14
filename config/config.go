package config

import (
	"errors"
	"os"

	"gopkg.in/yaml.v2"
)

var (
	AccountConnectPort int32
	CtraderPort        int32
	CtraderEndpoint    string
)

type Config struct {
	Servers struct {
		Ctrader struct {
			Endpoint string `yaml:"endpoint"`
			Port     int32  `yaml:"port"`
		} `yaml:"ctrader"`
		AccountConnectServer struct {
			Port int32 `yaml:"port"`
		} `yaml:"account-connect-server"`
	} `yaml:"servers"`
}

func loadConfig() (*Config, error) {
	f, err := os.Open("./config.yml")
	if err != nil {
		return nil, err
	}
	var cfg Config
	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(&cfg)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func LoadConfigs() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	AccountConnectPort = cfg.Servers.AccountConnectServer.Port
	if AccountConnectPort == 0 {
		return errors.New("Required account-connect port is missing")
	}

	CtraderPort = cfg.Servers.Ctrader.Port
	CtraderEndpoint = cfg.Servers.Ctrader.Endpoint
	return nil

}
