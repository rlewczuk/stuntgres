package core_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
)

// Exercise the actual owner boundary and successful TEMP storage opening. The
// physical initialization survives images; only setting values/actions unwind.
func TestAmcheckTemporarySettingsAuthority(t *testing.T) {
	for _, mode := range []string{"initialized-before-entry", "local-first-access", "nested-first-access", "exception-rollback"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE am_temp_parent(n INT)")
			parent := e.Catalog.State.MustRelation(enginetest.RoleTableRef(t, e, "am_temp_parent").Address)
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TEMP TABLE am_temp_data(n INT)")
			sessionSQL(t, s, "SET temp_buffers=2048")
			if mode == "initialized-before-entry" {
				sessionSQL(t, s, "SELECT n FROM am_temp_data")
			}
			sessionSQL(t, s, "BEGIN")
			sessionSQL(t, s, "SET application_name='caller-journal'")
			beforeValues, beforeTyped := maps.Clone(s.Settings.Values), maps.Clone(s.Settings.Typed)
			beforeActions := maps.Clone(s.Settings.Actions)
			beforeTimers, beforeRoleAction := s.Timeouts, s.Settings.RoleAction
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
			invoke := func(query string) (err error) {
				restore := scope.ActivateSQLRoutine(nil, catalog.Read{})
				defer func() { err = restore(err) }()
				finish, err := scope.BeginRoutineCommand()
				if err != nil {
					return err
				}
				defer func() { err = finish(err) }()
				stmt := enginetest.Statement(t, query)
				if err = scope.AcquireRelation(stmt, true); err != nil {
					return err
				}
				result, err := core.EngineExecuteStatement(e, scope, stmt, 0, nil)
				if result.Rows != nil {
					result.Rows.Close()
				}
				return err
			}
			must := func(query string) {
				t.Helper()
				if err := invoke(query); err != nil {
					t.Fatal(query, err)
				}
			}
			setting := func(want string) {
				t.Helper()
				if got, _ := op.Setting("temp_buffers"); got != want {
					t.Fatalf("effective temp_buffers = %q, want %q", got, want)
				}
			}
			reject := func(candidate int) {
				t.Helper()
				store := op.EffectiveSettings()
				values, typed, actions := maps.Clone(store.Values), maps.Clone(store.Typed), maps.Clone(store.Actions)
				err := invoke(fmt.Sprintf("SET temp_buffers=%d", candidate))
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023",
					Message: fmt.Sprintf(`invalid value for parameter "temp_buffers": %d`, candidate),
					Detail:  `"temp_buffers" cannot be changed after any temporary tables have been accessed in the session.`}
				var got *sqlstate.Error
				if !errors.As(err, &got) || *got != want {
					t.Fatalf("diagnostic = %#v (%v), want %#v", got, err, want)
				}
				if !reflect.DeepEqual(store.Values, values) || !reflect.DeepEqual(store.Typed, typed) || !reflect.DeepEqual(store.Actions, actions) {
					t.Fatal("rejected candidate changed effective values/actions")
				}
			}
			frozen := 3072
			err = scope.WithAmcheckOwner(parent, func(*exec.Context) error {
				setting("16MB")
				switch mode {
				case "initialized-before-entry":
					frozen = 2048
					reject(3072)
				case "local-first-access":
					must("SET temp_buffers=3072")
					must("SELECT n FROM am_temp_data")
					reject(2048)
				case "nested-first-access":
					frozen = 4096
					must("SET temp_buffers=3072")
					if err := scope.WithAmcheckOwner(parent, func(*exec.Context) error {
						setting("24MB")
						must("SET temp_buffers=4096")
						must("SELECT n FROM am_temp_data")
						reject(3072)
						return nil
					}); err != nil {
						return err
					}
					setting("24MB")
					reject(3072)
				case "exception-rollback":
					mark, err := scope.BeginPLException()
					if err != nil {
						return err
					}
					must("SET temp_buffers=3072")
					// Initialize in a nested frame after the outer exception mark:
					// neither frame return nor rollback may restore the old zero.
					if err := scope.WithAmcheckOwner(parent, func(*exec.Context) error {
						must("SELECT n FROM am_temp_data")
						return nil
					}); err != nil {
						return err
					}
					if err := scope.EndPLException(mark, true, errors.New("caught recipe exception")); err != nil {
						return err
					}
					setting("16MB")
					reject(2048)
				}
				must(fmt.Sprintf("SET temp_buffers=%d", frozen))
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			setting("16MB")
			if op.Restricted != nil || !reflect.DeepEqual(s.Settings.Values, beforeValues) || !reflect.DeepEqual(s.Settings.Typed, beforeTyped) || !reflect.DeepEqual(s.Settings.Actions, beforeActions) || !reflect.DeepEqual(s.Timeouts, beforeTimers) || s.Settings.RoleAction != beforeRoleAction {
				t.Fatal("invocation changed caller values/actions/timers")
			}
			// A fresh invocation must use the same physical authority, not a
			// stale count from its caller's unchanged effective setting.
			if err := scope.WithAmcheckOwner(parent, func(*exec.Context) error {
				rejected := 2048
				if frozen == rejected {
					rejected = 3072
				}
				reject(rejected)
				must(fmt.Sprintf("SET temp_buffers=%d", frozen))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s.Settings.Actions, beforeActions) || !reflect.DeepEqual(s.Settings.Values, beforeValues) {
				t.Fatal("subsequent invocation changed caller journal")
			}
			// Ordinary caller assignment sees the same frozen authority even
			// though the caller's effective value was deliberately not changed.
			rejected := 2048
			if frozen == rejected {
				rejected = 3072
			}
			reject(rejected)
		})
	}
}
