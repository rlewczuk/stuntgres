package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePostgresFDWInstallation(t *testing.T) {
	postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWInstall(run)
		run(fixture.Select("SELECT extversion,extrelocatable,obj_description(oid,'pg_extension') AS description FROM pg_extension WHERE extname='postgres_fdw'", []string{"extversion", "extrelocatable", "description"}, []string{"TEXT", "BOOL", "TEXT"}, [][]any{{"1.2", true, "foreign-data wrapper for remote PostgreSQL servers"}}))
		run(fixture.Select("SELECT p.proname,l.lanname,p.proisstrict,p.provolatile,p.prorettype::integer,p.pronargs::integer,p.probin,p.prosrc FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid IN('postgres_fdw_handler()'::regprocedure,'postgres_fdw_validator(text[],oid)'::regprocedure) ORDER BY p.proname", []string{"proname", "lanname", "proisstrict", "provolatile", "prorettype", "pronargs", "probin", "prosrc"}, []string{"NAME", "NAME", "BOOL", "CHAR", "INT4", "INT4", "TEXT", "TEXT"}, [][]any{{"postgres_fdw_handler", "c", true, "v", int64(3115), int64(0), "$libdir/postgres_fdw", "postgres_fdw_handler"}, {"postgres_fdw_validator", "c", true, "v", int64(2278), int64(2), "$libdir/postgres_fdw", "postgres_fdw_validator"}}))
		run(fixture.Select("SELECT w.fdwhandler='postgres_fdw_handler()'::regprocedure AS handler,w.fdwvalidator='postgres_fdw_validator(text[],oid)'::regprocedure AS validator,w.fdwowner=e.extowner AS owner,w.fdwoptions IS NULL AS no_options FROM pg_foreign_data_wrapper w JOIN pg_extension e ON e.extname='postgres_fdw' WHERE w.fdwname='postgres_fdw'", []string{"handler", "validator", "owner", "no_options"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		// Filter selected declarations; PG legitimately installs additional helpers.
		run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='postgres_fdw' AND d.refclassid=3079 AND d.deptype='e' AND ((d.classid=1255 AND d.objid IN('postgres_fdw_handler()'::regprocedure,'postgres_fdw_validator(text[],oid)'::regprocedure)) OR (d.classid=2328 AND d.objid=(SELECT oid FROM pg_foreign_data_wrapper WHERE fdwname='postgres_fdw')))", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER pf_alias HANDLER postgres_fdw_handler VALIDATOR postgres_fdw_validator", 0))
		run(fixture.Select("SELECT a.fdwhandler=s.fdwhandler AS same_handler,a.fdwvalidator=s.fdwvalidator AS same_validator FROM pg_foreign_data_wrapper a JOIN pg_foreign_data_wrapper s ON s.fdwname='postgres_fdw' WHERE a.fdwname='pf_alias'", []string{"same_handler", "same_validator"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	})
	for _, collision := range []struct{ name, setup, code, message, context string }{
		{"handler", "CREATE FUNCTION postgres_fdw_handler() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "42723", "function \"postgres_fdw_handler\" already exists with same argument types", "SQL statement \"CREATE FUNCTION postgres_fdw_handler()\nRETURNS fdw_handler\nAS '$libdir/postgres_fdw'\nLANGUAGE C STRICT\"\nextension script file \"postgres_fdw--1.0.sql\", near line 6"},
		{"validator", "CREATE FUNCTION postgres_fdw_validator(text[],oid) RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "42723", "function \"postgres_fdw_validator\" already exists with same argument types", "SQL statement \"CREATE FUNCTION postgres_fdw_validator(text[], oid)\nRETURNS void\nAS '$libdir/postgres_fdw'\nLANGUAGE C STRICT\"\nextension script file \"postgres_fdw--1.0.sql\", near line 11"},
		{"wrapper", "CREATE FOREIGN DATA WRAPPER postgres_fdw", "42710", `foreign-data wrapper "postgres_fdw" already exists`, "SQL statement \"CREATE FOREIGN DATA WRAPPER postgres_fdw\n  HANDLER postgres_fdw_handler\n  VALIDATOR postgres_fdw_validator\"\nextension script file \"postgres_fdw--1.0.sql\", near line 16"},
	} {
		t.Run("collision_"+collision.name, func(t *testing.T) {
			postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				run(fixture.Exec(collision.setup, 0))
				s := postgresFDWError("CREATE EXTENSION postgres_fdw", collision.code, collision.message)
				s.Diagnostic.Context = collision.context
				run(s)
				run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='postgres_fdw'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				if collision.name != "handler" {
					run(fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='postgres_fdw_handler'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			})
		})
	}
	t.Run("denied_install", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pf_actor"})
		postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
			run(fixture.Exec("CREATE ROLE pf_actor", 0))
			defer func() {
				for _, q := range []string{"RESET ROLE", "DROP OWNED BY pf_actor CASCADE", "DROP ROLE pf_actor"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(q, err)
					}
				}
			}()
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("GRANT CREATE ON DATABASE "+fdwQuote(database)+" TO pf_actor", 0))
			run(fixture.Exec("GRANT CREATE,USAGE ON SCHEMA public TO pf_actor", 0))
			run(fixture.Exec("SET ROLE pf_actor", 0))
			s := postgresFDWError("CREATE EXTENSION postgres_fdw", "42501", `permission denied to create extension "postgres_fdw"`)
			s.Diagnostic.Hint = "Must be superuser to create this extension."
			run(s)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='postgres_fdw'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT count(*) FROM pg_proc WHERE proname IN('postgres_fdw_handler','postgres_fdw_validator')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	})
}

