package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	missing := fixture.ImplicitError("SELECT bt_index_check('absent')", fixture.Diagnostic{Code: "42883", Message: "function bt_index_check(unknown) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
	p.Step(t, missing)
	amcheckSetup(t, p)
	for _, tc := range []struct{ query, message string }{
		{"SELECT bt_index_check()", "function bt_index_check() does not exist"},
		{"SELECT bt_index_check('am_idx',true,true,true)", "function bt_index_check(unknown, boolean, boolean, boolean) does not exist"},
		{"SELECT bt_index_check(no_such => 'am_idx')", "function bt_index_check(no_such => unknown) does not exist"},
	} {
		p.Step(t, fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42883", Message: tc.message, Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}))
	}
	missingTarget := fixture.ImplicitError("SELECT bt_index_check('am_absent')", fixture.Diagnostic{Code: "42P01", Message: `relation "am_absent" does not exist`, Position: "23"})
	p.Step(t, missingTarget)
	missingTarget = fixture.Prepared(missingTarget)
	missingTarget.ErrorStage = "prepare"
	p.Step(t, missingTarget)
	p.Step(t, fixture.Exec("CREATE INDEX am_hash ON am_heap USING hash(n)", 0))
	p.Step(t, fixture.Exec("CREATE VIEW am_view AS SELECT n FROM am_heap", 0))
	p.Step(t, fixture.Exec("CREATE SEQUENCE am_sequence", 0))
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ target, code, message, detail string }{
			{"'am_heap'", "42809", `"am_heap" is not an index`, ""},
			{"'am_view'", "42809", `"am_view" is not an index`, ""},
			{"'am_sequence'", "42809", `"am_sequence" is not an index`, ""},
			{"17::regclass", "XX000", "could not open relation with OID 17", ""},
			{"'am_hash'", "0A000", `expected "btree" index as targets for verification`, `Relation "am_hash" is a hash index.`},
		} {
			step := fixture.ImplicitError("SELECT bt_index_check("+tc.target+")", fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail})
			step.SelectRows = true
			step.ErrorStage = "execute"
			if prepared {
				step = fixture.Prepared(step)
			}
			p.Step(t, step)
		}
	}
	p.Step(t, amcheckSelect("SELECT bt_index_check(17::regclass,NULL::boolean) AS ok", true))
	for _, backend := range p.Backends() {
		t.Run(backend.Name+"/foreign_temp", func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			fixture.LockExec(t, p.Ctx, a, "CREATE TEMP TABLE am_foreign(n INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "CREATE INDEX am_foreign_idx ON am_foreign(n)", 0)
			var oid int64
			if err := a.QueryRowContext(p.Ctx, "SELECT 'am_foreign_idx'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			query := fmt.Sprintf("SELECT bt_index_parent_check(%d::regclass)", oid)
			step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "cannot access temporary tables of other sessions", Detail: `Index "am_foreign_idx" is associated with temporary relation.`})
			step.SelectRows = true
			fixture.RunStep(t, p.Ctx, b, backend.Recorder, step)
			fixture.RunStep(t, p.Ctx, b, backend.Recorder, amcheckSelect("SELECT bt_index_parent_check('am_idx',true,true,true) AS ok", false))
			tx, err := b.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.ImplicitError("SELECT bt_index_check('am_heap')", fixture.Diagnostic{Code: "42809", Message: `"am_heap" is not an index`}))
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.ImplicitError("SELECT bt_index_check('am_idx')", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, b, backend.Recorder, amcheckSelect("SELECT bt_index_check('am_idx',true,true) AS ok", false))
		})
	}
}

func TestReferenceAmcheckPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"am_reader"})
	p := fixture.NewScenario(t)
	amcheckSetup(t, p)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, step) }
			run(fixture.Exec("CREATE ROLE am_reader", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO am_reader", 0))
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			run(fixture.Exec("SET ROLE am_reader", 0))
			for _, overload := range amcheckOverloads {
				args := "'public.am_idx'::regclass"
				for i := 1; i < overload.arguments; i++ {
					args += ",true"
				}
				step := fixture.ImplicitError("SELECT "+overload.name+"("+args+")", fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + overload.name})
				step.SelectRows = true
				run(step)
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION bt_index_check(regclass) TO am_reader", 0))
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'am_idx'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("SET ROLE am_reader", 0))
			query := "SELECT bt_index_check($1::regclass) AS ok"
			held, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			owner := fixture.HeldStatement{Executor: c, Statement: held, SQL: query}
			fixture.RunStep(t, p.Ctx, owner, backend.Recorder, amcheckSelect(query, false, oid))
			run(fixture.ImplicitError("SELECT n FROM am_heap", fixture.Diagnostic{Code: "42501", Message: "permission denied for table am_heap"}))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION bt_index_check(regclass) FROM am_reader", 0))
			run(fixture.Exec("SET ROLE am_reader", 0))
			denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function bt_index_check"}, oid)
			denied.SelectRows = true
			fixture.RunStep(t, p.Ctx, owner, backend.Recorder, denied)
			run(fixture.Exec("RESET ROLE", 0))
			run(amcheckSelect("SELECT bt_index_check('am_idx') AS ok", false))
			run(fixture.Exec("REVOKE USAGE ON SCHEMA public FROM am_reader", 0))
			run(fixture.Exec("DROP ROLE am_reader", 0))
		})
	}
}

func TestReferenceAmcheckSecurity(t *testing.T) {
	p := fixture.NewScenario(t)
	amcheckSetup(t, p)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, step) }
			var beforeUser, beforeSession string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_user,session_user").Scan(&beforeUser, &beforeSession); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("SET application_name='amcheck caller'", 0))
			run(fixture.Exec("SET search_path=public,pg_catalog", 0))
			run(amcheckSelect("SELECT bt_index_parent_check('am_expr',true,true,true) AS ok", false))
			run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"amcheck caller"}}))
			run(fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"public, pg_catalog"}}))
			var afterUser, afterSession string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_user,session_user").Scan(&afterUser, &afterSession); err != nil || afterUser != beforeUser || afterSession != beforeSession {
				t.Fatal("caller identity changed", afterUser, afterSession, err)
			}
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("SET TRANSACTION READ ONLY", 0))
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, amcheckSelect("SELECT bt_index_parent_check('am_idx',true,true,true) AS ok", false))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
