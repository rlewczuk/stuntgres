package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type databaseAdmissionDefaults struct {
	apply func()
}

func (p *databaseAdmissionDefaults) SessionDefault(_ context.Context, d SettingDefinition) (string, bool, error) {
	if d.Name == "application_name" && p.apply != nil {
		apply := p.apply
		p.apply = nil
		apply()
	}
	return "", false, nil
}

func databaseAdmissionFixture() (*Engine, authorization.PrincipalRef) {
	e, _, _, _, reader := ownershipUnitRoles()
	roles := cloneRoleFixture(e.roles)
	login := fixtureRole(roles, reader)
	login.Login = true
	login.ConnectionLimit = 1
	putFixtureRole(roles, login)
	e.roles = roles
	return e, reader
}

func databaseAdmissionPolicyLocked(e *Engine, acl authorization.ACLValue) {
	candidate := cloneCatalog(e.catalog.state)
	db := *candidate.MustDatabase(currentDatabaseAddress)
	db.revision++
	db.acl = acl
	candidate.setDescriptor(currentDatabaseAddress, &db)
	e.catalog.state = candidate
}

func TestDatabaseAdmissionPublicationAndCleanup(t *testing.T) {
	for _, phase := range []string{"revoke", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			e, login := databaseAdmissionFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := &databaseAdmissionDefaults{apply: func() {
				// Host initialization callbacks must run outside the catalog lock.
				// Change the actual root after startup captured login eligibility.
				e.mu.Lock()
				databaseAdmissionPolicyLocked(e, authorization.ACLValue{Explicit: true})
				e.mu.Unlock()
				if phase == "cancel" {
					cancel()
				}
			}}
			id := ids.SessionID(testSessionID.Add(1))
			s, err := e.StartSession(ctx, SessionStart{ID: id, Authenticated: login, Defaults: provider})
			if s != nil {
				s.RequestEnd(CleanClose)
				_ = s.WaitEnd(context.Background())
				t.Fatal("failed physical admission published a session")
			}
			if phase == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation lost", err)
				}
			} else {
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `permission denied for database "` + e.databaseName + `"`, Detail: "User does not have CONNECT privilege."}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
					t.Fatal("fresh captured CONNECT denial", err, want)
				}
			}
			if len(e.sessions) != 0 || e.roleSessionCountLocked(login) != 0 {
				t.Fatal("failed initialization consumed registry/count resources")
			}
			e.mu.Lock()
			databaseAdmissionPolicyLocked(e, authorization.ACLValue{})
			e.mu.Unlock()
			// Reusing the exact failed identity exercises advisory-ticket cleanup,
			// not merely absence from the public session registry.
			live, err := e.StartSession(context.Background(), SessionStart{ID: id, Authenticated: login})
			if err != nil {
				t.Fatal("failed admission retained lifecycle resources", err)
			}
			live.RequestEnd(CleanClose)
			if err := live.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(e.sessions) != 0 || e.roleSessionCountLocked(login) != 0 {
				t.Fatal("physical close leaked authenticated count")
			}
		})
	}
}

func TestDatabaseAdmissionRetainedSession(t *testing.T) {
	e, login := databaseAdmissionFixture()
	live, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		live.RequestEnd(CleanClose)
		if err := live.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	e.mu.Lock()
	databaseAdmissionPolicyLocked(e, authorization.ACLValue{Explicit: true})
	e.mu.Unlock()
	op, err := live.Acquire(context.Background())
	if err != nil {
		t.Fatal("CONNECT revoke terminated admitted session", err)
	}
	op.Done()
	if !live.IsLive() || live.countedAuthenticated != login || e.roleSessionCountLocked(login) != 1 {
		t.Fatal("revocation changed physical lifetime or authenticated count")
	}
	fresh, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login})
	if fresh != nil {
		fresh.RequestEnd(CleanClose)
		_ = fresh.WaitEnd(context.Background())
		t.Fatal("fresh session admitted after revoke")
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Detail != "User does not have CONNECT privilege." {
		t.Fatal("CONNECT denial must precede exhausted role connection count", err)
	}
	if e.roleSessionCountLocked(login) != 1 {
		t.Fatal("denied session consumed connection count")
	}
}

func TestDatabaseAdmissionAuthenticatedNotResetRole(t *testing.T) {
	for _, allowLogin := range []bool{false, true} {
		e, owner, reset, _, login := ownershipUnitRoles()
		roles := cloneRoleFixture(e.roles)
		actor := fixtureRole(roles, login)
		actor.Login = true
		actor.ConnectionLimit = -1
		putFixtureRole(roles, actor)
		setFixtureEdges(roles, []authorization.Membership{{Role: reset, Member: login, Grantor: owner, Set: true}})
		e.roles = roles
		grantee := reset
		if allowLogin {
			grantee = login
		}
		databaseAdmissionPolicyLocked(e, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: grantee}, Privileges: authorization.ConnectPrivilege}}})
		start := SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login, Defaults: roleSwitchDefaultProvider{value: fixtureRole(roles, reset).Name}}
		s, err := e.StartSession(context.Background(), start)
		if !allowLogin {
			if s != nil {
				s.RequestEnd(CleanClose)
				_ = s.WaitEnd(context.Background())
				t.Fatal("effective startup role supplied CONNECT to NOINHERIT login")
			}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
				t.Fatal("authenticated CONNECT denial", err)
			}
			continue
		}
		if err != nil {
			t.Fatal("authenticated CONNECT denied by effective reset role", err)
		}
		if s.authorization.state.CurrentUser != reset || s.countedAuthenticated != login {
			t.Fatal("startup role or physical identity changed", s.authorization.state)
		}
		s.RequestEnd(CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
