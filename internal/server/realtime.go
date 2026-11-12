package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type realtimeEvent struct {
	LibraryID string `json:"libraryId,omitempty"`
	Kind      string `json:"kind"`
	Revision  uint64 `json:"revision"`
}

type realtimeHub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan realtimeEvent]struct{}
	revisions   map[string]uint64
}

func newRealtimeHub() *realtimeHub {
	return &realtimeHub{
		subscribers: make(map[string]map[chan realtimeEvent]struct{}),
		revisions:   make(map[string]uint64),
	}
}

func (h *realtimeHub) subscribe(libraryID string) (chan realtimeEvent, func()) {
	ch := make(chan realtimeEvent, 1)
	h.mu.Lock()
	if h.subscribers[libraryID] == nil {
		h.subscribers[libraryID] = make(map[chan realtimeEvent]struct{})
	}
	h.subscribers[libraryID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subscribers[libraryID], ch)
		if len(h.subscribers[libraryID]) == 0 {
			delete(h.subscribers, libraryID)
		}
		h.mu.Unlock()
	}
}

func (h *realtimeHub) publish(libraryID, kind string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revisions[libraryID]++
	event := realtimeEvent{LibraryID: libraryID, Kind: kind, Revision: h.revisions[libraryID]}
	for channel := range h.subscribers[libraryID] {
		select {
		case channel <- event:
		default:
			select {
			case <-channel:
			default:
			}
			channel <- event
		}
	}
}

func (h *realtimeHub) revision(libraryID string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.revisions[libraryID]
}

func (h *realtimeHub) publishAll(kind string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for libraryID, subscribers := range h.subscribers {
		h.revisions[libraryID]++
		event := realtimeEvent{LibraryID: libraryID, Kind: kind, Revision: h.revisions[libraryID]}
		for channel := range subscribers {
			select {
			case channel <- event:
			default:
				select {
				case <-channel:
				default:
				}
				channel <- event
			}
		}
	}
}

func realtimeHandler(state *diaryRuntime, hub *realtimeHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		layout, _, err := state.active()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		channel, unsubscribe := hub.subscribe(layout.Manifest.ID)
		defer unsubscribe()
		ready, _ := json.Marshal(realtimeEvent{LibraryID: layout.Manifest.ID, Kind: "ready", Revision: hub.revision(layout.Manifest.ID)})
		_, _ = fmt.Fprintf(w, "retry: 2000\n\nevent: ready\ndata: %s\n\n", ready)
		flusher.Flush()
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case event := <-channel:
				data, _ := json.Marshal(event)
				if _, err := fmt.Fprintf(w, "event: change\ndata: %s\n\n", data); err != nil {
					return
				}
				flusher.Flush()
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
