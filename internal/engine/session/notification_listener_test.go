package session

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func notificationTestListener(domain *ids.Domain, id ids.SessionID) *NotificationListener {
	return &NotificationListener{Session: &Session{SessionToken: domain.Session(id)}}
}

func TestNotificationRegistryEndLocked(t *testing.T) {
	domain := &ids.Domain{}
	registered, unregistered := notificationTestListener(domain, 1), notificationTestListener(domain, 2)
	// A different listener under the unregistered listener's token (a failed
	// duplicate start) must survive the unregistered listener's end.
	holder := notificationTestListener(domain, 2)
	r := NotificationRegistry{registered.Session.SessionToken: registered, holder.Session.SessionToken: holder}
	for _, n := range []*NotificationListener{registered, unregistered} {
		wake := make(chan struct{})
		n.State, n.Wake = &NotificationState{History: true}, wake
		r.EndLocked(n)
		if !n.Ended || n.State != nil || n.Wake != nil {
			t.Fatalf("ended listener kept state: ended %v, state %v, wake %v", n.Ended, n.State, n.Wake)
		}
		select {
		case <-wake:
		default:
			t.Fatal("end did not wake the waiter")
		}
	}
	if _, ok := r[registered.Session.SessionToken]; ok {
		t.Fatal("end kept the listener's registration")
	}
	if r[holder.Session.SessionToken] != holder {
		t.Fatal("end removed another listener's registration")
	}

	// Ending twice is a no-op: no state change, registry edit or wake.
	r[registered.Session.SessionToken] = registered
	wake := make(chan struct{})
	registered.Wake = wake
	r.EndLocked(registered)
	if r[registered.Session.SessionToken] != registered || registered.Wake != wake {
		t.Fatal("repeated end changed the registry or the wake")
	}
}
