package streams

import (
	"context"
	"sync"
)

// StreamMerger is a dynamic fan-in that merges multiple stream channels into one
// and supports adding new streams at runtime.
type StreamMerger struct {
	merged chan []byte
	add    chan chan []byte
	ctx    context.Context
}

// NewStreamMerger creates and starts a new StreamMerger
func NewStreamMerger(ctx context.Context) *StreamMerger {
	sm := &StreamMerger{
		merged: make(chan []byte, 100),
		add:    make(chan chan []byte),
		ctx:    ctx,
	}
	go sm.run()
	return sm
}

// run is the core fan-in loop — listens for new streams and forwards their messages
func (sm *StreamMerger) run() {
	defer close(sm.merged)

	var wg sync.WaitGroup

	for {
		select {
		case <-sm.ctx.Done():
			wg.Wait()
			return
		case stream, ok := <-sm.add:
			if !ok {
				wg.Wait()
				return
			}
			wg.Add(1)
			go func(s chan []byte) {
				defer wg.Done()
				for {
					select {
					case <-sm.ctx.Done():
						return
					case msg, ok := <-s:
						if !ok {
							return
						}
						select {
						case sm.merged <- msg:
						case <-sm.ctx.Done():
							return
						}
					}
				}
			}(stream)
		}
	}
}

// Add registers a new stream channel with the merger at runtime
func (sm *StreamMerger) Add(stream chan []byte) {
	select {
	case sm.add <- stream:
	case <-sm.ctx.Done():
	}
}

// Messages returns the merged output channel
func (sm *StreamMerger) Messages() <-chan []byte {
	return sm.merged
}
