package rolecmds_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

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
func roleSwitchUnitSession(t *testing.T) (*core.Engine, *session.Session) {
	t.Helper()
	e := engine.New()
	for _, q := range []string{"CREATE USER switch_login NOINHERIT", "CREATE ROLE switch_a", "CREATE ROLE switch_b", "CREATE ROLE switch_c", "GRANT switch_a,switch_b,switch_c TO switch_login WITH INHERIT FALSE, SET TRUE"} {
		enginetest.Run(t, e, q)
	}
	login, _ := e.Roles().Lookup("switch_login")
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login.Ref})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return e, s
}
func roleSwitchAssert(t *testing.T, s *session.Session, before authorization.RoleState, name, text string) {
	t.Helper()
	role, ok := core.EngineOf(s).Roles().Lookup(name)
	if !ok {
		t.Fatal(name)
	}
	s.Mu.Lock()
	after := s.Authorization.State
	s.Mu.Unlock()
	before.CurrentUser = role.Ref
	before.ActiveRole, before.RoleText = text != "", text
	if after != before {
		t.Fatalf("role state %+v want %+v", after, before)
	}
}
func roleSwitchUnitExec(t *testing.T, s *session.Session, q string) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		return err
	}
	defer op.Done()
	_, err = core.EngineOf(s).Execute(op, enginetest.Statement(t, q), 0, nil)
	return err
}

func TestRoleSwitchJournalTransitions(t *testing.T) {
	for _, tc := range []struct {
		commands       []string
		inside, commit string // Empty text means inactive; explicit self is nonempty.
	}{
		{[]string{"SET ROLE switch_a"}, "switch_a", "switch_a"},
		{[]string{"SET LOCAL ROLE switch_a"}, "switch_a", ""},
		{[]string{"SET ROLE switch_a", "SET LOCAL ROLE switch_b", "SET LOCAL ROLE switch_c"}, "switch_c", "switch_a"},
		{[]string{"SET LOCAL ROLE switch_a", "SET ROLE switch_b"}, "switch_b", "switch_b"},
		{[]string{"SET ROLE switch_a", "SET LOCAL ROLE switch_b", "SET ROLE switch_c"}, "switch_c", "switch_c"},
		{[]string{"SET ROLE switch_a", "SET LOCAL ROLE TO DEFAULT"}, "", "switch_a"},
		{[]string{"SET ROLE switch_login", "SET LOCAL ROLE NONE"}, "", "switch_login"},
		{[]string{"SET ROLE NONE", "SET LOCAL ROLE switch_login"}, "switch_login", ""},
		{[]string{"SET LOCAL ROLE switch_login"}, "switch_login", ""},
	} {
		for _, commit := range []bool{false, true} {
			t.Run(strings.Join(tc.commands, ";")+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				e, s := roleSwitchUnitSession(t)
				before := s.Authorization.State
				graph, catalog := e.RoleStore.Published, e.Catalog.State
				enginetest.SessionSQL(t, s, "BEGIN")
				for _, q := range tc.commands {
					enginetest.SessionSQL(t, s, q)
				}
				insideName := tc.inside
				if insideName == "" {
					insideName = "switch_login"
				}
				roleSwitchAssert(t, s, before, insideName, tc.inside)
				revision := s.Authorization.Revision
				end := "ROLLBACK"
				want := ""
				if commit {
					end = "COMMIT"
					want = tc.commit
				}
				enginetest.SessionSQL(t, s, end)
				wantName := want
				if wantName == "" {
					wantName = "switch_login"
				}
				roleSwitchAssert(t, s, before, wantName, want)
				if s.Settings.RoleAction.Present || e.RoleStore.Published != graph || e.Catalog.State != catalog {
					t.Fatal("journal/catalog changed")
				}
				if want != tc.inside && s.Authorization.Revision != revision+1 {
					t.Fatal("restoration stamp", revision, s.Authorization.Revision)
				}
			})
		}
	}
}

