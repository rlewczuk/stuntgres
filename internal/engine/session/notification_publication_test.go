package session

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
)

func notificationTestListening(domain *ids.Domain, id ids.SessionID, channels ...string) *NotificationListener {
	n := notificationTestListener(domain, id)
	n.State = &NotificationState{Channels: map[string]struct{}{}, History: true}
	for _, channel := range channels {
		n.State.Channels[channel] = struct{}{}
	}
	return n
}

func notificationTestRegistry(listeners ...*NotificationListener) NotificationRegistry {
	r := make(NotificationRegistry, len(listeners))
	for _, n := range listeners {
		r[n.Session.SessionToken] = n
	}
	return r
}

func notificationTestPrepared(r *NotificationRegistry, own *NotificationListener, actions []NotificationAction, events []Notification) *NotificationPublication {
	p := &NotificationPublication{}
	r.CaptureLocked(p, own)
	p.Prepare(actions, events)
	return p
}

func TestNotificationPublicationStaleCapture(t *testing.T) {
	events := []Notification{{PID: 9, Channel: "c", Payload: "p"}}
	for _, change := range []string{"none", "busy", "state", "ended", "unregistered", "membership"} {
		t.Run(change, func(t *testing.T) {
			domain := &ids.Domain{}
			a, b, c := notificationTestListening(domain, 1, "c"), notificationTestListening(domain, 2, "c"), notificationTestListening(domain, 3, "c")
			own := notificationTestListener(domain, 4)
			r := notificationTestRegistry(a, b)
			p := notificationTestPrepared(&r, own, nil, events)
			switch change {
			case "busy":
				a.Busy = true
			case "state":
				next := *a.State
				a.State = &next
			case "ended":
				a.Ended = true
			case "unregistered":
				delete(r, a.Session.SessionToken)
			case "membership":
				// Same registration count, different members.
				delete(r, b.Session.SessionToken)
				r[c.Session.SessionToken] = c
			}
			if got, want := r.UnchangedLocked(p), change == "none"; got != want {
				t.Fatalf("UnchangedLocked = %v, want %v", got, want)
			}
		})
	}
	var r NotificationRegistry
	if !r.UnchangedLocked(nil) {
		t.Fatal("an empty publication was stale")
	}
	r.InstallLocked(nil)
	if r != nil {
		t.Fatal("installing an empty publication allocated a registry")
	}
}

func TestNotificationPublicationPrepareAndInstall(t *testing.T) {
	domain := &ids.Domain{}
	idle, busy, ended := notificationTestListening(domain, 1, "x"), notificationTestListening(domain, 2, "x"), notificationTestListening(domain, 3, "x")
	busy.Busy = true
	r := notificationTestRegistry(idle, busy, ended)
	ended.Ended = true
	own := notificationTestListener(domain, 4)
	wakes := map[*NotificationListener]chan struct{}{}
	for _, n := range []*NotificationListener{idle, busy, ended, own} {
		wakes[n] = make(chan struct{})
		n.Wake = wakes[n]
	}
	events := []Notification{{PID: 4, Channel: "x", Payload: "one"}, {PID: 4, Channel: "x", Payload: "two"}}
	idleState, endedState := idle.State, ended.State
	p := notificationTestPrepared(&r, own, []NotificationAction{{Channel: "x", Listen: true}}, events)
	if idle.State != idleState || own.State != nil {
		t.Fatal("Prepare installed state")
	}
	r.InstallLocked(p)

	if got := idle.State.Ready.Back; got == nil || got.Next != nil || &got.Batch[0] != &events[0] {
		t.Fatal("idle recipient did not get the shared ready batch")
	}
	if got := busy.State.Pending; got == nil || &got.Batch[0] != &events[0] || busy.State.Ready.Back != nil {
		t.Fatal("busy recipient did not hold the batch until its boundary")
	}
	if got := own.State; got == nil || !got.History || len(got.Channels) != 1 || got.Ready.Back == nil || &got.Ready.Back.Batch[0] != &events[0] {
		t.Fatal("sender did not listen and receive its own events", got)
	}
	if ended.State != endedState || ended.Used.Load() {
		t.Fatal("ended recipient changed")
	}
	for _, n := range []*NotificationListener{idle, busy, own} {
		if !n.Used.Load() || n.Wake != nil {
			t.Fatal("changed recipient not marked used or not woken")
		}
		select {
		case <-wakes[n]:
		default:
			t.Fatal("changed recipient's wake still open")
		}
	}
	if ended.Wake == nil {
		t.Fatal("unchanged recipient woken")
	}
	want := notificationTestRegistry(idle, busy, own)
	if !reflect.DeepEqual(r, want) {
		t.Fatal("registration change did not rebuild the registry without ended listeners", r)
	}

	// Events alone keep the installed registry map.
	before := reflect.ValueOf(r).Pointer()
	r.InstallLocked(notificationTestPrepared(&r, own, nil, events[:1]))
	if reflect.ValueOf(r).Pointer() != before {
		t.Fatal("events without registration change replaced the registry")
	}

	// UNLISTEN of the last channel clears pending events and unregisters.
	own.State = &NotificationState{Channels: own.State.Channels, History: true, Pending: &NotificationLink{Batch: events}}
	r.InstallLocked(notificationTestPrepared(&r, own, []NotificationAction{{Channel: "x"}}, nil))
	if len(own.State.Channels) != 0 || own.State.Pending != nil || !own.State.History {
		t.Fatal("UNLISTEN kept channels or pending events", own.State)
	}
	if _, ok := r[own.Session.SessionToken]; ok {
		t.Fatal("UNLISTEN kept the registration")
	}
	// UNLISTEN * clears every channel of a registered listener.
	r.InstallLocked(notificationTestPrepared(&r, idle, []NotificationAction{{All: true}}, nil))
	if len(idle.State.Channels) != 0 || r[idle.Session.SessionToken] != nil || r[busy.Session.SessionToken] != busy {
		t.Fatal("UNLISTEN * did not unregister only its listener")
	}
}
