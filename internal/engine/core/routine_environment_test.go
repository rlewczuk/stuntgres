package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineEffectiveUserEnvironment(t *testing.T) {
	for _, outcome := range []string{"success", "error", "cancel", "self-success", "self-error", "self-cancel"} {
		t.Run(outcome, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE ROLE re_owner")
			enginetest.Run(t, e, "CREATE SCHEMA re_owner AUTHORIZATION re_owner")
			enginetest.Run(t, e, "CREATE TABLE re_parent(n integer)")
			enginetest.Run(t, e, "ALTER TABLE re_parent OWNER TO re_owner")
			parent := e.Catalog.State.MustRelation(enginetest.RoleTableRef(t, e, "re_parent").Address)
			sess := sessionStart(t, e, nil)
			selected := strings.HasPrefix(outcome, "self-")
			outcome = strings.TrimPrefix(outcome, "self-")
			if selected {
				sess.Mu.Lock()
				sess.Authorization.State.ActiveRole, sess.Authorization.State.RoleText = true, "postgres"
				sess.Mu.Unlock()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := sess.Acquire(ctx)
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
			if err := core.ScopeSnapshotDataPhase(scope); err != nil {
				t.Fatal(err)
			}
			before := op.RoleState()
			textType, _ := e.CatalogRead().LookupType(sqltype.Text)
			boolType, _ := e.CatalogRead().LookupType(sqltype.Bool)
			assertSuperuser := func(want string) {
				t.Helper()
				value, err := core.HostOf(scope).InvokeSetting("current_setting", []sqltype.Value{sqltype.String(textType, "is_superuser")}, textType)
				if err != nil {
					t.Fatal(err)
				}
				got, err := value.Text()
				if err != nil || got != want {
					t.Fatalf("effective superuser = %q, %v; want %q", got, err, want)
				}
				roleValue, err := core.HostOf(scope).InvokeSetting("current_setting", []sqltype.Value{sqltype.String(textType, "role")}, textType)
				if err != nil {
					t.Fatal(err)
				}
				gotRole, err := roleValue.Text()
				wantRole := "none"
				if selected {
					wantRole = "postgres"
				}
				if err != nil || gotRole != wantRole {
					t.Fatal("effective actor changed physical role projection", gotRole, err)
				}
				_, captured, err := e.PersistedSettingCurrent(op, "role")
				if err != nil || captured != wantRole || op.RoleState() != before {
					t.Fatal("physical FROM CURRENT capture changed role owner", captured, err)
				}
			}
			assertIdentityRestriction := func(contextName string) {
				t.Helper()
				for _, name := range []string{"role", "session_authorization"} {
					for _, reset := range []bool{false, true} {
						value := sqltype.String(textType, "re_owner")
						if reset {
							value = sqltype.Null(textType)
						}
						_, err := core.HostOf(scope).InvokeSetting("set_config", []sqltype.Value{
							sqltype.String(textType, name), value, sqltype.Boolean(boolType, false),
						}, textType)
						var diagnostic *sqlstate.Error
						if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" ||
							diagnostic.Message != `cannot set parameter "`+name+`" within `+contextName {
							t.Fatal("identity mutation lost restriction precedence", name, reset, err)
						}
						if op.RoleState() != before {
							t.Fatal("denied identity mutation changed session roles")
						}
					}
				}
			}
			assertSuperuser("on")
			input, output := scope.Input, scope.Output
			rowSecurity := scope.RowSecurity
			oldTimeout := op.LockTimeout
			e.Mu.Lock()
			enclosingActor, err := scope.RoutineAuthorizationLocked()
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			scope.ForeignActionAuth = &enclosingActor
			definition := &catalog.Procedure{Owner: parent.Owner, SecurityDefiner: true,
				Configuration: catalog.NewRoutineConfiguration([]string{"datestyle=SQL, DMY", "row_security=off", "lock_timeout=37ms"})}
			leave, err := core.ScopeEnterRoutineEnvironment(scope, definition)
			if err != nil {
				t.Fatal(err)
			}
			if op.Restricted != nil || op.EffectiveSettings() != sess.Settings || op.RoleState() != before || scope.ForeignActionAuth != nil {
				t.Fatal("definer reused restricted context or changed session identity")
			}
			e.Mu.Lock()
			auth, err := scope.RoutineAuthorizationLocked()
			e.Mu.Unlock()
			if err != nil || auth.State.CurrentUser != parent.Owner || auth.State.SessionUser != before.SessionUser || scope.Identity.CurrentPrincipal != parent.Owner {
				t.Fatal("effective identity did not refresh together", auth.State, scope.Identity, err)
			}
			assertSuperuser("off")
			assertIdentityRestriction("security-definer function")
			// An RI action reached later inside the definer retains its own
			// relation actor; only the enclosing action was superseded.
			scope.ForeignActionAuth = &enclosingActor
			e.Mu.Lock()
			childActor, childErr := scope.RoutineAuthorizationLocked()
			e.Mu.Unlock()
			scope.ForeignActionAuth = nil
			if childErr != nil || childActor.State.CurrentUser != before.CurrentUser {
				t.Fatal("definer overrode a later RI actor", childActor.State, childErr)
			}
			path, err := core.ReferencePath(auth)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, address := range path {
				found = found || auth.Catalog.Get(address).Name() == "re_owner"
			}
			if !found {
				t.Fatal("$user still resolved as session caller", path)
			}
			if scope.Input.DateStyle != (sqltype.DateStyle{Style: sqltype.DateSQL, Order: sqltype.DateDMY}) || scope.Output.DateStyle != scope.Input.DateStyle || scope.RowSecurity || op.LockTimeout != 37*time.Millisecond {
				t.Fatal("typed presentation or operation policy stayed stale", scope.Input.DateStyle, scope.Output.DateStyle, scope.RowSecurity, op.LockTimeout)
			}
			invokerLeave, err := core.ScopeEnterRoutineEnvironment(scope, &catalog.Procedure{})
			if err != nil {
				t.Fatal(err)
			}
			if err := invokerLeave(nil); err != nil {
				t.Fatal(err)
			}
			if op.EffectiveUser == nil || op.EffectiveUser.Owner != parent.Owner {
				t.Fatal("invoker discarded enclosing actor")
			}
			// A maintenance frame nested inside a definer still owns its stronger
			// local settings and restrictions, and returns to the definer frame.
			if err := scope.WithAmcheckOwner(parent, func(*exec.Context) error {
				if op.Restricted == nil || op.EffectiveSettings() == sess.Settings {
					t.Fatal("maintenance semantics were weakened")
				}
				assertIdentityRestriction("security-restricted operation")
				nestedLeave, err := core.ScopeEnterRoutineEnvironment(scope, &catalog.Procedure{Owner: before.CurrentUser, SecurityDefiner: true})
				if err != nil {
					return err
				}
				e.Mu.Lock()
				nested, err := scope.RoutineAuthorizationLocked()
				e.Mu.Unlock()
				if err != nil || nested.State.CurrentUser != before.CurrentUser || op.Restricted == nil {
					t.Fatal("nested definer lost actor or enclosing restriction", nested.State, err)
				}
				if err := nestedLeave(nil); err != nil {
					return err
				}
				e.Mu.Lock()
				restored, err := scope.RoutineAuthorizationLocked()
				e.Mu.Unlock()
				if err != nil || restored.State.CurrentUser != parent.Owner || op.Restricted == nil {
					t.Fatal("nested definer did not restore maintenance actor", restored.State, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if op.Restricted != nil || op.EffectiveUser == nil {
				t.Fatal("maintenance exit lost definer")
			}
			assertSuperuser("off")
			var primary error
			if outcome == "error" {
				primary = errors.New("body failure")
			}
			if outcome == "cancel" {
				cancel()
				primary = op.Context().Err()
			}
			if err := leave(primary); !errors.Is(err, primary) {
				t.Fatal("cleanup replaced primary diagnostic", err)
			}
			if op.EffectiveUser != nil || op.Restricted != nil || op.RoleState() != before || sess.Settings.NestLevel != 1 || scope.ForeignActionAuth != &enclosingActor {
				t.Fatal("unbalanced routine environment", op.EffectiveUser, op.RoleState(), sess.Settings.NestLevel)
			}
			if outcome != "cancel" {
				assertSuperuser("on")
			}
			if scope.Input.DateStyle != input.DateStyle || scope.Output.DateStyle != output.DateStyle || scope.RowSecurity != rowSecurity || op.LockTimeout != oldTimeout {
				t.Fatal("callee presentation or policy escaped")
			}
			if !reflect.DeepEqual(sess.Settings.Values, map[string]string{}) || len(sess.Settings.Actions) != 0 {
				t.Fatal("SAVE-only entry left overrides/actions", sess.Settings.Values, sess.Settings.Actions)
			}
		})
	}
	t.Run("partial_entry", func(t *testing.T) {
		e := engine.New()
		sess := sessionStart(t, e, nil)
		op, err := sess.Acquire(context.Background())
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
		before := op.RoleState()
		beforeModes := []any{scope.Owner.Isolation, scope.Owner.ReadOnly, scope.Owner.Deferrable}
		definition := &catalog.Procedure{Owner: before.CurrentUser, SecurityDefiner: true,
			Configuration: catalog.NewRoutineConfiguration([]string{"application_name=partial", "transaction_read_only=on"})}
		leave, err := core.ScopeEnterRoutineEnvironment(scope, definition)
		if err == nil || leave != nil {
			t.Fatal("unsupported SAVE was installed", err)
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != `parameter "transaction_read_only" cannot be set locally in functions` {
			t.Fatal("wrong SAVE entry diagnostic", err)
		}
		if op.EffectiveUser != nil || op.RoleState() != before || len(sess.Settings.Values) != 0 || len(sess.Settings.Actions) != 0 ||
			!reflect.DeepEqual(beforeModes, []any{scope.Owner.Isolation, scope.Owner.ReadOnly, scope.Owner.Deferrable}) {
			t.Fatal("failed entry leaked actor, first setting or transaction mode")
		}
	})
}
