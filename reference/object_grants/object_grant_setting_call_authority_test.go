package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantSettingCallAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		readTarget := "pg_catalog.current_setting(text)"
		writeTarget := "pg_catalog.set_config(text,text,boolean)"
		targets := []string{readTarget, writeTarget, "pg_catalog.current_setting(text,boolean)"}
		// Builtin ACLs survive PostgreSQL's public-schema reset. Register this
		// before any mutation, including failures while preparing a held reader.
		defer objectGrantRoutineCleanup(t, p, c, targets)
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		value := func(q, column string, want any, args ...any) fixture.Step {
			return fixture.Select(q, []string{column}, []string{"TEXT"}, [][]any{{want}}, args...)
		}
		failure := func(q, code, message string, args ...any) fixture.Step {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
			s.SelectRows, s.ErrorStage = true, "execute"
			return s
		}
		denied := func(q, name string, args ...any) fixture.Step {
			return failure(q, "42501", "permission denied for function "+name, args...)
		}
		importFailure := func(q string, args ...any) fixture.Step {
			s := fixture.ImplicitError(q, fixture.Diagnostic{
				Code: "22P02", Message: `invalid input syntax for type integer: "bad"`,
				Context: "unnamed portal parameter $1 = '...'",
			}, args...)
			s.SelectRows, s.ErrorStage = true, "execute"
			return s
		}

		// Both released current_setting overloads are strict; set_config is
		// nonstrict. Each overload owns its independent routine ACL.
		strictness := [][]any{{true}, {false}, {true}}
		run(fixture.Select("SELECT proisstrict FROM pg_proc WHERE oid IN (2077,2078,3294) ORDER BY oid",
			[]string{"proisstrict"}, []string{"BOOL"}, strictness))
		run(fixture.Exec("SET application_name='og_setting_public'", 0))
		query := "SELECT current_setting($1)"
		var held fixture.HeldStatement
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			held = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		})
		defer held.Statement.Close()
		// This SAME statement is actually warmed, then reused through both
		// revocations. Its non-NULL STABLE result must reflect SET, not a saved
		// setting value. No assertion calls a warmed plan custom or generic.
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(value("SELECT current_setting('application_name')", "current_setting", "og_setting_public"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, value(query, "current_setting", "og_setting_public", "application_name"))
		})
		readPhase := func(allowed bool, want string) {
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				// Each run prepares anew when the scenario is prepared. NULL is
				// deliberately never supplied to the warmed statement: PostgreSQL
				// generic-plan initialization can retain an EXECUTE requirement.
				run(value("SELECT current_setting(NULL)", "current_setting", nil))
				run(value(query, "current_setting", nil, nil))
				// Typed NULL arguments eliminate the strict invocation, while
				// invalid typed import still fails before routine admission.
				run(value("SELECT pg_catalog.current_setting($1::TEXT)", "current_setting", nil, nil))
				castQuery := "SELECT current_setting($1::INTEGER::TEXT)"
				run(value(castQuery, "current_setting", nil, nil))
				run(importFailure(castQuery, "bad"))
				for _, q := range []string{"SELECT current_setting('application_name')", "SELECT pg_catalog.current_setting('application_name')"} {
					if allowed {
						run(value(q, "current_setting", want))
					} else {
						run(denied(q, "current_setting"))
					}
				}
				s := value(query, "current_setting", want, "application_name")
				if !allowed {
					s = denied(query, "current_setting", "application_name")
				}
				run(s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				// The released sibling has its own ACL and true strictness.
				for _, tc := range []struct {
					q    string
					want any
				}{
					{"SELECT current_setting('application_name',true)", want},
					{"SELECT current_setting(NULL,true)", nil},
					{"SELECT current_setting('application_name',NULL)", nil},
				} {
					sibling := value(tc.q, "current_setting", tc.want)
					run(sibling)
				}
			})
		}
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+readTarget+" FROM PUBLIC", 0))
		readPhase(false, "og_setting_public")
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+readTarget+" TO og_object_reader", 0))
		run(fixture.Exec("SET application_name='og_setting_granted'", 0))
		readPhase(true, "og_setting_granted")
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+readTarget+" FROM og_object_reader", 0))
		readPhase(false, "og_setting_granted")
		// The optional overload has its own EXECUTE ACL. Strict NULLs bypass
		// invocation, not a non-NULL missing setting.
		optionalTarget := "pg_catalog.current_setting(text,boolean)"
		optionalQuery := "SELECT current_setting($1,$2)"
		var optionalHeld fixture.HeldStatement
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			stmt, err := c.PrepareContext(p.Ctx, optionalQuery)
			if err != nil {
				t.Fatal(err)
			}
			optionalHeld = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: optionalQuery}
		})
		defer optionalHeld.Statement.Close()
		optionalRead := func(allowed bool) {
			step := value(optionalQuery, "current_setting", "og_setting_granted", "application_name", false)
			if !allowed {
				step = denied(optionalQuery, "current_setting", "application_name", false)
			}
			fixture.RunStep(t, p.Ctx, optionalHeld, b.Recorder, step)
		}
		// Warm this exact statement with non-NULL arguments before changing
		// authority. Strict-NULL checks below use fresh calls, not this plan.
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for range 6 {
				optionalRead(true)
			}
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+optionalTarget+" FROM PUBLIC", 0))
		for _, allowed := range []bool{false, true, false} {
			if allowed {
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+optionalTarget+" TO og_object_reader", 0))
			} else {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+optionalTarget+" FROM og_object_reader", 0))
			}
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(value("SELECT current_setting(NULL,true)", "current_setting", nil))
				run(value("SELECT current_setting('application_name',NULL)", "current_setting", nil))
				if allowed {
					run(value("SELECT current_setting('application_name',true)", "current_setting", "og_setting_granted"))
					run(value("SELECT current_setting('og_missing_setting',true)", "current_setting", nil))
				} else {
					run(denied("SELECT current_setting('application_name',true)", "current_setting"))
					run(denied("SELECT current_setting('og_missing_setting',true)", "current_setting"))
				}
				optionalRead(allowed)
				// Granting the optional overload cannot authorize its sibling.
				fixture.RunStep(t, p.Ctx, held, b.Recorder, denied(query, "current_setting", "application_name"))
			})
			// The same warmed handle must also follow effective SET ROLE,
			// rather than retain either the preparing or last executing user.
			optionalRead(true)
			run(fixture.Exec("SET ROLE og_object_reader", 0))
			run(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"og_object_reader"}}))
			optionalRead(allowed)
			run(fixture.Exec("RESET ROLE", 0))
			optionalRead(true)
		}
		// The inverse grant must likewise leave the optional overload denied.
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+readTarget+" TO og_object_reader", 0))
		run(fixture.Exec("SET ROLE og_object_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, value(query, "current_setting", "og_setting_granted", "application_name"))
		optionalRead(false)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+readTarget+" FROM og_object_reader", 0))

		// set_config is a different, NONSTRICT routine. Even a NULL name
		// requires EXECUTE; granting it restores the real 22004 body error.
		writeQuery := "SELECT set_config($1,$2,$3)"
		writeStmt, err := c.PrepareContext(p.Ctx, writeQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer writeStmt.Close()
		writeHeld := fixture.HeldStatement{Executor: c, Statement: writeStmt, SQL: writeQuery}
		writePhase := func(allowed bool) {
			run(fixture.Exec("SET application_name='og_setting_retained'", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				badBool := fixture.ImplicitError(writeQuery, fixture.Diagnostic{
					Code: "22P02", Message: `invalid input syntax for type boolean: "maybe"`,
					Context: "unnamed portal parameter $3 = '...'",
				}, nil, "ignored", []byte("maybe"))
				badBool.SelectRows, badBool.ErrorStage = true, "execute"
				run(badBool)
				fixture.RunStep(t, p.Ctx, writeHeld, b.Recorder, badBool)
				for _, args := range [][]any{
					{"application_name", "og_setting_written", false},
					{"application_name", nil, nil},
					{nil, "ignored", false},
					{nil, nil, nil},
				} {
					s := value(writeQuery, "set_config", "og_setting_written", args...)
					if args[0] == nil {
						s = failure(writeQuery, "22004", "SET requires parameter name", args...)
					} else if args[1] == nil {
						s = value(writeQuery, "set_config", "", args...)
					}
					if !allowed {
						s = denied(writeQuery, "set_config", args...)
					}
					run(s)
					fixture.RunStep(t, p.Ctx, writeHeld, b.Recorder, s)
				}
				literal := "SELECT set_config(NULL,NULL,NULL)"
				if allowed {
					run(failure(literal, "22004", "SET requires parameter name"))
				} else {
					run(denied(literal, "set_config"))
				}
				want := "og_setting_retained"
				if allowed {
					want = ""
				}
				// SHOW has no current_setting EXECUTE edge. Denials and bad
				// imports publish no setting prefix; authorized NULL resets it.
				run(value("SHOW application_name", "application_name", want))
			})
		}
		writePhase(true)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+writeTarget+" FROM PUBLIC", 0))
		writePhase(false)
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+writeTarget+" TO og_object_reader", 0))
		writePhase(true)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+writeTarget+" FROM og_object_reader", 0))
		writePhase(false)
	})
}
