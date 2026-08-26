package main

import (
	"account-connect/config"
	"account-connect/internal/clients"
	"account-connect/internal/managers"
	"account-connect/internal/messages"
	"account-connect/internal/messagevalidator"
	"account-connect/persistence"
	db "account-connect/persistence"
	"context"
	"encoding/json"
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
	wsReadLimit       = 512 * 1024
	wsPingInterval    = 30 * time.Second
	wsPingWriteWait   = 15 * time.Second
	wsReadDeadline    = 60 * time.Second
	wsWriteDeadline   = 30 * time.Second
	wsShutdownTimeout = 5 * time.Second
)

// writerLoop is the single goroutine that owns all writes to the websocket
// connection, eliminating concurrent write races.
func writerLoop(ws *websocket.Conn, send <-chan []byte, done <-chan struct{}) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-send:
			if !ok {
				// send channel closed — write a close frame and exit
				ws.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
					time.Now().Add(wsWriteDeadline),
				)
				return
			}
			ws.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
			if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("Write error: %v", err)
				return
			}

		case <-ticker.C:
			err := ws.WriteControl(
				websocket.PingMessage,
				[]byte{},
				time.Now().Add(wsPingWriteWait),
			)
			if err != nil {
				log.Printf("Ping failed: %v", err)
				return
			}

		case <-done:
			return
		}
	}
}

func startWsService(ctx context.Context, clientManager *managers.AccountConnectClientManager) error {
	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.AccountConnectPort),
		Handler: mux,
	}

	msgValidator := messagevalidator.New()
	msgValidator.RegisterValidations()

	mux.HandleFunc("/ws", func(w http.ResponseWriter, req *http.Request) {
		ws, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			log.Printf("Failed to upgrade connection to ws: %v", err)
			return
		}
		ws.SetReadLimit(wsReadLimit)

		// Validate client ID before doing anything else.
		clientID := req.URL.Query().Get("tradeshare_client_id")
		if clientID == "" {
			rejectConn(ws, "client_id_required", "tradeshare_client_id parameter is required")
			return
		}

		client := clients.NewAccountConnectClient(clientID, ws)
		clientManager.Register <- client
		defer func() {
			clientManager.Unregister <- client
			log.Printf("Client %s disconnected", clientID)
		}()

		// send is the only channel allowed to write to ws.
		send := make(chan []byte, 64)
		done := make(chan struct{})
		defer close(done)

		go writerLoop(ws, send, done)

		ws.SetPongHandler(func(_ string) error {
			log.Printf("Pong received from client: %s", clientID)
			ws.SetReadDeadline(time.Now().Add(wsReadDeadline))
			return nil
		})

		for {
			ws.SetReadDeadline(time.Now().Add(wsReadDeadline))
			_, rawMsg, err := ws.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err,
					websocket.CloseGoingAway,
					websocket.CloseNormalClosure,
					websocket.CloseNoStatusReceived,
				) {
					log.Printf("Unexpected close from client %s: %v", clientID, err)
				}
				break
			}

			var msg messages.AccountConnectMsg
			if err := json.Unmarshal(rawMsg, &msg); err != nil {
				sendError(send, "unmarshal_failed", err.Error())
				continue
			}

			if err := msgValidator.Validate(msg); err != nil {
				sendError(send, "message_validation_failed", err.Error())
				continue
			}

			if err := clientManager.ValidateClient(msg.TradeshareClientId); err != nil {
				sendError(send, "client_validation_failed", err.Error())
				continue
			}

			clientManager.IncomingClientMessages <- rawMsg
		}
	})

	go func() {
		<-ctx.Done()
		log.Println("Shutting down WebSocket server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), wsShutdownTimeout)
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

// rejectConn sends an error message and a close frame, then closes the
// connection. Used before a client is registered.
func rejectConn(ws *websocket.Conn, code, msg string) {
	ws.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
	ws.WriteJSON(map[string]string{"error": code, "message": msg})
	ws.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, code),
		time.Now().Add(wsWriteDeadline),
	)
	ws.Close()
}

// sendError marshals an error payload and queues it on the send channel.
// Non-blocking: drops the message and logs if the channel is full.
func sendError(send chan<- []byte, code, msg string) {
	payload, err := json.Marshal(map[string]string{"error": code, "message": msg})
	if err != nil {
		log.Printf("Failed to marshal error payload: %v", err)
		return
	}
	select {
	case send <- payload:
	default:
		log.Printf("Send buffer full, dropped error: %s", code)
	}
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

	accCache := persistence.NewBboltTradeCache(accdb.Db)
	if err := accCache.RegisterBuckets(); err != nil {
		log.Printf("Failed to register cache buckets: %v", err)
		os.Exit(1)
	}

	clientManager := managers.NewClientManager(accCache)

	wg.Add(1)
	go func() {
		defer wg.Done()
		clientManager.StartClientManagement(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := startWsService(ctx, clientManager); err != nil {
			log.Printf("WebSocket service error: %v", err)
			cancel()
		}
	}()

	<-ctx.Done()
	log.Println("Main: context canceled, waiting for goroutines...")
	wg.Wait()
	log.Println("Graceful shutdown done")
}
