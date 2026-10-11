package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/parser"
	"testing"
)

type roleSwitchDefaultProvider struct {
	value string
	err   error
	hook  func(context.Context, SettingDefinition)
}

func (p roleSwitchDefaultProvider) SessionDefault(ctx context.Context, d SettingDefinition) (string, bool, error) {
	if d.Name != "role" {
		return "", false, nil
	}
	if p.hook != nil {
		p.hook(ctx, d)
	}
	return p.value, true, p.err
}
func roleSwitchUnitSession(t *testing.T) (*Engine, *Session) {
	t.Helper()
	e := New()
	for _, q := range []string{"CREATE USER switch_login NOINHERIT", "CREATE ROLE switch_a", "CREATE ROLE switch_b", "CREATE ROLE switch_c", "GRANT switch_a,switch_b,switch_c TO switch_login WITH INHERIT FALSE, SET TRUE"} {
		run(t, e, q)
	}
	login, _ := e.Roles().Lookup("switch_login")
	s, err := e.StartSession(context.Background(), SessionStart{ID: SessionID(testSessionID.Add(1)), Authenticated: login.Ref})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return e, s
}
func roleSwitchAssert(t *testing.T, s *Session, before RoleState, name string) {
	t.Helper()
	role, ok := s.Engine().Roles().Lookup(name)
	if !ok {
		t.Fatal(name)
	}
	s.mu.Lock()
	after := s.authorization.state
	s.mu.Unlock()
	before.CurrentUser = role.Ref
	if after != before {
		t.Fatalf("role state %+v want %+v", after, before)
	}
}
func roleSwitchUnitExec(t *testing.T, s *Session, q string) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		return err
	}
	defer op.Done()
	_, err = s.Engine().Execute(op, statement(t, q), 0, nil)
	return err
}

func TestRoleSwitchJournalTransitions(t *testing.T) {
	for _, tc := range []struct {
		commands       []string
		inside, commit string
	}{
		{[]string{"SET ROLE switch_a"}, "switch_a", "switch_a"},
		{[]string{"SET LOCAL ROLE switch_a"}, "switch_a", "switch_login"},
		{[]string{"SET ROLE switch_a", "SET LOCAL ROLE switch_b", "SET LOCAL ROLE switch_c"}, "switch_c", "switch_a"},
		{[]string{"SET LOCAL ROLE switch_a", "SET ROLE switch_b"}, "switch_b", "switch_b"},
		{[]string{"SET ROLE switch_a", "SET LOCAL ROLE switch_b", "SET ROLE switch_c"}, "switch_c", "switch_c"},
		{[]string{"SET ROLE switch_a", "SET LOCAL ROLE TO DEFAULT"}, "switch_login", "switch_a"},
	} {
		for _, commit := range []bool{false, true} {
			t.Run(strings.Join(tc.commands, ";")+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				e, s := roleSwitchUnitSession(t)
				before := s.authorization.state
				graph, catalog := e.roles, e.state
				sessionSQL(t, s, "BEGIN")
				for _, q := range tc.commands {
					sessionSQL(t, s, q)
				}
				roleSwitchAssert(t, s, before, tc.inside)
				revision := s.authorization.revision
				end := "ROLLBACK"
				want := "switch_login"
				if commit {
					end = "COMMIT"
					want = tc.commit
				}
				sessionSQL(t, s, end)
				roleSwitchAssert(t, s, before, want)
				if s.settings.roleAction.present || e.roles != graph || e.state != catalog {
					t.Fatal("journal/catalog changed")
				}
				if want != tc.inside && s.authorization.revision != revision+1 {
					t.Fatal("restoration stamp", revision, s.authorization.revision)
				}
			})
		}
	}
}

func TestRoleSwitchSavepointAndFailureRestoration(t *testing.T) {
	_, s := roleSwitchUnitSession(t)
	before := s.authorization.state
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "SET ROLE switch_a")
	sessionSQL(t, s, "SET LOCAL ROLE switch_b")
	sessionSQL(t, s, "SAVEPOINT x")
	retained := s.settings.roleAction
	sessionSQL(t, s, "SET ROLE switch_c")
	revision := s.authorization.revision
	state(t, roleSwitchUnitExec(t, s, "SET ROLE absent"), "22023")
	roleSwitchAssert(t, s, before, "switch_b")
	if s.authorization.revision != revision+1 || s.settings.roleAction != retained {
		t.Fatal("failure checkpoint journal/stamp")
	}
	state(t, roleSwitchUnitExec(t, s, "SET ROLE switch_a"), "25P02")
	sessionSQL(t, s, "ROLLBACK TO x")
	roleSwitchAssert(t, s, before, "switch_b")
	sessionSQL(t, s, "SET ROLE switch_c")
	sessionSQL(t, s, "ROLLBACK TO x")
	roleSwitchAssert(t, s, before, "switch_b")
	sessionSQL(t, s, "RELEASE x")
	sessionSQL(t, s, "COMMIT")
	roleSwitchAssert(t, s, before, "switch_a")
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "SET ROLE switch_c")
	state(t, roleSwitchUnitExec(t, s, "SET ROLE absent"), "22023")
	roleSwitchAssert(t, s, before, "switch_a")
	sessionSQL(t, s, "ROLLBACK")
}

