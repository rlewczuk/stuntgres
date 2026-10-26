package core_test

import (
	"context"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestSessionPayloadHoldsRootResources(t *testing.T) {
	e := engine.NewWithSettings(nil, "", false)
	s := enginetest.StartSession(t, e, nil)
	p := core.PayloadOf(s)
	if p == nil {
		t.Fatal("started session has no payload")
	}
	if p.Cursors == nil || core.CursorsToken(p.Cursors) != s.SessionToken {
		t.Fatalf("payload cursors = %+v, want a registry for %v", p.Cursors, s.SessionToken)
	}
	if p.PreparedSQL == nil {
		t.Fatal("payload has no prepared registry")
	}
	peer := enginetest.StartSession(t, e, nil)
	definition := &core.PreparedDefinition{}
	p.PreparedSQL.Install("owned", definition)
	if err := p.PreparedSQL.EndSession(peer.SessionToken, session.CleanClose); err == nil || p.PreparedSQL.Lookup("owned") != definition {
		t.Fatal("foreign session could end prepared registry", err)
	}
	if core.PayloadNotifications(p).Session != s || core.EngineOf(core.PayloadNotifications(p).Session) != e {
		t.Fatal("payload listener is not bound to its session and the session's engine")
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.PreparedSQL.Lookup("owned") != nil {
		t.Fatal("owning session did not end prepared registry")
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
