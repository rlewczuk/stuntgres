package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/enginetest"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
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

func TestDatabaseAdmissionPolicyTransitions(t *testing.T) {
	for _, phase := range []string{"allow-connections", "connection-limit"} {
		t.Run(phase, func(t *testing.T) {
			e, login := databaseAdmissionFixture()
			publish := func(allow bool, limit int32) {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				next := catalog.CloneState(e.Catalog.State)
				database := *next.MustDatabase(e.Catalog.DatabaseAddress())
				database.AllowConnections, database.ConnectionLimit = allow, limit
				database.ObjectRevision++
				next.SetDescriptor(database.Address(), &database)
				e.Catalog.PublishLocked(next)
			}
			publish(true, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			id := enginetest.NextSessionID()
			admission, err := e.AdmitDatabase(ctx, login, id, core.DatabaseSelector{})
			if err != nil {
				t.Fatal(err)
			}
			defer admission.Release()
			entered, release := make(chan struct{}), make(chan struct{})
			provider := &databaseAlterDefaults{visit: func() error {
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			type outcome struct {
				session *session.Session
				err     error
			}
			result := make(chan outcome, 1)
			joined := false
			go func() {
				s, err := e.StartSession(ctx, admission, session.SessionStart{ID: id, Defaults: provider})
				result <- outcome{s, err}
			}()
			defer func() {
				cancel()
				if !joined {
					got := <-result
					if got.session != nil {
						databaseAlterEnd(t, got.session)
					}
				}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			assertOccupancy := func(count int) {
				t.Helper()
				if got := core.EngineDatabaseAdmissionCount(e); got != count {
					t.Fatalf("database occupancy: got %d want %d", got, count)
				}
				if got := core.EngineRoleSessionCountLocked(e, login); got != count {
					t.Fatalf("principal occupancy: got %d want %d", got, count)
				}
			}
			assertOccupancy(1)
			core.EngineSessionMuRef(e).Lock()
			published := len(core.EngineSessions(e))
			core.EngineSessionMuRef(e).Unlock()
			if published != 0 {
				t.Fatal("public initializer published before defaults finished")
			}
			want := &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL"}
			if phase == "allow-connections" {
				publish(false, 1)
				want.Code, want.Message = "55000", `database "`+e.DatabaseName+`" is not currently accepting connections`
			} else {
				publish(true, 0)
				want.Code, want.Message = "53300", `too many connections for database "`+e.DatabaseName+`"`
			}
			// Submitted ownership cannot be abandoned by the original caller.
			admission.Release()
			admission.Release()
			assertOccupancy(1)
			close(release)
			got := <-result
			joined = true
			if got.session != nil {
				defer databaseAlterEnd(t, got.session)
			}
			if got.err != nil || got.session == nil || !got.session.IsLive() {
				t.Fatal("committed database policy retrospectively rejected admission", got.session, got.err)
			}
			assertOccupancy(1)
			deniedID := enginetest.NextSessionID()
			touched := false
			fresh, denial := enginetest.OpenSession(ctx, e, session.SessionStart{ID: deniedID, Defaults: &databaseAlterDefaults{visit: func() error {
				touched = true
				return nil
			}}}, login)
			if fresh != nil {
				databaseAlterEnd(t, fresh)
				t.Fatal("next physical initializer ignored committed policy")
			}
			var diagnostic *sqlstate.Error
			if touched || !errors.As(denial, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("next admission diagnostic/default boundary", touched, denial)
			}
			assertOccupancy(1)
			databaseAlterEnd(t, got.session)
			databaseAlterEnd(t, got.session)
			admission.Release()
			assertOccupancy(0)
			core.EngineSessionMuRef(e).Lock()
			remaining := len(core.EngineSessions(e))
			core.EngineSessionMuRef(e).Unlock()
			if remaining != 0 {
				t.Fatal("physical close retained a public lifecycle owner")
			}
			publish(true, 1)
			// Reuse both identities: terminal release and rejection must each
			// retire their genuine startup ticket, not only decrement counts.
			for _, reuseID := range []ids.SessionID{id, deniedID} {
				live, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: reuseID}, login)
				if err != nil {
					t.Fatal("policy transition leaked ticket or occupied slot", err)
				}
				assertOccupancy(1)
				databaseAlterEnd(t, live)
				assertOccupancy(0)
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

func TestDatabaseAdmissionExactSelectors(t *testing.T) {
	for _, name := range []string{"", " ", "postgres://opaque/database?name=other"} {
		t.Run(name, func(t *testing.T) {
			e := engine.NewWithSettings(nil, name, false)
			principal, err := e.ResolveLogin("postgres")
			if err != nil {
				t.Fatal(err)
			}
			if e.DatabaseName != name || e.Cluster.DefaultTarget != e || e.Cluster.Targets[name] != e {
				t.Fatal("constructor changed the exact original attachment")
			}
			before := e.Catalog.State
			id := enginetest.NextSessionID()
			for _, missing := range []string{"", name + " "} {
				want := sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "3D000", Message: `database "` + missing + `" does not exist`}
				admission, err := e.AdmitDatabase(t.Context(), principal, id, core.DatabaseSelector{Name: missing, Explicit: true})
				if admission != nil {
					admission.Release()
					t.Fatal("explicit missing name reserved the original attachment", missing)
				}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatalf("explicit admission %q: %#v, want %#v", missing, err, want)
				}
				target, err := e.SelectDatabase(t.Context(), missing)
				diagnostic = nil
				if target != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatalf("explicit lookup %q: target=%p error=%#v, want %#v", missing, target, err, want)
				}
			}
			if e.Catalog.State != before || e.DatabaseName != name || e.Cluster.DefaultTarget != e ||
				len(e.Cluster.Targets) != 1 || e.Cluster.Targets[name] != e ||
				len(core.EngineSessions(e)) != 0 || core.EngineDatabaseAdmissionCount(e) != 0 {
				t.Fatal("rejected explicit selection changed the original attachment or occupancy")
			}
			if name != "" {
				target, err := e.SelectDatabase(t.Context(), name)
				if err != nil || target != e {
					t.Fatal("exact nonempty name was not selectable", target, err)
				}
				admission, err := e.AdmitDatabase(t.Context(), principal, id, core.DatabaseSelector{Name: name, Explicit: true})
				if err != nil || admission == nil {
					t.Fatal("exact nonempty name was not admitted", err)
				}
				if admission.Target() != e {
					admission.Release()
					t.Fatal("exact name retargeted the attachment")
				}
				admission.Release()
			}
			// Reuse the rejected identity: an explicit-empty failure must not
			// retain a ticket, and default selection must still open the same
			// empty/whitespace/URL-named original attachment, not a renamed one.
			admission, err := e.AdmitDatabase(t.Context(), principal, id, core.DatabaseSelector{})
			if err != nil || admission == nil {
				t.Fatal("default attachment became inaccessible", err)
			}
			defer admission.Release()
			if admission.Target() != e {
				t.Fatal("default selector redirected the original attachment")
			}
			func() {
				live, err := e.StartSession(t.Context(), admission, session.SessionStart{ID: id})
				if err != nil || live == nil {
					t.Fatal("default admission failed physical startup", err)
				}
				defer databaseAlterEnd(t, live)
				if core.EngineOf(live) != e || core.EngineDatabaseAdmissionCount(e) != 1 {
					t.Fatal("default startup lost its target or physical reservation")
				}
			}()
			if len(core.EngineSessions(e)) != 0 || core.EngineDatabaseAdmissionCount(e) != 0 {
				t.Fatal("default physical close leaked publication or occupancy")
			}
		})
	}
}