func TestRoleSwitchSavepointAndFailureRestoration(t *testing.T) {
	_, s := roleSwitchUnitSession(t)
	before := s.Authorization.State
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "SET ROLE switch_a")
	enginetest.SessionSQL(t, s, "SET LOCAL ROLE switch_b")
	enginetest.SessionSQL(t, s, "SAVEPOINT x")
	retained := s.Settings.RoleAction
	enginetest.SessionSQL(t, s, "SET ROLE switch_c")
	revision := s.Authorization.Revision
	enginetest.State(t, roleSwitchUnitExec(t, s, "SET ROLE absent"), "22023")
	roleSwitchAssert(t, s, before, "switch_b", "switch_b")
	if s.Authorization.Revision != revision+1 || s.Settings.RoleAction != retained {
		t.Fatal("failure checkpoint journal/stamp")
	}
	enginetest.State(t, roleSwitchUnitExec(t, s, "SET ROLE switch_a"), "25P02")
	enginetest.SessionSQL(t, s, "ROLLBACK TO x")
	roleSwitchAssert(t, s, before, "switch_b", "switch_b")
	enginetest.SessionSQL(t, s, "SET ROLE switch_c")
	enginetest.SessionSQL(t, s, "ROLLBACK TO x")
	roleSwitchAssert(t, s, before, "switch_b", "switch_b")
	enginetest.SessionSQL(t, s, "RELEASE x")
	enginetest.SessionSQL(t, s, "COMMIT")
	roleSwitchAssert(t, s, before, "switch_a", "switch_a")
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "SET ROLE switch_c")
	enginetest.State(t, roleSwitchUnitExec(t, s, "SET ROLE absent"), "22023")
	roleSwitchAssert(t, s, before, "switch_a", "switch_a")
	enginetest.SessionSQL(t, s, "ROLLBACK")
	// Reusable marks must distinguish self from NONE even though their refs
	// coincide, and retain the masked SET candidate through both restorations.
	roleTransactionalAST(t, core.EngineOf(s), s, &parser.RoleSelection{Reset: true})
	enginetest.SessionSQL(t, s, "BEGIN")
	roleTransactionalAST(t, core.EngineOf(s), s, &parser.RoleSelection{Name: "switch_login"})
	roleTransactionalAST(t, core.EngineOf(s), s, &parser.RoleSelection{Name: "none", Local: true})
	markName := parser.Identifier{Text: "self_mark"}
	roleTransactionalAST(t, core.EngineOf(s), s, &parser.SavepointControl{Kind: parser.CreateSavepoint, Name: markName})
	action := s.Settings.RoleAction
	for range 2 {
		roleTransactionalAST(t, core.EngineOf(s), s, &parser.RoleSelection{Name: "switch_login"})
		revision := s.Authorization.Revision
		roleTransactionalAST(t, core.EngineOf(s), s, &parser.SavepointControl{Kind: parser.RollbackSavepoint, Name: markName})
		roleSwitchAssert(t, s, before, "switch_login", "")
		if s.Settings.RoleAction != action || s.Authorization.Revision != revision+1 {
			t.Fatal("active-only mark lost role action or monotonic revision")
		}
	}
	enginetest.SessionSQL(t, s, "COMMIT")
	roleSwitchAssert(t, s, before, "switch_login", "switch_login")
}

func TestRoleSwitchInitializationAndCachedReset(t *testing.T) {
	e, _ := roleSwitchUnitSession(t)
	login, _ := e.Roles().Lookup("switch_login")
	count := 0
	provider := roleSwitchDefaultProvider{value: "switch_a", hook: func(ctx context.Context, d settings.Definition) {
		count++
		if d.Type != settings.String || d.BootDefault != "none" {
			t.Fatal(d)
		}
		if !e.Mu.TryLock() {
			t.Fatal("provider under Engine lock")
		}
		e.Mu.Unlock()
	}}
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login.Ref, Defaults: provider})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if count != 1 {
		t.Fatal("role provider calls", count)
	}
	before := s.Authorization.State
	roleSwitchAssert(t, s, before, "switch_a", "switch_a")
	if before.ResetRole != before.CurrentUser || before.ResetRoleText != "switch_a" {
		t.Fatal(before)
	}
	enginetest.SessionSQL(t, s, "SET ROLE switch_b")
	enginetest.SessionSQL(t, s, "SET ROLE NONE")
	roleSwitchAssert(t, s, before, "switch_login", "")
	enginetest.Run(t, e, "GRANT switch_a TO switch_login WITH SET FALSE")
	enginetest.SessionSQL(t, s, "RESET ROLE")
	roleSwitchAssert(t, s, before, "switch_a", "switch_a")
	enginetest.State(t, roleSwitchUnitExec(t, s, "SET ROLE switch_a"), "42501")
	enginetest.SessionSQL(t, s, "SET ROLE TO DEFAULT")
	roleSwitchAssert(t, s, before, "switch_a", "switch_a")
	e.Mu.Lock()
	descriptor, err := e.RoleStore.Published.Root.Read(login.Ref).Descriptor(before.ResetRole)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	descriptor.Name = "switch_renamed"
	root, err := e.RoleStore.Published.Root.PutDescriptor(descriptor)
	if err == nil {
		e.RoleStore.PublishLocked(e.RoleStore.Published.WithRoot(root))
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	roleSwitchAssert(t, s, before, "switch_renamed", "switch_a")
	enginetest.SessionSQL(t, s, "SET ROLE NONE")
	enginetest.SessionSQL(t, s, "RESET ROLE")
	roleSwitchAssert(t, s, before, "switch_renamed", "switch_a")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, text, err := e.PersistedSettingCurrent(op, "role")
	op.Done()
	if err != nil || text != "switch_a" {
		t.Fatal("reset role text followed live descriptor rename", text, err)
	}
}

