package clients

import (
	"account-connect/internal/adapters"
	"account-connect/internal/messages"
	"account-connect/internal/streams"
	"context"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

const (
	ClientSendBufferSize = 512
	StreamBufferSize     = 500
)

type AccountConnectClient struct {
	ID            string
	Conn          *websocket.Conn
	PlatformConns map[messages.Platform]adapters.ProvidersAdapter
	Send          chan []byte
	Streams       map[string]chan []byte
	StreamsMutex  sync.Mutex
	StreamMerger  *streams.StreamMerger
}

func NewAccountConnectClient(id string, conn *websocket.Conn) *AccountConnectClient {
	return &AccountConnectClient{
		ID:            id,
		Conn:          conn,
		Send:          make(chan []byte, ClientSendBufferSize),
		Streams:       make(map[string]chan []byte),
		PlatformConns: make(map[messages.Platform]adapters.ProvidersAdapter),
	}
}

func (c *AccountConnectClient) AddStream(ctx context.Context, streamId string) error {
	c.StreamsMutex.Lock()
	defer c.StreamsMutex.Unlock()

	if _, exists := c.Streams[streamId]; exists {
		return fmt.Errorf("stream %s already exists", streamId)
	}
	stream := make(chan []byte, 100)
	c.Streams[streamId] = stream
	c.StreamMerger.Add(stream)
	return nil
}

// RemoveStream removes a stream from the [Streams] map
func (c *AccountConnectClient) RemoveStream(streamID string) {
	c.StreamsMutex.Lock()
	defer c.StreamsMutex.Unlock()

	if stream, exists := c.Streams[streamID]; exists {
		close(stream)
		delete(c.Streams, streamID)
	}
}
