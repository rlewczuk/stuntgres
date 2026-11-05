package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePostgresFDWTypeInputDiagnostics(t *testing.T) {
	p, _ := postgresFDWPhysicalScenario(t)
	fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`CREATE TABLE "pf source ""quoted".input_diagnostics(head text,id integer NOT NULL,payload text,tail text)`, 0))
	postgresFDWEach(t, p, func(t *testing.T, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWPhysicalInstall(t, b, run)
		for _, query := range []string{
			"CREATE SCHEMA pf_helpers",
			"CREATE SEQUENCE pf_helpers.diagnostic_calls START 1",
			"CREATE FUNCTION pf_helpers.diagnostic_allowed(bytea) RETURNS boolean LANGUAGE sql VOLATILE STRICT AS $$SELECT nextval('pf_helpers.diagnostic_calls')>0 AND octet_length($1)<=2$$",
			"CREATE DOMAIN pf_helpers.diagnostic_bytes AS bytea CHECK(pf_helpers.diagnostic_allowed(VALUE))",
			"CREATE TABLE pf_local(v bytea)",
			`CREATE FOREIGN TABLE pf_bad_input(unused text OPTIONS(column_name 'head'),id integer NOT NULL,bytes bytea OPTIONS(column_name 'payload'),tail text) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'input_diagnostics')`,
			`CREATE FOREIGN TABLE pf_bad_domain(unused text OPTIONS(column_name 'head'),id integer NOT NULL,bytes pf_helpers.diagnostic_bytes OPTIONS(column_name 'payload'),tail text) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'input_diagnostics')`,
		} {
			run(fixture.Exec(query, 0))
		}
		for _, table := range []string{"pf_bad_input", "pf_bad_domain"} {
			t.Run(table, func(t *testing.T) {
				defer postgresFDWRemoteCleanup(t, p, c)
				runCase := func(step fixture.Step) {
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				}
				for _, step := range []fixture.Step{
					fixture.Exec(`TRUNCATE "pf source ""quoted".input_diagnostics`, 0),
					fixture.Exec(`INSERT INTO "pf source ""quoted".input_diagnostics VALUES('unused',1,NULL,'first'),('unused',2,E'\\xGG','second')`, 2),
				} {
					fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
				}
				runCase(fixture.Exec("TRUNCATE pf_local", 0))
				runCase(fixture.Exec("INSERT INTO pf_local VALUES(decode('7a','hex'))", 1))
				runCase(fixture.Exec("ALTER SEQUENCE pf_helpers.diagnostic_calls RESTART WITH 1", 0))
				calls := func(n int64) {
					t.Helper()
					value, called := n, true
					if table == "pf_bad_input" || n == 0 {
						value, called = 1, false
					}
					runCase(fixture.Select("SELECT last_value,is_called FROM pf_helpers.diagnostic_calls", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{value, called}}))
				}
				calls(0)
				plain := fdwDiagnostic("22023", `invalid hexadecimal digit: "G"`)
				plain.Context = `column "bytes" of foreign table "` + table + `"`
				postgresFDWQueryFailure(t, p, b, c, "SELECT bytes::bytea AS bytes FROM "+table+" ORDER BY id", prepared, plain)
				// The projected bytes slot is neither the first local column nor the
				// first remote result slot. Column and relation aliases own Context.
				from := table + " AS f(skipped,local_id,picked,last)"
				query := "SELECT last,picked::bytea AS value,local_id FROM " + from + " ORDER BY local_id"
				want := fdwDiagnostic("22023", `invalid hexadecimal digit: "G"`)
				want.Context = `column "picked" of foreign table "f"`
				postgresFDWQueryFailure(t, p, b, c, query, prepared, want)
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				badHeld := fixture.ErrorDiagnostic(query, want)
				badHeld.SelectRows, badHeld.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, badHeld)
				// A failed source conversion must not partially publish local DML.
				insert := fixture.ErrorDiagnostic("INSERT INTO pf_local SELECT picked::bytea FROM "+from, want)
				insert.ErrorStage = "execute"
				runCase(insert)
				// The earlier NULL prefix never invokes the STRICT predicate and
				// malformed builtin input must fail before any domain check.
				calls(0)
				runCase(fixture.Select("SELECT v FROM pf_local", []string{"v"}, []string{"BYTEA"}, [][]any{{"z"}}))
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select(`SELECT id,payload,tail FROM "pf source ""quoted".input_diagnostics ORDER BY id`, []string{"id", "payload", "tail"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), nil, "first"}, {int64(2), `\xGG`, "second"}}))
				postgresFDWRemoteCleanup(t, p, c)
				// Correct the actual source, not a provider fault/fallback. Reuse the
				// same SQL session and the very statement that previously failed.
				for _, step := range []fixture.Step{
					fixture.Exec(`UPDATE "pf source ""quoted".input_diagnostics SET payload=E'\\x6364' WHERE id=2`, 1),
					fixture.Exec(`UPDATE "pf source ""quoted".input_diagnostics SET payload=E'\\x6162' WHERE id=1`, 1),
					fixture.Exec(`INSERT INTO "pf source ""quoted".input_diagnostics VALUES('unused',3,NULL,'null'),('unused',4,'','empty')`, 2),
				} {
					fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
				}
				good := fixture.Select(query, []string{"last", "value", "local_id"}, []string{"TEXT", "BYTEA", "INT4"}, [][]any{{"first", "ab", int64(1)}, {"second", "cd", int64(2)}, {"null", nil, int64(3)}, {"empty", "", int64(4)}})
				runCase(good)
				calls(3)
				for _, n := range []int64{6, 9} {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
					calls(n)
				}
				runCase(fixture.Exec("INSERT INTO pf_local SELECT picked::bytea FROM "+from, 4))
				calls(12)
				stored := fixture.Select("SELECT v FROM pf_local", []string{"v"}, []string{"BYTEA"}, [][]any{{"z"}, {"ab"}, {"cd"}, {nil}, {""}})
				stored.Bag = true
				runCase(stored)
				// An actual retired, unrequested attribute must not compact the
				// stored body's original aliases onto the wrong diagnostic slot.
				function := "input_diagnostic_" + table
				runCase(fixture.Exec("CREATE FUNCTION pf_helpers."+function+"() RETURNS bytea LANGUAGE sql BEGIN ATOMIC SELECT picked::bytea FROM "+from+" WHERE local_id=2; END", 0))
				functionQuery := "SELECT pf_helpers." + function + "() AS value"
				functionStmt, err := c.PrepareContext(p.Ctx, functionQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer functionStmt.Close()
				functionHeld := fixture.HeldStatement{Executor: c, Statement: functionStmt, SQL: functionQuery}
				runCase(fixture.Exec("ALTER FOREIGN TABLE "+table+" DROP COLUMN unused RESTRICT", 0))
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`UPDATE "pf source ""quoted".input_diagnostics SET payload=E'\\xGG' WHERE id=2`, 1))
				functionError := want
				functionError.Context += "\nSQL function \"" + function + "\" statement 1"
				postgresFDWQueryFailure(t, p, b, c, functionQuery, prepared, functionError)
				badFunction := fixture.ErrorDiagnostic(functionQuery, functionError)
				badFunction.SelectRows, badFunction.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, functionHeld, b.Recorder, badFunction)
				postgresFDWRemoteCleanup(t, p, c)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`UPDATE "pf source ""quoted".input_diagnostics SET payload=E'\\x6364' WHERE id=2`, 1))
				repaired := fixture.Select(functionQuery, []string{"value"}, []string{"BYTEA"}, [][]any{{"cd"}})
				runCase(repaired)
				fixture.RunStep(t, p.Ctx, functionHeld, b.Recorder, repaired)
			})
		}
	})
}