func TestRoleSwitchRevisionHeadroom(t *testing.T) {
	t.Run("active-only mutation preflight", func(t *testing.T) {
		e, s := roleSwitchUnitSession(t)
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		before := s.Authorization.State
		s.Authorization.Revision = ^uint64(0) - 2
		_, err = rolecmds.ExecuteRole(e, op, &parser.RoleSelection{Name: "switch_login"}, 0, true)
		if err == nil || !strings.Contains(err.Error(), "revision exhausted") || s.Authorization.State != before || s.Settings.RoleAction.Present || s.Authorization.Revision != ^uint64(0)-2 {
			t.Fatal("active-only exhausted mutation installed a prefix", err, s.Authorization)
		}
	})
	t.Run("mutation preflight", func(t *testing.T) {
		e, s := roleSwitchUnitSession(t)
		before := s.Authorization.State
		s.Authorization.Revision = ^uint64(0) - 2
		err := roleSwitchUnitExec(t, s, "SET LOCAL ROLE switch_a")
		if err == nil || !strings.Contains(err.Error(), "revision exhausted") || s.Authorization.State != before || s.Settings.RoleAction.Present || s.Authorization.Revision != ^uint64(0)-2 {
			t.Fatal(err, s.Authorization)
		}
		_ = e
	})
	t.Run("nested failure unwind and outer rollback", func(t *testing.T) {
		_, s := roleSwitchUnitSession(t)
		before := s.Authorization.State
		s.Authorization.Revision = ^uint64(0) - 10
		enginetest.SessionSQL(t, s, "BEGIN")
		enginetest.SessionSQL(t, s, "SET ROLE switch_a")
		enginetest.SessionSQL(t, s, "SAVEPOINT x")
		enginetest.SessionSQL(t, s, "SET LOCAL ROLE switch_b")
		enginetest.SessionSQL(t, s, "SAVEPOINT y")
		enginetest.SessionSQL(t, s, "SET ROLE switch_c")
		enginetest.State(t, roleSwitchUnitExec(t, s, "SET ROLE absent"), "22023")
		roleSwitchAssert(t, s, before, "switch_b", "switch_b")
		enginetest.SessionSQL(t, s, "ROLLBACK TO x")
		roleSwitchAssert(t, s, before, "switch_a", "switch_a")
		enginetest.SessionSQL(t, s, "ROLLBACK")
		roleSwitchAssert(t, s, before, "switch_login", "")
		if s.Authorization.Revision == 0 {
			t.Fatal("revision wrapped")
		}
	})
	t.Run("rollback-to preflight preserves unwind reserve", func(t *testing.T) {
		_, s := roleSwitchUnitSession(t)
		before := s.Authorization.State
		s.Authorization.Revision = ^uint64(0) - 5
		enginetest.SessionSQL(t, s, "BEGIN")
		enginetest.SessionSQL(t, s, "SET ROLE switch_a")
		enginetest.SessionSQL(t, s, "SAVEPOINT x")
		enginetest.SessionSQL(t, s, "SET LOCAL ROLE switch_b")
		err := roleSwitchUnitExec(t, s, "ROLLBACK TO x")
		if err == nil || !strings.Contains(err.Error(), "revision exhausted") {
			t.Fatal(err)
		}
		enginetest.SessionSQL(t, s, "ROLLBACK")
		roleSwitchAssert(t, s, before, "switch_login", "")
		if s.Authorization.Revision == 0 {
			t.Fatal("revision wrapped")
		}
	})
}

