package core_test

import (
	"context"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
)

func TestRuleModePlanGenerationHeadroom(t *testing.T) {
	for _, tc := range []struct {
		name      string
		setup     []string
		query     string
		remaining uint64
		detached  bool
	}{
		{"set", nil, "SET session_replication_role=replica", 1, false},
		{"reset-all", []string{"SET session_replication_role=replica"}, "RESET ALL", 1, false},
		{"discard", nil, "DISCARD PLANS", 1, false},
		{"discard-all", []string{"SET session_replication_role=replica"}, "DISCARD ALL", 1, false},
		{"savepoint", []string{"BEGIN", "SAVEPOINT settings"}, "SET session_replication_role=replica", 2, false},
		{"routine-save", []string{"CREATE FUNCTION saved_mode() RETURNS INT LANGUAGE SQL SET session_replication_role=replica AS $$SELECT 1$$"}, "SELECT saved_mode()", 2, false},
		{"detached", nil, "SET session_replication_role=replica", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range tc.setup {
				sessionSQL(t, s, q)
			}
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			if tc.detached {
				op.Restricted = &session.RestrictedInvocation{Owner: s.Authorization.State.CurrentUser}
			}
			store := op.EffectiveSettings()
			s.DiscardPlansGeneration = ^uint64(0) - tc.remaining
			beforeGeneration := s.DiscardPlansGeneration
			values, typed, actions := maps.Clone(store.Values), maps.Clone(store.Typed), maps.Clone(store.Actions)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Execute(enginetest.Statement(t, tc.query), 0, nil)
			if err == nil {
				scope.Finish(nil)
				t.Fatal("exhausted transition was accepted")
			}
			if s.DiscardPlansGeneration != beforeGeneration || !reflect.DeepEqual(values, store.Values) || !reflect.DeepEqual(typed, store.Typed) || !reflect.DeepEqual(actions, store.Actions) {
				scope.Finish(err)
				t.Fatal("failed preflight changed values/actions/generation")
			}
			scope.Finish(err)
		})
	}
	t.Run("restoration-and-intervening-discard", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		sessionSQL(t, s, "BEGIN")
		sessionSQL(t, s, "SAVEPOINT settings")
		s.DiscardPlansGeneration = ^uint64(0) - 8
		sessionSQL(t, s, "SET LOCAL session_replication_role=replica")
		first := s.DiscardPlansGeneration
		sessionSQL(t, s, "DISCARD PLANS")
		if s.DiscardPlansGeneration != first+1 {
			t.Fatal("DISCARD did not consume the shared generation")
		}
		sessionSQL(t, s, "ROLLBACK TO settings")
		if s.DiscardPlansGeneration != first+2 {
			t.Fatal("savepoint did not invalidate restored effective role")
		}
		sessionSQL(t, s, "COMMIT")
		if s.DiscardPlansGeneration != first+2 {
			t.Fatal("unchanged completion spuriously invalidated")
		}
	})
	t.Run("discard-all-two-consumers", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		sessionSQL(t, s, "SET session_replication_role=replica")
		s.DiscardPlansGeneration = ^uint64(0) - 2
		sessionSQL(t, s, "DISCARD ALL")
		if s.DiscardPlansGeneration != ^uint64(0) {
			t.Fatal("DISCARD ALL failed to consume reset and explicit discard generations", s.DiscardPlansGeneration)
		}
	})
	t.Run("detached-exception-restoration", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		op.Restricted = &session.RestrictedInvocation{Owner: s.Authorization.State.CurrentUser}
		store := op.EffectiveSettings()
		mark := op.CaptureInvocation()
		s.DiscardPlansGeneration = ^uint64(0) - 8
		e.Mu.Lock()
		store.Mu.Lock()
		s.Mu.Lock()
		definition := e.Settings.Definitions()["session_replication_role"]
		value, _, err := store.ParseValue(definition, "replica")
		if err == nil {
			_, err = store.ApplyLocked(op, core.DomainOf(e), e.Settings, definition, &value, nil, session.SettingLocal)
		}
		s.Mu.Unlock()
		store.Mu.Unlock()
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		first := s.DiscardPlansGeneration
		mark.Restore()
		if s.DiscardPlansGeneration != first+1 || op.EffectiveSettings().TypedValue("session_replication_role").Text != "origin" || s.Settings.TypedValue("session_replication_role").Text != "origin" {
			t.Fatal("exception restore lost physical generation or leaked detached value")
		}
	})
	t.Run("detached-exit", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE parent(id INT)")
		parent := e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "parent").Address)
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(nil)
		if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
			t.Fatal(err)
		}
		s.DiscardPlansGeneration = ^uint64(0) - 6
		before := s.DiscardPlansGeneration
		if err := scope.WithAmcheckOwner(parent, func(*exec.Context) error {
			_, err := core.EngineExecuteStatement(e, scope, enginetest.Statement(t, "SET session_replication_role=replica"), 0, nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if s.DiscardPlansGeneration != before+2 || op.Restricted != nil || s.Settings.TypedValue("session_replication_role").Text != "origin" {
			t.Fatal("detached exit retained changed role or failed to invalidate")
		}
	})
	t.Run("same-value-at-exhaustion", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		s.DiscardPlansGeneration = ^uint64(0)
		for _, q := range []string{"SET session_replication_role=origin", "RESET session_replication_role", "RESET ALL"} {
			sessionSQL(t, s, q)
		}
		if s.DiscardPlansGeneration != ^uint64(0) || s.Settings.TypedValue("session_replication_role").Text != "origin" {
			t.Fatal("equal effective assignments consumed generation")
		}
	})
}