func TestReferencePostgresFDWValidatorCalls(t *testing.T) {
	postgresFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWInstall(run)
		for _, class := range []int{0, 999999, 2328, 1417, 1418, 3118, 1249} {
			run(fileFDWVoid(postgresFDWCall(nil, class)))
		}
		for _, q := range []string{
			"SELECT postgres_fdw_validator(NULL::text[],1417::oid) AS validated",
			"SELECT postgres_fdw_validator(ARRAY['unknown=x'],NULL::oid) AS validated",
			"SELECT postgres_fdw_validator(NULL::text[],NULL::oid) AS validated",
			"SELECT postgres_fdw_validator($1::text[],$2::oid) AS validated",
		} {
			if q == "SELECT postgres_fdw_validator($1::text[],$2::oid) AS validated" {
				run(fixture.Select(q, []string{"validated"}, []string{"VOID"}, [][]any{{nil}}, nil, int64(1417)))
			} else {
				run(fixture.Select(q, []string{"validated"}, []string{"VOID"}, [][]any{{nil}}))
			}
		}
		run(fixture.Exec("CREATE TABLE pf_local(a text[],b oid)", 0))
		run(fixture.Exec("INSERT INTO pf_local VALUES(NULL,1417),(ARRAY['unknown=x'],NULL)", 2))
		run(fixture.Select("SELECT postgres_fdw_validator(a,b) IS NULL AS null_result FROM pf_local", []string{"null_result"}, []string{"BOOL"}, [][]any{{true}, {true}}))
		for _, q := range []string{
			postgresFDWCall([]string{"fetch_size=1", "fetch_size=2"}, 1417),
			postgresFDWCall([]string{"keep_connections", "async_capable"}, 1417),
			postgresFDWCall([]string{"table_name", "schema_name=a=b"}, 3118),
			postgresFDWCall([]string{"user", "password_required=true"}, 1418),
			"SELECT postgres_fdw_validator(ARRAY[['fetch_size=1','batch_size=2'],['fetch_size=3','batch_size=4']],1417::oid) AS validated",
			"SELECT postgres_fdw_validator('[0:1]={fetch_size=1,batch_size=2}'::text[],1417::oid) AS validated",
		} {
			run(fileFDWVoid(q))
		}
		run(fileFDWVoid("SELECT postgres_fdw_validator(ARRAY[$1::text],$2::oid) AS validated", "fetch_size=2", int64(1417)))
		for _, q := range []string{"SELECT postgres_fdw_validator(ARRAY[NULL]::text[],1417::oid)", "SELECT postgres_fdw_validator(ARRAY['unknown=x',NULL]::text[],1417::oid)"} {
			run(postgresFDWError(q, "22004", "null array element not allowed in this context"))
		}
		for _, name := range []string{"fetch_size", "batch_size", "fdw_startup_cost", "fdw_tuple_cost", "extensions", "analyze_sampling"} {
			run(postgresFDWError(postgresFDWCall([]string{name}, 1417), "42601", name+" requires a parameter"))
		}
		run(postgresFDWError(postgresFDWCall([]string{"keep_connections=yes", "unknown=x"}, 1417), "42601", "keep_connections requires a Boolean value"))
		run(postgresFDWInvalid(postgresFDWCall([]string{"unrelated_option_zzzzzz=x", "keep_connections=yes"}, 1417), "unrelated_option_zzzzzz", ""))
		run(postgresFDWError("SELECT postgres_fdw_handler()", "0A000", "cannot display a value of type fdw_handler"))
		for _, tc := range []struct{ q, signature string }{
			{"SELECT postgres_fdw_validator()", "postgres_fdw_validator()"},
			{"SELECT postgres_fdw_validator(1,2)", "postgres_fdw_validator(integer, integer)"},
			{"SELECT postgres_fdw_handler(1)", "postgres_fdw_handler(integer)"},
		} {
			d := fdwDiagnostic("42883", "function "+tc.signature+" does not exist")
			d.Position = fixture.Position(8)
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			s := fixture.ErrorDiagnostic(tc.q, d)
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		q := "SELECT postgres_fdw_validator(ARRAY[$1::text],1417::oid) AS validated"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, arg := range []string{"fetch_size=1", "fetch_size=2", "fetch_size=010"} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fileFDWVoid(q, arg))
		}
		run(fixture.Exec("CREATE SCHEMA pf_helpers", 0))
		run(fixture.Exec("ALTER FUNCTION postgres_fdw_validator(text[],oid) RENAME TO actual_validator", 0))
		run(fixture.Exec("ALTER FUNCTION actual_validator(text[],oid) SET SCHEMA pf_helpers", 0))
		run(fileFDWVoid("SELECT pf_helpers.actual_validator(ARRAY['fetch_size=1'],1417::oid) AS validated"))
		run(fixture.Exec("CREATE FUNCTION postgres_fdw_validator(text[],oid) RETURNS text LANGUAGE SQL AS 'SELECT ''shadow'''", 0))
		run(fixture.Select("SELECT postgres_fdw_validator(ARRAY['bogus=x'],1417::oid) AS shadow", []string{"shadow"}, []string{"TEXT"}, [][]any{{"shadow"}}))
		run(postgresFDWError("ALTER SERVER pf_server OPTIONS(ADD fetch_size '0')", "22023", `"fetch_size" must be an integer value greater than zero`))
	})
}