func TestRoleSwitchCanceledJournalPublication(t *testing.T) {
	for _, tc := range []struct{ selected, local string }{
		{"switch_a", "switch_b"},
		{"switch_login", "none"},
	} {
		t.Run(tc.selected, func(t *testing.T) {
			e, s := roleSwitchUnitSession(t)
			enginetest.SessionSQL(t, s, "BEGIN")
			roleTransactionalAST(t, e, s, &parser.RoleSelection{Name: tc.selected})
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			before, action, revision := op.RoleState(), s.Settings.RoleAction, s.Authorization.Revision
			ctx, cancel := context.WithCancel(op.Context())
			defer cancel()
			boundary := &identityRolePublicationContext{Context: ctx, session: s, cancel: cancel}
			op.Ctx = boundary
			_, err = rolecmds.ExecuteRole(e, op, &parser.RoleSelection{Name: tc.local, Local: true}, 0, false)
			if !boundary.observed || !errors.Is(err, context.Canceled) || op.RoleState() != before || s.Settings.RoleAction != action || s.Authorization.Revision != revision {
				t.Fatal("canceled publication", err)
			}
			op.Done()
			enginetest.SessionSQL(t, s, "COMMIT")
			if !reflect.DeepEqual(s.Authorization.State, before) {
				t.Fatal("canceled LOCAL damaged SESSION candidate")
			}
		})
	}
}

func TestRoleSwitchPrincipalValidation(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE ROLE switch_stale")
	stale, _ := e.Roles().Lookup("switch_stale")
	admin, _ := e.Roles().Lookup("postgres")
	other := engine.New()
	foreign, _ := other.Roles().Lookup("postgres")
	enginetest.Run(t, e, "DROP ROLE switch_stale")
	for _, target := range []authorization.PrincipalRef{{}, foreign.Ref, stale.Ref} {
		if _, err := e.Roles().CanSetRole(admin.Ref, target); err == nil {
			t.Fatal("superuser adopted invalid target", target)
		}
		if _, err := e.Roles().CanSetRole(target, target); err == nil {
			t.Fatal("self bypass adopted invalid target", target)
		}
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, target := range []authorization.PrincipalRef{foreign.Ref, stale.Ref} {
		op.Session.Mu.Lock()
		op.Session.Authorization.State.ResetRole = target
		op.Session.Authorization.State.ResetRoleText = "never-adopt"
		before := op.Session.Authorization.State
		revision := op.Session.Authorization.Revision
		op.Session.Mu.Unlock()
		if _, err := e.Execute(op, enginetest.Statement(t, "RESET ROLE"), 0, nil); err == nil {
			t.Fatal("invalid cached reset", target)
		}
		if op.RoleState() != before || op.Session.Authorization.Revision != revision {
			t.Fatal("invalid reset published")
		}
	}
	op.Session.Mu.Lock()
	op.Session.Authorization.State.ResetRole = authorization.PrincipalRef{}
	op.Session.Authorization.State.ResetRoleText = ""
	op.Session.Mu.Unlock()
}

func TestRoleSwitchFinishConsumers(t *testing.T) {
	for _, tc := range []struct{ selected, local string }{
		{"switch_a", "switch_b"},
		{"switch_login", "none"},
	} {
		t.Run(tc.selected, func(t *testing.T) {
			e, s := roleSwitchUnitSession(t)
			before := s.Authorization.State
			enginetest.SessionSQL(t, s, "BEGIN")
			roleTransactionalAST(t, e, s, &parser.RoleSelection{Name: tc.selected})
			roleTransactionalAST(t, e, s, &parser.RoleSelection{Name: tc.local, Local: true})
			enginetest.SessionSQL(t, s, "PREPARE TRANSACTION 'role_switch_unit'")
			roleSwitchAssert(t, s, before, tc.selected, tc.selected)
			if s.Settings.RoleAction.Present {
				t.Fatal("PREPARE retained role journal")
			}
			enginetest.Run(t, e, "COMMIT PREPARED 'role_switch_unit'")
			roleSwitchAssert(t, s, before, tc.selected, tc.selected)
			enginetest.SessionSQL(t, s, "BEGIN")
			roleTransactionalAST(t, e, s, &parser.RoleSelection{Name: tc.local, Local: true})
			s.RequestEnd(session.CleanClose)
			if err := s.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			if s.Settings.RoleAction.Present {
				t.Fatal("physical teardown retained role journal")
			}
			// A separate physical session did not acquire the ended owner's identity.
			peer := enginetest.StartSession(t, e, nil)
			admin, _ := e.Roles().Lookup("postgres")
			if peer.Authorization.State != (authorization.RoleState{Authenticated: admin.Ref, SessionUser: admin.Ref, CurrentUser: admin.Ref}) {
				t.Fatal(peer.Authorization.State)
			}
		})
	}
}
