package engine

import (
	"context"
	"stuntgres/internal/engine/session"
	"testing"
)

func TestSessionPayloadHoldsRootResources(t *testing.T) {
	e := NewWithSettings(nil, "", false)
	s := sessionStart(t, e, nil)
	p := payloadOf(s)
	if p == nil {
		t.Fatal("started session has no payload")
	}
	if p.cursors == nil || p.cursors.token != s.SessionToken {
		t.Fatalf("payload cursors = %+v, want a registry for %v", p.cursors, s.SessionToken)
	}
	if p.preparedSQL == nil || p.preparedSQL.token != s.SessionToken {
		t.Fatalf("payload prepared registry = %+v, want a registry for %v", p.preparedSQL, s.SessionToken)
	}
	if p.notifications.Session != s || engineOf(p.notifications.Session) != e {
		t.Fatal("payload listener is not bound to its session and the session's engine")
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if payloadOf(s) != p {
		t.Fatal("session end replaced the payload")
	}
	if !p.notifications.Ended {
		t.Fatal("session end did not end the payload listener")
	}
}

func TestSessionPayloadOfLiteralIsNil(t *testing.T) {
	if p := payloadOf(&session.Session{}); p != nil {
		t.Fatalf("payloadOf(&Session{}) = %p, want nil", p)
	}
}
