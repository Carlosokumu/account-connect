package main

import (
	"account-connect/config"
	db "account-connect/persistence"
	"account-connect/router"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"account-connect/messages"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func startWsService(accDb db.AccountConnectDb) {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	r := router.NewRouter()
	r.Handle("connect", router.HandleConnect)
	r.Handle("historical_deals", router.HandleHistoricalDeals)

	http.HandleFunc("/ws", func(w http.ResponseWriter, req *http.Request) {
		ws, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			log.Printf("Failed to upgrade connection to ws: %v", err)
			return
		}

		defer ws.Close()

		for {
			ws.SetReadDeadline(time.Now().Add(60 * time.Second))
			ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_, rawMsg, err := ws.ReadMessage()
			if err != nil {
				log.Printf("Error received while reading: %v", err)
				break
			}

			var msg messages.AccountConnectMsg
			if err := json.Unmarshal(rawMsg, &msg); err != nil {
				_ = ws.WriteJSON(map[string]string{
					"status":  "error",
					"message": "Invalid message format",
				})
				log.Printf("Invalid message format %v:", err)
				continue
			}

			if err := r.Route(ws, &accDb, msg); err != nil {
				log.Printf("Failed to route  message type %s: with error: %v", msg.Type, err)
				continue
			}
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

	startWsService(accdb)
}
