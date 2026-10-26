package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAmcheckRestrictedContext(t *testing.T) {
	for _, outcome := range []string{"success", "error", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE ROLE am_owner")
			enginetest.Run(t, e, "CREATE TABLE owned(n INT)")
			enginetest.Run(t, e, "ALTER TABLE owned OWNER TO am_owner")
			parent := e.Catalog.State.MustRelation(enginetest.RoleTableRef(t, e, "owned").Address)
			sess := sessionStart(t, e, nil)
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
			if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
				t.Fatal(err)
			}
			if err := core.ScopeSnapshotDataPhase(scope); err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			beforeValues, beforeTyped, beforePlaceholders := maps.Clone(sess.Settings.Values), maps.Clone(sess.Settings.Typed), maps.Clone(sess.Settings.Placeholders)
			beforeActions := maps.Clone(sess.Settings.Actions)
			beforeIdentity := op.RoleState()
			beforeModes := []any{scope.Owner.Isolation, scope.Owner.ReadOnly, scope.Owner.Deferrable}
			beforeTimers, beforeRoleAction := sess.Timeouts, sess.Settings.RoleAction
			outerInput, outerOutput, outerIdentity := scope.Input, scope.Output, scope.Identity
			primary := errors.New("recipe failure")
			err = scope.WithAmcheckOwner(parent, func(evaluation *exec.Context) error {
				e.Mu.Lock()
				auth, err := scope.RoutineAuthorizationLocked()
				e.Mu.Unlock()
				if err != nil || auth.State.CurrentUser != parent.Owner || auth.State.SessionUser != beforeIdentity.SessionUser || !op.Restricted.SafePath {
					t.Fatal("owner invocation authority", auth.State, err)
				}
				if evaluation.RoutineAuthority == nil || evaluation.Input.Regclass == nil || evaluation.Input.Regtype == nil || evaluation.Input.Regrole == nil || evaluation.Input.Operator == nil || evaluation.Input.Regproc == nil || evaluation.Input.ACLItem == nil || evaluation.Input.RegTS == nil || evaluation.Input.TypeDiagnosticName == nil {
					t.Fatal("borrowed caller input authority")
				}
				path, _ := op.Setting("search_path")
				if path != "pg_catalog, pg_temp" {
					t.Fatal("18.6 initial safe path", path)
				}
				invoke := func(query string) (err error) {
					// Each recipe command needs its own admitted lexical scratch,
					// just like executeSQLRoutineCommand, not the outer SELECT's ref.
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
				for _, query := range []string{"SET application_name='local'", "SET demo.amcheck='local'", "SELECT set_config('statement_timeout','7s',false)", "SET transaction_timeout='8s'", "SET idle_session_timeout='9s'", "SHOW application_name", "SELECT current_setting('application_name')", "RESET application_name", "SET search_path=public"} {
					if err := invoke(query); err != nil {
						t.Fatal(query, err)
					}
				}
				if value, _ := op.Setting("demo.amcheck"); value != "local" || !op.EffectiveSettings().Placeholders["demo.amcheck"] {
					t.Fatal("local placeholder", value)
				}
				if op.Restricted.SafePath {
					t.Fatal("successful path assignment kept fixed marker")
				}
				// Nested activation must not erase the owner or local image.
				restore := scope.ActivateSQLRoutine(nil, catalog.Read{})
				e.Mu.Lock()
				nested, err := scope.RoutineAuthorizationLocked()
				e.Mu.Unlock()
				if err != nil || nested.State.CurrentUser != parent.Owner || core.AuthorizationSettings(&nested) != op.EffectiveSettings() {
					t.Fatal("nested owner/settings recapture", err)
				}
				if err := restore(nil); err != nil {
					t.Fatal(err)
				}
				if err := scope.WithAmcheckOwner(parent, func(*exec.Context) error {
					if value, _ := op.Setting("demo.amcheck"); value != "local" {
						t.Fatal("nested image lost parent", value)
					}
					if value, _ := op.Setting("search_path"); value != "pg_catalog, pg_temp" {
						t.Fatal("nested initial path", value)
					}
					return invoke("SET demo.amcheck='nested'")
				}); err != nil {
					t.Fatal(err)
				}
				if value, _ := op.Setting("demo.amcheck"); value != "local" {
					t.Fatal("nested image leaked", value)
				}
				for _, tc := range []struct{ query, message string }{
					{"PREPARE forbidden AS SELECT 1", "cannot execute PREPARE within security-restricted operation"},
					{"DEALLOCATE ALL", "cannot execute DEALLOCATE within security-restricted operation"},
					{"CLOSE ALL", "cannot execute CLOSE within security-restricted operation"},
					{"LISTEN forbidden", "cannot execute LISTEN within security-restricted operation"},
					{"UNLISTEN *", "cannot execute UNLISTEN within security-restricted operation"},
					{"DISCARD PLANS", "cannot execute DISCARD within security-restricted operation"},
					{"CREATE TEMP TABLE forbidden(n INT)", "cannot create temporary table within security-restricted operation"},
					{"DECLARE forbidden CURSOR WITH HOLD FOR SELECT 1", "cannot create a cursor WITH HOLD within security-restricted operation"},
					{"SET ROLE am_owner", `cannot set parameter "role" within security-restricted operation`},
					{"RESET SESSION AUTHORIZATION", `cannot set parameter "session_authorization" within security-restricted operation`},
					{"SELECT set_config('role','am_owner',false)", `cannot set parameter "role" within security-restricted operation`},
				} {
					amcheckUnitDiagnostic(t, invoke(tc.query), sqlstate.Error{Code: "42501", Message: tc.message})
				}
				if err := invoke("NOTIFY allowed"); err != nil {
					t.Fatal("blanket restricted utility rejection", err)
				}
				// Real PL exception rollback restores only the complete local
				// image and modes, never caller settings or live timer state.
				mark, err := scope.BeginPLException()
				if err != nil {
					t.Fatal(err)
				}
				if err := invoke("SET transaction_read_only=on"); err != nil {
					t.Fatal(err)
				}
				if !op.EffectiveReadOnly() || scope.Owner.ReadOnly {
					t.Fatal("local characteristic mutated owner or not enforced")
				}
				amcheckUnitDiagnostic(t, invoke("SET transaction_read_only=off"), sqlstate.Error{Code: "25001", Message: "cannot set transaction read-write mode inside a read-only transaction"})
				amcheckUnitDiagnostic(t, invoke("INSERT INTO owned VALUES(1)"), sqlstate.Error{Code: "25006", Message: "cannot execute INSERT in a read-only transaction"})
				if err := invoke("SET demo.after_mark='new'"); err != nil {
					t.Fatal(err)
				}
				if err := invoke("SET search_path=pg_catalog"); err != nil {
					t.Fatal(err)
				}
				if err := scope.EndPLException(mark, true, nil); err != nil {
					t.Fatal(err)
				}
				if _, ok := op.Setting("demo.after_mark"); ok {
					t.Fatal("placeholder escaped exception image rollback")
				}
				if value, _ := op.Setting("search_path"); value != "public" {
					t.Fatal("path mark lost", value)
				}
				if op.EffectiveReadOnly() {
					t.Fatal("local mode rollback lost")
				}
				if err := invoke("SET transaction_read_only=on"); err != nil {
					t.Fatal(err)
				}
				amcheckUnitDiagnostic(t, invoke("SET transaction_read_only=off"), sqlstate.Error{Code: "25001", Message: "transaction read-write mode must be set before any query"})
				if outcome == "error" {
					return primary
				}
				if outcome == "cancel" {
					cancel()
					return op.Context().Err()
				}
				return nil
			})
			if outcome == "success" && err != nil || outcome == "error" && !errors.Is(err, primary) || outcome == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if op.Restricted != nil || op.RoleState() != beforeIdentity || !reflect.DeepEqual(sess.Settings.Values, beforeValues) || !reflect.DeepEqual(sess.Settings.Typed, beforeTyped) || !reflect.DeepEqual(sess.Settings.Placeholders, beforePlaceholders) || !reflect.DeepEqual(sess.Settings.Actions, beforeActions) || !reflect.DeepEqual([]any{scope.Owner.Isolation, scope.Owner.ReadOnly, scope.Owner.Deferrable}, beforeModes) {
				t.Fatal("invocation leaked session authority/settings/modes")
			}
			if !reflect.DeepEqual(sess.Timeouts, beforeTimers) || sess.Settings.RoleAction != beforeRoleAction {
				t.Fatal("invocation changed caller lifetime timers or role setting actions")
			}
			if reflect.ValueOf(scope.Input.TypeDiagnosticName).Pointer() != reflect.ValueOf(outerInput.TypeDiagnosticName).Pointer() || !reflect.DeepEqual(scope.Output, outerOutput) || !reflect.DeepEqual(scope.Identity, outerIdentity) {
				t.Fatal("invocation leaked captured codec/identity")
			}
		})
	}
}

// Keep the result type used by runtime callbacks honest: successful VOID is
// distinct from SQL NULL even when the presentation is empty.
func TestAmcheckVoidValue(t *testing.T) {
	e := engine.New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Void)
	value := sqltype.VoidValue(typ)
	text, err := typ.Output(context.Background(), value)
	if err != nil || value.IsNull() || text != "" {
		t.Fatal(value, text, err)
	}
}
