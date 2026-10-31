package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/enginetest"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

type databaseAdmissionDefaults struct {
	apply func()
}

func (p *databaseAdmissionDefaults) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	if d.Name == "application_name" && p.apply != nil {
		apply := p.apply
		p.apply = nil
		apply()
	}
	return "", false, nil
}

func databaseAdmissionFixture() (*core.Engine, authorization.PrincipalRef) {
	e, _, _, _, reader := ownershipUnitRoles()
	roles := enginetest.CloneRoleFixture(e.RoleStore.Published)
	login := enginetest.FixtureRole(roles, reader)
	login.Login = true
	login.ConnectionLimit = 1
	enginetest.PutFixtureRole(roles, login)
	e.RoleStore.PublishLocked(roles)
	return e, reader
}

func databaseAdmissionPolicyLocked(e *core.Engine, acl authorization.ACLValue) {
	candidate := catalog.CloneState(e.Catalog.State)
	db := *candidate.MustDatabase(catalog.CurrentDatabaseAddress)
	db.ObjectRevision++
	db.ACL = acl
	candidate.SetDescriptor(catalog.CurrentDatabaseAddress, &db)
	e.Catalog.PublishLocked(candidate)
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
				e.Mu.Lock()
				databaseAdmissionPolicyLocked(e, authorization.ACLValue{Explicit: true})
				e.Mu.Unlock()
				if phase == "cancel" {
					cancel()
				}
			}}
			id := enginetest.NextSessionID()
			s, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: id, Defaults: provider}, login)
			if phase == "cancel" {
				if s != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("canceled initialization published", s, err)
				}
			} else {
				if err != nil || s == nil {
					t.Fatal("retained admission was revalidated after defaults", s, err)
				}
				fresh, denial := enginetest.OpenSession(ctx, e, session.SessionStart{ID: id + 1}, login)
				want := &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "42501", Message: `permission denied for database "` + e.DatabaseName + `"`, Detail: "User does not have CONNECT privilege."}
				var diagnostic *sqlstate.Error
				if fresh != nil || !errors.As(denial, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
					t.Fatal("subsequent admission ignored revoked CONNECT", fresh, denial)
				}
				s.RequestEnd(session.CleanClose)
				if err := s.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if len(core.EngineSessions(e)) != 0 || core.EngineRoleSessionCountLocked(e, login) != 0 {
				t.Fatal("failed initialization consumed registry/count resources")
			}
			e.Mu.Lock()
			databaseAdmissionPolicyLocked(e, authorization.ACLValue{})
			e.Mu.Unlock()
			// Reusing the exact failed identity exercises advisory-ticket cleanup,
			// not merely absence from the public session registry.
			live, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: id}, login)
			if err != nil {
				t.Fatal("failed admission retained lifecycle resources", err)
			}
			live.RequestEnd(session.CleanClose)
			if err := live.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(core.EngineSessions(e)) != 0 || core.EngineRoleSessionCountLocked(e, login) != 0 {
				t.Fatal("physical close leaked authenticated count")
			}
		})
	}
}

func TestDatabaseAdmissionRetainedSession(t *testing.T) {
	e, login := databaseAdmissionFixture()
	live, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: enginetest.NextSessionID()}, login)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		live.RequestEnd(session.CleanClose)
		if err := live.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	e.Mu.Lock()
	databaseAdmissionPolicyLocked(e, authorization.ACLValue{Explicit: true})
	e.Mu.Unlock()
	op, err := live.Acquire(context.Background())
	if err != nil {
		t.Fatal("CONNECT revoke terminated admitted session", err)
	}
	op.Done()
	if !live.IsLive() || live.CountedAuthenticated != login || core.EngineRoleSessionCountLocked(e, login) != 1 {
		t.Fatal("revocation changed physical lifetime or authenticated count")
	}
	fresh, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: enginetest.NextSessionID()}, login)
	if fresh != nil {
		fresh.RequestEnd(session.CleanClose)
		_ = fresh.WaitEnd(context.Background())
		t.Fatal("fresh session admitted after revoke")
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Detail != "User does not have CONNECT privilege." {
		t.Fatal("CONNECT denial must precede exhausted role connection count", err)
	}
	if core.EngineRoleSessionCountLocked(e, login) != 1 {
		t.Fatal("denied session consumed connection count")
	}
}

func TestDatabaseAdmissionAuthenticatedNotResetRole(t *testing.T) {
	for _, allowLogin := range []bool{false, true} {
		e, owner, reset, _, login := ownershipUnitRoles()
		roles := enginetest.CloneRoleFixture(e.RoleStore.Published)
		actor := enginetest.FixtureRole(roles, login)
		actor.Login = true
		actor.ConnectionLimit = -1
		enginetest.PutFixtureRole(roles, actor)
		enginetest.SetFixtureEdges(roles, []authorization.Membership{{Role: reset, Member: login, Grantor: owner, Set: true}})
		e.RoleStore.PublishLocked(roles)
		grantee := reset
		if allowLogin {
			grantee = login
		}
		databaseAdmissionPolicyLocked(e, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: grantee}, Privileges: authorization.ConnectPrivilege}}})
		start := session.SessionStart{ID: enginetest.NextSessionID(), Defaults: roleSwitchDefaultProvider{value: enginetest.FixtureRole(roles, reset).Name}}
		s, err := enginetest.OpenSession(context.Background(), e, start, login)
		if !allowLogin {
			if s != nil {
				s.RequestEnd(session.CleanClose)
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
		if s.Authorization.State.CurrentUser != reset || s.CountedAuthenticated != login {
			t.Fatal("startup role or physical identity changed", s.Authorization.State)
		}
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func ownershipUnitRoles() (e *core.Engine, owner, source, target, reader authorization.PrincipalRef) {
	e = engine.New()
	owner = e.RoleStore.Domain.Principal(10)
	refs := []*authorization.PrincipalRef{&source, &target, &reader}
	names := []string{"ownership_source", "ownership_target", "ownership_reader"}
	publication := enginetest.CloneRoleFixture(e.RoleStore.Published)
	for i, destination := range refs {
		e.RoleStore.InstallPrincipalFrontierLocked(uint64(100001 + i))
		ref, err := e.RoleStore.PrincipalCandidateLocked()
		if err != nil {
			panic(err)
		}
		*destination = ref
		e.RoleStore.InstallPrincipalFrontierLocked(ref.ID() + 1)
		enginetest.PutFixtureRole(publication, authorization.RoleDescriptor{Ref: ref, Name: names[i], Inherit: true})
	}
	e.RoleStore.PublishLocked(publication)
	return
}

type roleSwitchDefaultProvider struct {
	value string
	err   error
	hook  func(context.Context, settings.Definition)
}

func (p roleSwitchDefaultProvider) SessionDefault(ctx context.Context, d settings.Definition) (string, bool, error) {
	if d.Name != "role" {
		return "", false, nil
	}
	if p.hook != nil {
		p.hook(ctx, d)
	}
	return p.value, true, p.err
}
