package config

import (
	"os"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Servers struct {
		Ctrader struct {
			Endpoint string `yaml:"endpoint"`
			Port     int32    `yaml:"port"`
		} `yaml:"ctrader"`
		AccountConnectServer struct {
			Port int `yaml:"port"`
		} `yaml:"account-connect-server"`
	} `yaml:"servers"`
}

func LoadConfig() (*Config, error) {
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
