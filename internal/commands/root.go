package commands

import (
	"account-connect/config"
	"account-connect/connection"
	"account-connect/persistence"
	"log"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	platform  string
	cfgFile   string
	accountID int64
)

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: $HOME/.trader.yaml)")
	rootCmd.PersistentFlags().StringVarP(&platform, "platform", "p", "ctrader", "platform to connect to")
	rootCmd.PersistentFlags().Int64VarP(&accountID, "account", "a", 0, "Account ID (required)")
}

func initConfig() {
	viper.SetConfigType("yaml")
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Failed to get home dir: %v", err)
		}
		viper.AddConfigPath(home)
		viper.SetConfigName(".trader")
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Println("Config file not found, using flags/defaults")
		} else {
			log.Fatalf("Config error: %v", err)
		}
	}
}

var rootCmd = &cobra.Command{
	Use:   "account-connect",
	Short: "Connect trading accounts to social platforms",
	Run: func(cmd *cobra.Command, args []string) {
		switch platform {
		case "ctrader":
			ctraderCfg := config.NewCTraderConfig(accountID)
			accountDb := persistence.AccountConnectDb{}
			_, err := connection.EstablishCTraderConnection(ctraderCfg, accountDb)
			if err != nil {
				log.Fatalf("CTrader initialization failed: %v", err)
			}
			log.Println("CTrader connection established successfully")
		default:
			log.Fatalf("Unsupported platform: %s", platform)
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
