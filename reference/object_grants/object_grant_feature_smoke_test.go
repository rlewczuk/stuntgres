package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"net/url"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantFeatureSmoke(t *testing.T) {
	objectGrantScenario(t, []string{"og_smoke"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)", "pg_catalog.sum(integer)", "pg_catalog.row_number()", "pg_catalog.first_value(anyelement)", "pg_catalog.int4pl(integer,integer)", "pg_catalog.int4eq(integer,integer)", "pg_catalog.json_array_length(json)", "pg_catalog.quote_literal(text)"})
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "REVOKE ALL ON LANGUAGE plpgsql FROM og_object_reader CASCADE", "REVOKE ALL ON PARAMETER lc_messages FROM og_object_reader,og_object_other CASCADE", "REVOKE ALL ON TABLESPACE pg_default FROM og_object_reader,og_object_other CASCADE"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error("feature smoke ACL cleanup", q, err)
				}
			}
		}()
		run(fixture.Exec("CREATE SCHEMA og_smoke", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_smoke TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("CREATE SEQUENCE og_smoke.s START 10", 0))
		run(fixture.Exec("GRANT USAGE ON SEQUENCE og_smoke.s TO og_object_reader WITH GRANT OPTION", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT nextval('og_smoke.s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(10)}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT setval('og_smoke.s',20)", "sequence", "s")
			run(fixture.Exec("GRANT USAGE ON SEQUENCE og_smoke.s TO og_object_other", 0))
		})
		run(fixture.Exec("REVOKE GRANT OPTION FOR USAGE ON SEQUENCE og_smoke.s FROM og_object_reader CASCADE", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() { fixture.RoleGrantPermissionDenied(run, "SELECT nextval('og_smoke.s')", "sequence", "s") })
		run(fixture.Exec("GRANT ALL ON ALL SEQUENCES IN SCHEMA og_smoke TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT setval('og_smoke.s',20) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(20)}}))
		})
		run(fixture.Exec("CREATE TABLE og_smoke.t(n INT,j JSON)", 0))
		run(fixture.Exec("INSERT INTO og_smoke.t VALUES(1,'[1,2]')", 1))
		run(fixture.Exec("GRANT SELECT,INSERT ON og_smoke.t TO og_object_reader", 0))
		abs := "SELECT abs($1::integer) AS v"
		stmt, err := c.PrepareContext(p.Ctx, abs)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: abs}
		absWant := fixture.Select(abs, []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}, int64(-3))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, absWant) })
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			s := fixture.ImplicitError(abs, fixture.Diagnostic{Code: "42501", Message: "permission denied for function abs"}, int64(-3))
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, absWant) })
		for _, target := range []string{"sum(integer)", "row_number()", "first_value(anyelement)", "int4pl(integer,integer)", "int4eq(integer,integer)"} {
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+target+" FROM PUBLIC", 0))
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT sum(n) FROM og_smoke.t", "aggregate", "sum")
			fixture.RoleGrantPermissionDenied(run, "SELECT row_number() OVER() FROM og_smoke.t", "function", "row_number")
			fixture.RoleGrantPermissionDenied(run, "SELECT first_value(n) OVER() FROM og_smoke.t", "function", "first_value")
			fixture.RoleGrantPermissionDenied(run, "SELECT n+1 FROM og_smoke.t WHERE FALSE", "function", "int4pl")
			fixture.RoleGrantPermissionDenied(run, "SELECT DISTINCT n FROM og_smoke.t", "function", "int4eq")
			run(fixture.Select("SELECT n FROM og_smoke.t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
		for _, target := range []string{"sum(integer)", "row_number()", "first_value(anyelement)", "int4pl(integer,integer)", "int4eq(integer,integer)"} {
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+target+" TO og_object_reader", 0))
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT sum(n) AS v FROM og_smoke.t", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT row_number() OVER() AS r,first_value(n) OVER() AS f,n+1 AS a FROM og_smoke.t", []string{"r", "f", "a"}, []string{"INT8", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(2)}}))
			run(fixture.Select("SELECT generate_series(1::integer,2::integer) AS v ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
		quote := "SELECT quote_literal(1::integer) AS v"
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.quote_literal(text) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			s := fixture.ImplicitError(quote, fixture.Diagnostic{Code: "42501", Message: "permission denied for function quote_literal", Context: `SQL function "quote_literal" during inlining`})
			s.SelectRows = true
			run(s)
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.quote_literal(text) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Select(quote, []string{"v"}, []string{"TEXT"}, [][]any{{"'1'"}})) })
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA og_smoke", 0))
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA og_smoke", 0))
		for _, target := range []string{"digest(text,text)", "daitch_mokotoff(text)"} {
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION og_smoke."+target+" FROM PUBLIC", 0))
		}
		digest := "SELECT encode(og_smoke.digest('abc','md5'),'hex') AS v"
		fuzzy := "SELECT og_smoke.daitch_mokotoff('George')::text AS v"
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, digest, "function", "digest")
			fixture.RoleGrantPermissionDenied(run, fuzzy, "function", "daitch_mokotoff")
		})
		run(fixture.Exec("GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA og_smoke TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(digest, []string{"v"}, []string{"TEXT"}, [][]any{{"900150983cd24fb0d6963f7d28e17f72"}}))
			run(fixture.Select(fuzzy, []string{"v"}, []string{"TEXT"}, [][]any{{"{595000}"}}))
		})
		run(fixture.Exec("CREATE VIEW og_smoke.v AS SELECT json_array_length(j) AS len FROM og_smoke.t", 0))
		run(fixture.Exec("GRANT SELECT ON og_smoke.v TO og_object_reader", 0))
		run(fixture.Exec("CREATE INDEX og_smoke_length ON og_smoke.t(json_array_length(j))", 0))
		viewSQL := "SELECT len FROM og_smoke.v"
		viewStmt, err := c.PrepareContext(p.Ctx, viewSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer viewStmt.Close()
		viewHeld := fixture.HeldStatement{Executor: c, Statement: viewStmt, SQL: viewSQL}
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			s := fixture.ImplicitError(viewSQL, fixture.Diagnostic{Code: "42501", Message: "permission denied for function json_array_length"})
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, s)
			fixture.RoleGrantPermissionDenied(run, "INSERT INTO og_smoke.t VALUES(2,'[1]')", "function", "json_array_length")
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("INSERT INTO og_smoke.t VALUES(2,'[1]')", 1))
			s := fixture.Select(viewSQL, []string{"len"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}})
			s.Bag = true
			fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, s)
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantError(run, "SET lc_messages='C'", "42501", `permission denied to set parameter "lc_messages"`)
		})
		run(fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER lc_messages TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantError(run, "SET lc_messages='C'", "42501", `permission denied to set parameter "lc_messages"`)
		})
		run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_reader WITH GRANT OPTION", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("SET lc_messages='C'", 0))
			run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO og_object_other", 0))
			run(fixture.Exec("SET application_name='object-grant-smoke'", 0))
		})
		run(fixture.Exec("REVOKE GRANT OPTION FOR SET ON PARAMETER lc_messages FROM og_object_reader CASCADE", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() {
			fixture.RoleGrantError(run, "SET lc_messages='C'", "42501", `permission denied to set parameter "lc_messages"`)
		})
		run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_default TO og_object_reader WITH GRANT OPTION", 0))
		run(fixture.Select("SELECT spcacl IS NOT NULL AS explicit FROM pg_tablespace WHERE spcname='pg_default'", []string{"explicit"}, []string{"BOOL"}, [][]any{{true}}))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_default TO og_object_other", 0)) })
		run(fixture.Exec("REVOKE GRANT OPTION FOR CREATE ON TABLESPACE pg_default FROM og_object_reader CASCADE", 0))
		objectGrantSmokeDatabase(t, p, b, c, run, prepared)
		if !b.PG {
			var writer objectGrantWriteProbe
			err := stuntgres.Dump(p.Ctx, p.Native, &writer)
			if err == nil || writer.calls != 0 {
				t.Fatal("explicit ACL Dump must reject before writer callbacks", err, writer.calls)
			}
			switch err.Error() {
			case "stuntgres: dump cannot serialize altered builtin routine authority",
				"stuntgres: dump cannot serialize explicit routine ACL",
				"stuntgres: dump cannot serialize explicit namespace ACL",
				"stuntgres: dump cannot serialize explicit relation or column ACL",
				"stuntgres: dump cannot serialize parameter ACL",
				"stuntgres: dump cannot serialize altered tablespace authority":
				// Owning descriptor traversal does not promise an offender order.
			default:
				t.Fatal("unexpected Dump failure instead of explicit ACL boundary", err)
			}
			q := "ALTER SYSTEM SET lc_messages='C'"
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "SYSTEM"`, Position: "7"})
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		run(fixture.Exec("GRANT USAGE ON LANGUAGE plpgsql TO og_object_reader", 0))
	})
}

func objectGrantSmokeDatabase(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
	t.Helper()
	var database string
	if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	public := []string{"CONNECT", "TEMPORARY"}
	if b.PG {
		public = objectGrantDatabasePublic(t, p.Ctx, c)
	}
	defer objectGrantDatabaseCleanup(t, p.Ctx, b.DB, database, []string{"og_object_reader", "og_object_other"}, public)
	quoted := pq.QuoteIdentifier(database)
	run(fixture.Exec("ALTER ROLE og_object_reader LOGIN", 0))
	if b.PG {
		if _, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE og_object_reader PASSWORD 'role-core-fixture'"); err != nil {
			t.Fatal(err)
		}
	}
	run(fixture.Exec("GRANT CONNECT ON DATABASE "+quoted+" TO og_object_reader", 0))
	run(fixture.Exec("REVOKE TEMPORARY ON DATABASE "+quoted+" FROM PUBLIC", 0))
	dsn := "reference/" + strings.TrimSuffix(t.Name(), "/"+b.Name)
	// Match the existing authenticated role fixtures: this borrowed connection
	// owns middleware result capture, while fresh Ping pools remain unwrapped.
	var connector driver.Connector
	var err error
	if b.PG {
		u, err := url.Parse(fixture.DSN)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword("og_object_reader", "role-core-fixture")
		connector, err = pq.NewConnector(u.String())
		if err != nil {
			t.Fatal(err)
		}
	} else {
		connector, err = p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, "og_object_reader")
		if err != nil {
			t.Fatal(err)
		}
	}
	wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{connector}, fixture.Interceptor{})
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	pool := sql.OpenDB(configured)
	defer pool.Close()
	admitted, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admitted.Close()
	authenticatedRecord := &fixture.Recorder{RawBackend: b.Recorder.RawBackend}
	use := func(step fixture.Step) {
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, admitted, authenticatedRecord, step)
	}
	denied := fixture.ImplicitError("CREATE TEMP TABLE og_smoke_lazy(n INT)", fixture.Diagnostic{Code: "42501", Message: `permission denied to create temporary tables in database "` + database + `"`, Position: "19"})
	denied.ErrorStage = "execute"
	use(denied)
	run(fixture.Exec("GRANT TEMPORARY ON DATABASE "+quoted+" TO og_object_reader", 0))
	use(fixture.Exec("CREATE TEMP TABLE og_smoke_temp(n INT)", 0))
	use(fixture.Exec("INSERT INTO og_smoke_temp VALUES(7)", 1))
	var namespace string
	if err := admitted.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.relname='og_smoke_temp'").Scan(&namespace); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(namespace, "pg_temp_") {
		t.Fatal("actual own TEMP namespace", namespace)
	}
	run(fixture.Exec("REVOKE TEMPORARY ON DATABASE "+quoted+" FROM og_object_reader", 0))
	denied = fixture.ImplicitError("CREATE TEMP TABLE og_smoke_allocated_denied(n INT)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema " + namespace, Position: "19"})
	denied.ErrorStage = "execute"
	use(denied)
	use(fixture.Select("SELECT n FROM og_smoke_temp", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
	run(fixture.Exec("GRANT TEMPORARY ON DATABASE "+quoted+" TO og_object_reader", 0))
	use(fixture.Exec("CREATE TEMP SEQUENCE og_smoke_temp_seq START 20", 0))
	use(fixture.Select("SELECT nextval('og_smoke_temp_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(20)}}))
	run(fixture.Exec("REVOKE CONNECT ON DATABASE "+quoted+" FROM PUBLIC,og_object_reader", 0))
	use(fixture.Select("SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	fresh := func(allowed bool) {
		db := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, dsn, "og_object_reader")
		defer db.Close()
		err := db.PingContext(p.Ctx)
		if allowed {
			if err != nil {
				t.Fatal("fresh authenticated CONNECT regrant", err)
			}
			return
		}
		want := fixture.ControlsReferenceDiagnostic("42501", `permission denied for database "`+database+`"`)
		want.Detail = "User does not have CONNECT privilege."
		if b.PG {
			want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
		}
		fixture.AssertDiagnostic(t, err, &want)
	}
	fresh(false)
	run(fixture.Exec("GRANT CONNECT ON DATABASE "+quoted+" TO og_object_reader", 0))
	fresh(true)
}
