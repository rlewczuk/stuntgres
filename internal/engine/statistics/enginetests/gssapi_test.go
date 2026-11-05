package statistics_test

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
)

// Provider unit fixtures own inert principal provenance, just as they own their
// session-token domain. Composed tests separately prove real login admission.
func statsLogin() authorization.PrincipalRef {
	return new(authorization.Domain).Principal(10000)
}

func TestStatsProviderGSSAPILifetime(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			var p statistics.Provider
			domain := new(ids.Domain)
			a, b, unknown := domain.Session(1), domain.Session(2), domain.Session(3)
			loginA, loginB := statsLogin(), statsLogin()
			var empty statistics.Reader
			empty.Configure(mode, 1)
			if got := empty.Activities(&p); got == nil || len(got) != 0 {
				t.Fatal("empty capture", got)
			}
			p.Command(unknown, ids.CommandIdentity{Command: 1}, "unregistered", time.Unix(100, 0), true)
			privateOwner := new(struct{ ids.SessionOwnerMarker })
			for _, invalid := range []ids.SessionToken{{}, domain.Session(0), domain.DetachedSession(4), domain.PrivateSession(privateOwner)} {
				p.RegisterActivity(invalid, statistics.Activity{Principal: loginA})
				p.Command(invalid, ids.CommandIdentity{Command: 1}, "invalid", time.Time{}, true)
			}
			p.RegisterActivity(a, statistics.Activity{})
			if got := p.CaptureActivities(); len(got) != 0 {
				t.Fatal("zero principal admitted", got)
			}
			if !p.BeginBackend(a, 1) || !p.BeginBackend(b, 2) {
				t.Fatal("public lifetime rejected")
			}
			p.RegisterActivity(a, statistics.Activity{Principal: loginA})
			p.RegisterActivity(b, statistics.Activity{Principal: loginB})
			p.RegisterActivity(a, statistics.Activity{Principal: loginB})
			if got := empty.Activities(&p); len(got) != 0 {
				t.Fatal("empty membership not cached", got)
			}
			empty.Clear()
			initial := empty.Activities(&p)
			if len(initial) != 2 || initial[a] != (statistics.Activity{Principal: loginA, Incarnation: 1, BackendID: 0}) || initial[b] != (statistics.Activity{Principal: loginB, Incarnation: 2, BackendID: 1}) {
				t.Fatal("initial attachment facts", initial)
			}
			delete(initial, a)
			if len(empty.Activities(&p)) != 2 {
				t.Fatal("caller mutated reader capture")
			}
			at := time.Unix(100, 0)
			p.Command(a, ids.CommandIdentity{Command: 1}, "sensitive", at, true)
			var active statistics.Reader
			active.Configure(mode, 1)
			captured := active.Activities(&p)
			if !captured[a].Active || captured[a].Principal != loginA {
				t.Fatal("command lost immutable login", captured)
			}
			p.Command(a, ids.CommandIdentity{Command: 2}, "disabled secret", at, false)
			var disabled statistics.Reader
			if got := disabled.Activities(&p)[a]; got.Principal != loginA || !got.Disabled || !got.Active || got.State != "disabled" || got.Query != "" || !got.Start.IsZero() || !got.TransactionStart.IsZero() || got.QueryID != 0 {
				t.Fatal("disabled activity leaked detail or removed client", got)
			}
			for _, reset := range []func(){
				func() { p.Reset(at) },
				func() { p.Replace(at, nil) },
				func() { p.ResetAllShared(at) },
			} {
				reset()
				var fresh statistics.Reader
				if got := fresh.Activities(&p); len(got) != 2 || got[a].Principal != loginA || got[b].Principal != loginB {
					t.Fatal("reset retired live membership", got)
				}
				if f, ok := p.ReadBackend(a); !ok || f != (statistics.BackendFact{}) {
					t.Fatal("registered backend must be logically present without history", f, ok)
				}
			}
			var target statistics.Provider
			target.InheritBackendRegistrations(&p)
			var inherited statistics.Reader
			if got := inherited.Activities(&target); len(got) != 0 || backendRegistrationCount(&target) != 2 {
				t.Fatal("target duplicated cluster activity or missed backend authority", got)
			}
			p.End(b)
			p.Command(b, ids.CommandIdentity{Command: 3}, "late", at, true)
			p.ReportIdle(b, "", "idle", ids.TxRef{}, time.Time{}, at)
			if got := active.Activities(&p); !reflect.DeepEqual(got, captured) {
				t.Fatal("retained activity changed", got, captured)
			}
			active.Clear()
			if got := active.Activities(&p); len(got) != 1 || got[a].Principal != loginA {
				t.Fatal("clear did not refresh retirement", got)
			}
			if !active.Timestamp().IsZero() || !empty.Timestamp().IsZero() {
				t.Fatal("activity initialized cumulative timestamp")
			}
			p.End(a)
			p.Command(a, ids.CommandIdentity{}, "after end", at, true)
			p.ReportIdle(a, "", "idle", ids.TxRef{}, time.Time{}, at)
			var ended statistics.Reader
			if len(ended.Activities(&p)) != 0 {
				t.Fatal("late command resurrected lifetime")
			}
			if len(inherited.Activities(&target)) != 0 || backendRegistrationCount(&target) != 2 {
				t.Fatal("backend inheritance aliases source or copied activity")
			}
		})
	}
	t.Run("concurrent_capture_end_and_target", func(t *testing.T) {
		var p, target statistics.Provider
		token, login := new(ids.Domain).Session(1), statsLogin()
		p.BeginBackend(token, 1)
		p.RegisterActivity(token, statistics.Activity{Principal: login})
		gate := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(3)
		go func() {
			defer workers.Done()
			<-gate
			p.Command(token, ids.CommandIdentity{Command: 1}, "command", time.Unix(100, 0), true)
		}()
		go func() { defer workers.Done(); <-gate; target.InheritBackendRegistrations(&p) }()
		var capture map[ids.SessionToken]statistics.Activity
		go func() { defer workers.Done(); <-gate; var reader statistics.Reader; capture = reader.Activities(&p) }()
		close(gate)
		workers.Wait()
		if len(capture) != 1 || capture[token].Principal != login {
			t.Fatal("incoherent concurrent capture", capture)
		}
		var inherited statistics.Reader
		if got := inherited.Activities(&target); len(got) != 0 || backendRegistrationCount(&target) != 1 {
			t.Fatal("concurrent target duplicated cluster activity or missed backend authority", got)
		}
		gate = make(chan struct{})
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-gate
			p.End(token)
			p.Command(token, ids.CommandIdentity{}, "late", time.Time{}, true)
		}()
		go func() { defer workers.Done(); <-gate; var reader statistics.Reader; capture = reader.Activities(&p) }()
		close(gate)
		workers.Wait()
		if len(capture) > 1 || len(capture) == 1 && capture[token].Principal != login {
			t.Fatal("end capture mismatched login", capture)
		}
		var fresh statistics.Reader
		if len(fresh.Activities(&p)) != 0 {
			t.Fatal("ended token reappeared")
		}
	})
}
