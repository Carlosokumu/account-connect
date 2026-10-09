package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
)

var (
	AccountConnectPort  int32
	CtraderPort         int32
	CtraderEndpoint     string
	CtraderLivePort     int32
	CtraderLiveEndpoint string
)

type Config struct {
	Servers struct {
		Ctrader struct {
			Endpoint     string `yaml:"endpoint"`
			Port         int32  `yaml:"port"`
			LiveEndpoint string `yaml:"live_endpoint"`
			LivePort     int32  `yaml:"live_port"`
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

	CtraderLivePort = cfg.Servers.Ctrader.LivePort
	CtraderLiveEndpoint = cfg.Servers.Ctrader.LiveEndpoint

	return nil
}

func EndpointForEnvironment(env string) (string, int32, error) {
	switch env {
	case "demo":
		if CtraderEndpoint == "" || CtraderPort == 0 {
			return "", 0, errors.New("demo cTrader endpoint is not configured")
		}
		return CtraderEndpoint, CtraderPort, nil
	case "live":
		if CtraderLiveEndpoint == "" || CtraderLivePort == 0 {
			return "", 0, errors.New("live cTrader endpoint is not configured")
		}
		return CtraderLiveEndpoint, CtraderLivePort, nil
	default:
		return "", 0, fmt.Errorf("unknown cTrader environment: %q", env)
	}
}
