package main

import (
	"account-connect/config"
	"account-connect/internal/clients"
	"account-connect/internal/models"
	db "account-connect/persistence"
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func startWsService(clientManager *clients.AccountConnectClientManager, accDb db.AccountConnectDb) {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
		return
	}

	http.HandleFunc("/ws", func(w http.ResponseWriter, req *http.Request) {
		ws, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			log.Printf("Failed to upgrade connection to ws: %v", err)
			return
		}

		clientID := req.URL.Query().Get("tradeshare_client_id")

		client := &models.AccountConnectClient{
			ID:   clientID,
			Conn: ws,
			Send: make(chan []byte, 256),
		}

		clientManager.Register <- client

		defer func() {
			clientManager.Unregister <- client
			ws.Close()
			log.Printf("Client %s disconnected", clientID)
		}()

		for {
			ws.SetReadDeadline(time.Now().Add(60 * time.Second))
			ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_, rawMsg, err := ws.ReadMessage()
			if err != nil {
				log.Printf("Error received while reading: %v", err)
				break
			}
			clientManager.IncomingClientMessages <- rawMsg
		}
	})
	port := cfg.Servers.AccountConnectServer.Port
	addr := fmt.Sprintf(":%d", port)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func main() {
	accdb := db.AccountConnectDb{}
	err := accdb.Create()
	if err != nil {
		log.Fatal("Failed to initialize account db: %v", err)
	}
	defer accdb.Close()

	clientManager := clients.NewClientManager(accdb)

	ctx := context.Background()
	go clientManager.StartClientManagement(ctx)

	startWsService(clientManager, accdb)
}
