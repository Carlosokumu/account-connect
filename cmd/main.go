package main

import (
	"account-connect/config"
	"account-connect/internal/applications"
	"log"
)

func main() {

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to read app configurations %v", err)
	}

	//Pass required  arguments
	t := applications.NewTrader("", "")

	err = t.EstablishCtraderConnection(cfg)
	if err != nil {
		log.Fatalf("Failed to establish cTrader connection: %v", err)
	}

	if err := t.AuthorizeApplication(); err != nil {
		log.Fatalf("Failed to authorize application: %v", err)
	}

}