func TestRoleSwitchInitializationAndCachedReset(t *testing.T) {
	e, _ := roleSwitchUnitSession(t)
	login, _ := e.Roles().Lookup("switch_login")
	count := 0
	provider := roleSwitchDefaultProvider{value: "switch_a", hook: func(ctx context.Context, d SettingDefinition) {
		count++
		if d.Type != SettingString || d.BootDefault != "none" {
			t.Fatal(d)
		}
		if !e.mu.TryLock() {
			t.Fatal("provider under Engine lock")
		}
		e.mu.Unlock()
	}}
	s, err := e.StartSession(context.Background(), SessionStart{ID: SessionID(testSessionID.Add(1)), Authenticated: login.Ref, Defaults: provider})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		s.RequestEnd(CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if count != 1 {
		t.Fatal("role provider calls", count)
	}
	before := s.authorization.state
	roleSwitchAssert(t, s, before, "switch_a")
	if before.ResetRole != before.CurrentUser {
		t.Fatal(before)
	}
	sessionSQL(t, s, "SET ROLE switch_b")
	sessionSQL(t, s, "SET ROLE NONE")
	roleSwitchAssert(t, s, before, "switch_login")
	run(t, e, "GRANT switch_a TO switch_login WITH SET FALSE")
	sessionSQL(t, s, "RESET ROLE")
	roleSwitchAssert(t, s, before, "switch_a")
	state(t, roleSwitchUnitExec(t, s, "SET ROLE switch_a"), "42501")
	sessionSQL(t, s, "SET ROLE TO DEFAULT")
	roleSwitchAssert(t, s, before, "switch_a")
}

func TestRoleSwitchInitializationFailures(t *testing.T) {
	for _, tc := range []struct {
		name, value, code string
		providerErr       error
		cancel            bool
	}{
		{name: "missing", value: "absent", code: "22023"},
		{name: "denied", value: "postgres", code: "42501"},
		{name: "provider", value: "switch_a", providerErr: errors.New("role default failed")},
		{name: "cancel", value: "switch_a", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := roleSwitchUnitSession(t)
			login, _ := e.Roles().Lookup("switch_login")
			before := len(e.sessions)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := roleSwitchDefaultProvider{value: tc.value, err: tc.providerErr, hook: func(context.Context, SettingDefinition) {
				if tc.cancel {
					cancel()
				}
			}}
			s, err := e.StartSession(ctx, SessionStart{ID: SessionID(testSessionID.Add(1)), Authenticated: login.Ref, Defaults: provider})
			if s != nil || err == nil || len(e.sessions) != before {
				t.Fatal("failed startup published", s, err)
			}
			if tc.code != "" {
				state(t, err, tc.code)
			}
			if tc.providerErr != nil && !errors.Is(err, tc.providerErr) {
				t.Fatal(err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	for _, value := range []string{"none"} {
		e, _ := roleSwitchUnitSession(t)
		login, _ := e.Roles().Lookup("switch_login")
		s, err := e.StartSession(context.Background(), SessionStart{ID: SessionID(testSessionID.Add(1)), Authenticated: login.Ref, Defaults: roleSwitchDefaultProvider{value: value}})
		if err != nil {
			t.Fatal(err)
		}
		if s.authorization.state.CurrentUser != login.Ref || s.authorization.state.ResetRole != (PrincipalRef{}) {
			t.Fatal(s.authorization.state)
		}
		s.RequestEnd(CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRoleSwitchRevisionHeadroom(t *testing.T) {
	t.Run("mutation preflight", func(t *testing.T) {
		e, s := roleSwitchUnitSession(t)
		before := s.authorization.state
		s.authorization.revision = ^uint64(0) - 2
		err := roleSwitchUnitExec(t, s, "SET LOCAL ROLE switch_a")
		if err == nil || !strings.Contains(err.Error(), "revision exhausted") || s.authorization.state != before || s.settings.roleAction.present || s.authorization.revision != ^uint64(0)-2 {
			t.Fatal(err, s.authorization)
		}
		_ = e
	})
	t.Run("nested failure unwind and outer rollback", func(t *testing.T) {
		_, s := roleSwitchUnitSession(t)
		before := s.authorization.state
		s.authorization.revision = ^uint64(0) - 10
		sessionSQL(t, s, "BEGIN")
		sessionSQL(t, s, "SET ROLE switch_a")
		sessionSQL(t, s, "SAVEPOINT x")
		sessionSQL(t, s, "SET LOCAL ROLE switch_b")
		sessionSQL(t, s, "SAVEPOINT y")
		sessionSQL(t, s, "SET ROLE switch_c")
		state(t, roleSwitchUnitExec(t, s, "SET ROLE absent"), "22023")
		roleSwitchAssert(t, s, before, "switch_b")
		sessionSQL(t, s, "ROLLBACK TO x")
		roleSwitchAssert(t, s, before, "switch_a")
		sessionSQL(t, s, "ROLLBACK")
		roleSwitchAssert(t, s, before, "switch_login")
		if s.authorization.revision == 0 {
			t.Fatal("revision wrapped")
		}
	})
	t.Run("rollback-to preflight preserves unwind reserve", func(t *testing.T) {
		_, s := roleSwitchUnitSession(t)
		before := s.authorization.state
		s.authorization.revision = ^uint64(0) - 5
		sessionSQL(t, s, "BEGIN")
		sessionSQL(t, s, "SET ROLE switch_a")
		sessionSQL(t, s, "SAVEPOINT x")
		sessionSQL(t, s, "SET LOCAL ROLE switch_b")
		err := roleSwitchUnitExec(t, s, "ROLLBACK TO x")
		if err == nil || !strings.Contains(err.Error(), "revision exhausted") {
			t.Fatal(err)
		}
		sessionSQL(t, s, "ROLLBACK")
		roleSwitchAssert(t, s, before, "switch_login")
		if s.authorization.revision == 0 {
			t.Fatal("revision wrapped")
		}
	})
}

func TestRoleSwitchCanceledJournalPublication(t *testing.T) {
	e, s := roleSwitchUnitSession(t)
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "SET ROLE switch_a")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	before, action, revision := op.RoleState(), s.settings.roleAction, s.authorization.revision
	ctx, cancel := context.WithCancel(op.Context())
	defer cancel()
	boundary := &identityRolePublicationContext{Context: ctx, session: s, cancel: cancel}
	op.ctx = boundary
	_, err = e.executeRole(op, &parser.RoleSelection{Name: "switch_b", Local: true}, 0, false)
	if !boundary.observed || !errors.Is(err, context.Canceled) || op.RoleState() != before || s.settings.roleAction != action || s.authorization.revision != revision {
		t.Fatal("canceled publication", err)
	}
	op.Done()
	sessionSQL(t, s, "COMMIT")
	if !reflect.DeepEqual(s.authorization.state, before) {
		t.Fatal("canceled LOCAL damaged SESSION candidate")
	}
}

func TestRoleSwitchPrincipalValidation(t *testing.T) {
	e := New()
	run(t, e, "CREATE ROLE switch_stale")
	stale, _ := e.Roles().Lookup("switch_stale")
	admin, _ := e.Roles().Lookup("postgres")
	other := New()
	foreign, _ := other.Roles().Lookup("postgres")
	run(t, e, "DROP ROLE switch_stale")
	for _, target := range []PrincipalRef{{}, foreign.Ref, stale.Ref} {
		if _, err := e.Roles().canSetRole(admin.Ref, target); err == nil {
			t.Fatal("superuser adopted invalid target", target)
		}
		if _, err := e.Roles().canSetRole(target, target); err == nil {
			t.Fatal("self bypass adopted invalid target", target)
		}
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, target := range []PrincipalRef{foreign.Ref, stale.Ref} {
		op.session.mu.Lock()
		op.session.authorization.state.ResetRole = target
		before := op.session.authorization.state
		revision := op.session.authorization.revision
		op.session.mu.Unlock()
		if _, err := e.Execute(op, statement(t, "RESET ROLE"), 0, nil); err == nil {
			t.Fatal("invalid cached reset", target)
		}
		if op.RoleState() != before || op.session.authorization.revision != revision {
			t.Fatal("invalid reset published")
		}
	}
	op.session.mu.Lock()
	op.session.authorization.state.ResetRole = PrincipalRef{}
	op.session.mu.Unlock()
}

func TestRoleSwitchFinishConsumers(t *testing.T) {
	e, s := roleSwitchUnitSession(t)
	before := s.authorization.state
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "SET ROLE switch_a")
	sessionSQL(t, s, "SET LOCAL ROLE switch_b")
	sessionSQL(t, s, "PREPARE TRANSACTION 'role_switch_unit'")
	roleSwitchAssert(t, s, before, "switch_a")
	if s.settings.roleAction.present {
		t.Fatal("PREPARE retained role journal")
	}
	run(t, e, "COMMIT PREPARED 'role_switch_unit'")
	roleSwitchAssert(t, s, before, "switch_a")
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "SET LOCAL ROLE switch_b")
	s.RequestEnd(CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.settings.roleAction.present {
		t.Fatal("physical teardown retained role journal")
	}
	// A separate physical session did not acquire the ended owner's identity.
	peer := sessionStart(t, e, nil)
	admin, _ := e.Roles().Lookup("postgres")
	if peer.authorization.state.CurrentUser != admin.Ref {
		t.Fatal(peer.authorization.state)
	}
}
