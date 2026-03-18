package service

import "sync"

const clientBufferSize = 256

// ClientHub manages WebSocket client connections and broadcasts messages.
type ClientHub struct {
	mu     sync.RWMutex
	conns  map[int]chan []byte
	nextID int
}

// NewClientHub creates a new ClientHub.
func NewClientHub() *ClientHub {
	return &ClientHub{
		conns: make(map[int]chan []byte),
	}
}

// Register adds a new client and returns its ID and receive channel.
func (h *ClientHub) Register() (int, <-chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	id := h.nextID
	h.nextID++

	ch := make(chan []byte, clientBufferSize)
	h.conns[id] = ch
	return id, ch
}

// Unregister removes a client by ID and closes its channel.
func (h *ClientHub) Unregister(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ch, ok := h.conns[id]; ok {
		delete(h.conns, id)
		close(ch)
	}
}

// Broadcast sends data to all connected clients. Non-blocking: drops
// messages for clients whose buffers are full.
func (h *ClientHub) Broadcast(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.conns {
		select {
		case ch <- data:
		default:
		}
	}
}
