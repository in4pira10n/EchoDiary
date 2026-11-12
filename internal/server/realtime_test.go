package server

import "testing"

func TestRealtimeHubScopesAndCoalescesEvents(t *testing.T) {
	hub := newRealtimeHub()
	first, stopFirst := hub.subscribe("library-a")
	defer stopFirst()
	second, stopSecond := hub.subscribe("library-b")
	defer stopSecond()

	hub.publish("library-a", "notes")
	if event := <-first; event.LibraryID != "library-a" || event.Kind != "notes" {
		t.Fatalf("library A event = %#v", event)
	}
	select {
	case event := <-second:
		t.Fatalf("library B received library A event: %#v", event)
	default:
	}

	hub.publish("library-a", "notes")
	hub.publish("library-a", "folders")
	if event := <-first; event.LibraryID != "library-a" || event.Kind != "folders" {
		t.Fatalf("coalesced event = %#v; want latest change", event)
	}
}

func TestRealtimeHubPublishesLibrarySwitchToAllSubscribers(t *testing.T) {
	hub := newRealtimeHub()
	first, stopFirst := hub.subscribe("library-a")
	defer stopFirst()
	second, stopSecond := hub.subscribe("library-b")
	defer stopSecond()

	hub.publishAll("library")
	for _, channel := range []<-chan realtimeEvent{first, second} {
		if event := <-channel; event.Kind != "library" {
			t.Fatalf("library switch event = %#v", event)
		}
	}
}
