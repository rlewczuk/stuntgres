package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestSessionHostRecoversEngine(t *testing.T) {
	e := engine.NewWithSettings(nil, "", false)
	public := sessionStart(t, e, nil)
	private := collationPrivateSession(t, e)
	for name, s := range map[string]*session.Session{"public": public, "private": private} {
		if got := core.EngineOf(s); got != e {
			t.Fatalf("%s session: engineOf = %p, want %p", name, got, e)
		}
	}
	if got := core.EngineOf(&session.Session{}); got != nil {
		t.Fatalf("host-less session: engineOf = %p, want nil", got)
	}
}

func TestSessionHostUnregisterKeepsOtherOwner(t *testing.T) {
	e := engine.NewWithSettings(nil, "", false)
	s := sessionStart(t, e, nil)
	host := s.Host
	other := &session.Session{SessionToken: s.SessionToken}
	core.EngineSessionMuRef(e).Lock()
	registered := core.EngineSessions(e)[s.SessionToken] == s
	core.EngineSessionMuRef(e).Unlock()
	if !registered {
		t.Fatal("live session is not registered")
	}
	host.Unregister(s.SessionToken, other)
	core.EngineSessionMuRef(e).Lock()
	kept := core.EngineSessions(e)[s.SessionToken] == s
	core.EngineSessionMuRef(e).Unlock()
	if !kept {
		t.Fatal("Unregister with another owner removed the registered session")
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	core.EngineSessionMuRef(e).Lock()
	_, stays := core.EngineSessions(e)[s.SessionToken]
	core.EngineSessionMuRef(e).Unlock()
	if stays {
		t.Fatal("ended session is still registered")
	}
	var _ ids.SessionOwner = s
}
