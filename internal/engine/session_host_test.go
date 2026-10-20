package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestSessionHostRecoversEngine(t *testing.T) {
	e := NewWithSettings(nil, "", false)
	public := sessionStart(t, e, nil)
	private := collationPrivateSession(t, e)
	for name, s := range map[string]*session.Session{"public": public, "private": private} {
		if got := engineOf(s); got != e {
			t.Fatalf("%s session: engineOf = %p, want %p", name, got, e)
		}
	}
	if got := engineOf(&session.Session{}); got != nil {
		t.Fatalf("host-less session: engineOf = %p, want nil", got)
	}
}

func TestSessionHostUnregisterKeepsOtherOwner(t *testing.T) {
	e := NewWithSettings(nil, "", false)
	s := sessionStart(t, e, nil)
	host := s.Host
	other := &session.Session{SessionToken: s.SessionToken}
	e.sessionMu.Lock()
	registered := e.sessions[s.SessionToken] == s
	e.sessionMu.Unlock()
	if !registered {
		t.Fatal("live session is not registered")
	}
	host.Unregister(s.SessionToken, other)
	e.sessionMu.Lock()
	kept := e.sessions[s.SessionToken] == s
	e.sessionMu.Unlock()
	if !kept {
		t.Fatal("Unregister with another owner removed the registered session")
	}
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.sessionMu.Lock()
	_, stays := e.sessions[s.SessionToken]
	e.sessionMu.Unlock()
	if stays {
		t.Fatal("ended session is still registered")
	}
	var _ ids.SessionOwner = s
}
