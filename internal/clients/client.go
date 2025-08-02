package clients

import (
	"account-connect/internal/adapters"
	"account-connect/internal/messages"
	"errors"
	"sync"

	"github.com/gorilla/websocket"
)

const StreamBufferSize = 500

type AccountConnectClient struct {
	ID   string
	Conn *websocket.Conn
	// PlatformConn *websocket.Conn
	PlatformConns map[messages.Platform]adapters.PlatformAdapter
	Send          chan []byte
	Streams       map[string]chan []byte
	StreamsMutex  sync.Mutex
}

// AddStream adds a new stream to the [Streams] map
func (c *AccountConnectClient) AddStream(streamID string) error {
	c.StreamsMutex.Lock()
	defer c.StreamsMutex.Unlock()

	if _, exists := c.Streams[streamID]; exists {
		return errors.New("stream already exists")
	}

	c.Streams[streamID] = make(chan []byte, StreamBufferSize)
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
