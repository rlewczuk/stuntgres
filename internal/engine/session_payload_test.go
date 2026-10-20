package engine

import (
	"context"
	"testing"
)

func TestSessionPayloadHoldsRootResources(t *testing.T) {
	e := NewWithSettings(nil, "", false)
	s := sessionStart(t, e, nil)
	p := payloadOf(s)
	if p == nil {
		t.Fatal("started session has no payload")
	}
	if p.cursors == nil || p.cursors.token != s.token {
		t.Fatalf("payload cursors = %+v, want a registry for %v", p.cursors, s.token)
	}
	if p.preparedSQL == nil || p.preparedSQL.token != s.token {
		t.Fatalf("payload prepared registry = %+v, want a registry for %v", p.preparedSQL, s.token)
	}
	if p.notifications.engine != e || p.notifications.token != s.token {
		t.Fatal("payload listener is not bound to the session's engine and token")
	}
	s.RequestEnd(CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if payloadOf(s) != p {
		t.Fatal("session end replaced the payload")
	}
	if !p.notifications.ended {
		t.Fatal("session end did not end the payload listener")
	}
}

func TestSessionPayloadOfLiteralIsNil(t *testing.T) {
	if p := payloadOf(&Session{}); p != nil {
		t.Fatalf("payloadOf(&Session{}) = %p, want nil", p)
	}
}
