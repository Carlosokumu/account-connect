package accountconnectrequestutils

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Response is what a request's waiter receives: either the raw payload the
// platform sent back, or an error (for example when the connection dropped).
type Response struct {
	Payload []byte
	Err     error
}

// Pending is one in-flight request. Ctx is the caller's context, Ch receives
// exactly one Response (it must be buffered with capacity 1, which NewPending
// guarantees), and Meta carries whatever adapter-specific data the response
// handler needs to finish the job (symbol name, period, and so on).
type Pending[M any] struct {
	Ctx  context.Context
	Ch   chan *Response
	Meta M
}

// NewPending returns a Pending bound to ctx with a buffered response channel.
func NewPending[M any](ctx context.Context) *Pending[M] {
	return &Pending[M]{Ctx: ctx, Ch: make(chan *Response, 1)}
}

// Tracker correlates platform responses with the requests waiting on them. It
// owns its lock; callers never touch the map directly. M is the adapter's
// request metadata type.
type Tracker[M any] struct {
	mu      sync.Mutex
	pending map[string]*Pending[M]
	seq     atomic.Uint64
}

func NewTracker[M any]() *Tracker[M] {
	return &Tracker[M]{pending: make(map[string]*Pending[M])}
}

// NextID returns a unique client message id such as "symbol_list_42".
func (r *Tracker[M]) NextID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, r.seq.Add(1))
}

// Add registers req under key, replacing any previous entry.
func (r *Tracker[M]) Add(key string, req *Pending[M]) {
	r.mu.Lock()
	r.pending[key] = req
	r.mu.Unlock()
}

// Get looks up key without removing it. Only fixed-key connect flows should
// need this; response handlers want Take.
func (r *Tracker[M]) Get(key string) (*Pending[M], bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req, ok := r.pending[key]
	return req, ok
}

// Take looks up key and removes it in one step. A response completes a request
// exactly once, so a second Take for the same key misses.
func (r *Tracker[M]) Take(key string) (*Pending[M], bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req, ok := r.pending[key]
	delete(r.pending, key)
	return req, ok
}

// FailAll unblocks every waiter with err and clears the map. Call it when the
// connection's reader exits so nobody waits forever on a dead socket. It never
// blocks: a request that already has an answer buffered keeps it.
func (r *Tracker[M]) FailAll(err error) {
	r.mu.Lock()
	pending := r.pending
	r.pending = make(map[string]*Pending[M])
	r.mu.Unlock()

	for _, req := range pending {
		select {
		case req.Ch <- &Response{Err: err}:
		default:
		}
	}
}

// Await waits for req's response, honouring ctx. key is the id req was
// registered under, so an abandoned request can be removed instead of leaking.
func (r *Tracker[M]) Await(ctx context.Context, key string, req *Pending[M]) ([]byte, error) {
	select {
	case resp := <-req.Ch:
		if resp.Err != nil {
			return nil, resp.Err
		}
		return resp.Payload, nil
	case <-ctx.Done():
		r.Take(key)
		return nil, ctx.Err()
	}
}
