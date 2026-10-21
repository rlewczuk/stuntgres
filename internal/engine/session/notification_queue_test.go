package session

import (
	"slices"
	"testing"
)

func drainNotifications(q NotificationFIFO) []string {
	var got []string
	for {
		q = q.Normalize()
		if q.Front == nil {
			return got
		}
		got = append(got, q.Front.Batch[q.Offset].Payload)
		q.Offset++
		if q.Offset == len(q.Front.Batch) {
			q.Front, q.Offset = q.Front.Next, 0
		}
	}
}

func event(channel, payload string) Notification {
	return Notification{PID: 1, Channel: channel, Payload: payload}
}

func TestNotificationQueueOrder(t *testing.T) {
	var q NotificationFIFO
	q = q.Append([]Notification{event("c", "1"), event("c", "2")}).Append(nil).Append([]Notification{event("c", "3")})
	shared := q
	q = q.Normalize()
	q.Offset = 1
	q = q.Append([]Notification{event("c", "4")})
	if got, want := drainNotifications(q), []string{"2", "3", "4"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Normalizing a copy leaves the original value's links intact.
	if got, want := drainNotifications(shared), []string{"1", "2", "3"}; !slices.Equal(got, want) {
		t.Fatalf("shared got %v, want %v", got, want)
	}
}

func TestNotificationQueueFilterAndScan(t *testing.T) {
	channels := map[string]struct{}{"a": {}}
	all := []Notification{event("a", "1"), event("a", "2")}
	if got := FilterNotificationBatch(all, channels); &got[0] != &all[0] || len(got) != 2 {
		t.Fatalf("matching batch was copied: %v", got)
	}
	if got := FilterNotificationBatch([]Notification{event("b", "x")}, channels); got != nil {
		t.Fatalf("unmatched batch kept %v", got)
	}
	mixed := []Notification{event("b", "x"), event("a", "3")}
	if got := FilterNotificationBatch(mixed, channels); len(got) != 1 || got[0].Payload != "3" || mixed[0].Payload != "x" {
		t.Fatalf("mixed batch filtered to %v, source %v", got, mixed)
	}
	// Pending lists are newest-first; scanning delivers them oldest-first.
	pending := &NotificationLink{Batch: mixed, Next: &NotificationLink{Batch: []Notification{event("b", "y")}, Next: &NotificationLink{Batch: all}}}
	ready := NotificationFIFO{}.Append([]Notification{event("a", "0")})
	if got, want := drainNotifications(ScanNotificationPending(ready, pending, channels)), []string{"0", "1", "2", "3"}; !slices.Equal(got, want) {
		t.Fatalf("scan got %v, want %v", got, want)
	}
	if got := ScanNotificationPending(ready, nil, channels); got != ready {
		t.Fatal("empty scan changed the queue")
	}
}

func TestNotificationQueueAppendEvents(t *testing.T) {
	single := []Notification{event("a", "1")}
	if got := AppendNotificationEvents(nil, single); &got[0] != &single[0] {
		t.Fatal("first single event was copied")
	}
	base := []Notification{event("a", "1"), event("b", "1")}
	got := AppendNotificationEvents(base, []Notification{event("a", "1"), event("a", "2"), {PID: 9, Channel: "b", Payload: "1"}})
	want := []Notification{event("a", "1"), event("b", "1"), event("a", "2")}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
