package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

func TestSessionPayloadHoldsRootResources(t *testing.T) {
	e := engine.NewWithSettings(nil, "", false)
	s := sessionStart(t, e, nil)
	p := core.PayloadOf(s)
	if p == nil {
		t.Fatal("started session has no payload")
	}
	if p.Cursors == nil || core.CursorsToken(p.Cursors) != s.SessionToken {
		t.Fatalf("payload cursors = %+v, want a registry for %v", p.Cursors, s.SessionToken)
	}
	if core.PayloadPreparedSQL(p) == nil || core.PreparedRegistryToken(core.PayloadPreparedSQL(p)) != s.SessionToken {
		t.Fatalf("payload prepared registry = %+v, want a registry for %v", core.PayloadPreparedSQL(p), s.SessionToken)
	}
	if core.PayloadNotifications(p).Session != s || core.EngineOf(core.PayloadNotifications(p).Session) != e {
		t.Fatal("payload listener is not bound to its session and the session's engine")
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if core.PayloadOf(s) != p {
		t.Fatal("session end replaced the payload")
	}
	if !core.PayloadNotifications(p).Ended {
		t.Fatal("session end did not end the payload listener")
	}
}

func TestSessionPayloadOfLiteralIsNil(t *testing.T) {
	if p := core.PayloadOf(&session.Session{}); p != nil {
		t.Fatalf("payloadOf(&Session{}) = %p, want nil", p)
	}
}
