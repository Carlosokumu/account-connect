package applications

import (
	"sync"
)

// Global channel registry
var (
	ChannelRegistry = make(map[string]chan []byte)
	RegistryLock    sync.RWMutex
)

func init() {
	ChannelRegistry["platform_connect_status"] = make(chan []byte)
}
