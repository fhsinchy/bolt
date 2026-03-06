package event

import "sync"

// Event is the interface all events implement.
type Event interface {
	EventType() string
}

// Progress is emitted periodically while a download is active.
type Progress struct {
	DownloadID string
	Downloaded int64
	TotalSize  int64
	Speed      int64
	ETA        int
	Status     string
}

func (Progress) EventType() string { return "progress" }

// DownloadAdded is emitted when a new download is enqueued.
type DownloadAdded struct {
	DownloadID string
	Filename   string
	TotalSize  int64
}

func (DownloadAdded) EventType() string { return "download_added" }

// DownloadCompleted is emitted when a download finishes successfully.
type DownloadCompleted struct {
	DownloadID string
	Filename   string
}

func (DownloadCompleted) EventType() string { return "download_completed" }

// DownloadFailed is emitted when a download terminates with an error.
type DownloadFailed struct {
	DownloadID string
	Error      string
}

func (DownloadFailed) EventType() string { return "download_failed" }

// DownloadRemoved is emitted when a download is deleted from the system.
type DownloadRemoved struct {
	DownloadID string
}

func (DownloadRemoved) EventType() string { return "download_removed" }

// DownloadPaused is emitted when a download is paused.
type DownloadPaused struct {
	DownloadID string
}

func (DownloadPaused) EventType() string { return "download_paused" }

// DownloadResumed is emitted when a download is resumed.
type DownloadResumed struct {
	DownloadID string
}

func (DownloadResumed) EventType() string { return "download_resumed" }

// RefreshNeeded is emitted when a download's metadata should be re-fetched.
type RefreshNeeded struct {
	DownloadID string
}

func (RefreshNeeded) EventType() string { return "refresh_needed" }

// DuplicateDetected is emitted when a new download matches an existing one by filename.
type DuplicateDetected struct {
	ExistingID string
	Filename   string
	Status     string
	NewURL     string
	NewHeaders map[string]string
}

func (DuplicateDetected) EventType() string { return "duplicate_detected" }

// WindowShow is emitted to signal the GUI window should be raised.
type WindowShow struct{}

func (WindowShow) EventType() string { return "window_show" }

const subscriberBufferSize = 256

// Bus is a publish/subscribe event bus.
// Publish takes a read lock so multiple goroutines can publish concurrently.
// Subscribe and Unsubscribe take a write lock to mutate the subscriber map.
type Bus struct {
	mu     sync.RWMutex
	subs   map[int]chan Event
	nextID int
}

// NewBus creates a ready-to-use Bus.
func NewBus() *Bus {
	return &Bus{
		subs: make(map[int]chan Event),
	}
}

// Subscribe registers a new subscriber and returns a buffered channel that
// will receive published events, along with a subscription ID that can be
// passed to Unsubscribe.
func (b *Bus) Subscribe() (<-chan Event, int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++

	ch := make(chan Event, subscriberBufferSize)
	b.subs[id] = ch

	return ch, id
}

// Unsubscribe removes the subscriber identified by id and closes its channel.
func (b *Bus) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(ch)
	}
}

// SubscriberCount returns the current number of subscribers.
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Publish sends an event to every subscriber. The send is non-blocking: if a
// subscriber's buffer is full the event is silently dropped for that subscriber.
func (b *Bus) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subs {
		select {
		case ch <- event:
		default:
			// subscriber buffer full, drop
		}
	}
}
