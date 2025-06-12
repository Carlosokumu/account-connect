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
	ChannelRegistry["historical_deals"] = make(chan []byte)
	ChannelRegistry["trader_info"] = make(chan []byte)
	ChannelRegistry["trend_bars"] = make(chan []byte)
	ChannelRegistry["errors"] = make(chan []byte)
	ChannelRegistry["platform_connect_status"] = make(chan []byte)
	ChannelRegistry["account_symbols"] = make(chan []byte)
}
