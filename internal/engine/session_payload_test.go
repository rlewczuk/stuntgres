package engine

import (
	"context"
	"stuntgres/internal/engine/session"
	"testing"
)

func TestSessionPayloadHoldsRootResources(t *testing.T) {
	e := NewWithSettings(nil, "", false)
	s := sessionStart(t, e, nil)
	p := PayloadOf(s)
	if p == nil {
		t.Fatal("started session has no payload")
	}
	if p.Cursors == nil || p.Cursors.token != s.SessionToken {
		t.Fatalf("payload cursors = %+v, want a registry for %v", p.Cursors, s.SessionToken)
	}
	if PayloadPreparedSQL(p) == nil || PayloadPreparedSQL(p).token != s.SessionToken {
		t.Fatalf("payload prepared registry = %+v, want a registry for %v", PayloadPreparedSQL(p), s.SessionToken)
	}
	if PayloadNotifications(p).Session != s || EngineOf(PayloadNotifications(p).Session) != e {
		t.Fatal("payload listener is not bound to its session and the session's engine")
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if PayloadOf(s) != p {
		t.Fatal("session end replaced the payload")
	}
	if !PayloadNotifications(p).Ended {
		t.Fatal("session end did not end the payload listener")
	}
}

func TestSessionPayloadOfLiteralIsNil(t *testing.T) {
	if p := PayloadOf(&session.Session{}); p != nil {
		t.Fatalf("payloadOf(&Session{}) = %p, want nil", p)
	}
}
