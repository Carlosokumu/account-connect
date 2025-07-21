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
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	ClientSendBufferSize = 512
)

func startWsService(ctx context.Context, clientManager *clients.AccountConnectClientManager, _ db.AccountConnectDb) error {
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.AccountConnectPort),
		Handler: nil,
	}

	http.HandleFunc("/ws", func(w http.ResponseWriter, req *http.Request) {
		ws, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			log.Printf("Failed to upgrade connection to ws: %v", err)
			return
		}

		clientID := req.URL.Query().Get("tradeshare_client_id")
		if clientID == "" {
			errMsg := map[string]string{
				"error":   "client_id_required",
				"message": "Connection rejected: tradeshare_client_id parameter is required",
			}
			ws.WriteJSON(errMsg)
			ws.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "client_id_required"),
				time.Now().Add(time.Second),
			)
			ws.Close()
			return
		}

		client := &models.AccountConnectClient{
			ID:      clientID,
			Conn:    ws,
			Send:    make(chan []byte, ClientSendBufferSize),
			Streams: make(map[string]chan []byte, ClientSendBufferSize),
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

	go func() {
		<-ctx.Done()
		log.Println("Shutting down WebSocket server...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("WebSocket server shutdown error: %v", err)
		}
	}()

	log.Printf("WebSocket server starting on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("WebSocket server failed: %v", err)
	}

	log.Println("WebSocket server stopped gracefully")
	return nil
}

func main() {
	var wg sync.WaitGroup

	err := config.LoadConfigs()
	if err != nil {
		log.Printf("Failed to read config file correctly: %v", err)
		os.Exit(1)
	}

	accdb := db.AccountConnectDb{}
	err = accdb.Create()
	if err != nil {
		log.Printf("Failed to initialize account db: %v", err)
		os.Exit(1)
	}
	defer accdb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		log.Println("Received shutdown signal")
		cancel()
	}()

	clientManager := clients.NewClientManager(accdb)

	wg.Add(1)
	go func() {
		defer wg.Done()
		clientManager.StartClientManagement(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := startWsService(ctx, clientManager, accdb); err != nil {
			log.Printf("WebSocket service error: %v", err)
			cancel()
		}
	}()

	<-ctx.Done()
	log.Println("Main: context canceled, waiting for goroutines to finish...")

	wg.Wait()
	log.Println("Graceful shutdown done")
}
