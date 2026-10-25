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

// Expectations come from REL_18_0 system_functions.sql, not the native
// implementation: genuine SQL invoker bodies delegate to callable primitives.
// A table column prevents a constant-only witness from hiding executable-body
// admission behind folding. Each dependency is revoked independently.
type objectGrantBodyCase struct {
	name, columns, values                      string
	expression, nullExpression, deadExpression string
	resultType                                 string
	value                                      any
	nullValue                                  any
	target, deniedName                         string
	primitive, primitiveType                   string
	primitiveValue                             any
	independent, independentType               string
	independentValue                           any
	wrapperContext                             string
	zeroRowsPruned                             bool
}

func objectGrantBodyConsumer(t *testing.T, tc objectGrantBodyCase) {
	t.Helper()
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{tc.target})
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_body_source("+tc.columns+")", 0))
		run(fixture.Exec("INSERT INTO og_body_source VALUES("+tc.values+")", 1))
		run(fixture.Exec("GRANT SELECT ON og_body_source TO og_object_reader", 0))
		run(fixture.Exec("ALTER ROLE og_object_reader LOGIN", 0))
		if b.PG {
			if _, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE og_object_reader PASSWORD 'role-core-fixture'"); err != nil {
				t.Fatal(err)
			}
		}
		query := "SELECT " + tc.expression + " AS v FROM og_body_source"
		want := fixture.Select(query, []string{"v"}, []string{tc.resultType}, [][]any{{tc.value}})
		// Prepare and execute as the actual invoker before changing its rights.
		// The held statement does not depend on the route: only the prepared
		// route holds it, and each held run follows the same fresh step.
		run(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
		var held fixture.HeldStatement
		heldRun := func(s fixture.Step) {
			t.Helper()
			if prepared {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		if prepared {
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		}
		heldRun(want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		fresh := func(step fixture.Step) {
			t.Helper()
			// A new authenticated physical backend cannot inherit a prior SQL
			// inlining/relcache decision. It still uses the common recorder.
			dsn := "reference/" + strings.TrimSuffix(t.Name(), "/"+b.Name)
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
			wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector}, fixture.Interceptor{})
			configured, err := wrapped.(driver.DriverContext).OpenConnector("")
			if err != nil {
				t.Fatal(err)
			}
			pool := sql.OpenDB(configured)
			defer pool.Close()
			conn, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, &fixture.Recorder{RawBackend: b.Recorder.RawBackend}, step)
		}
		deniedStep := func(q string) fixture.Step {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.deniedName})
			s.SelectRows = true
			s.ErrorStage = "execute"
			return s
		}
		wrapperDenied := func(q string) fixture.Step {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.deniedName, Context: tc.wrapperContext})
			s.SelectRows = true
			s.ErrorStage = "execute"
			return s
		}
		denied := func() {
			t.Helper()
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(wrapperDenied(query))
				heldRun(wrapperDenied(query))
				if tc.zeroRowsPruned {
					// A non-inlined SQL body is not entered without a row.
					run(fixture.Select(query+" WHERE FALSE", []string{"v"}, []string{tc.resultType}, nil))
				} else {
					// Inlined surviving calls still initialize with no rows.
					run(wrapperDenied(query + " WHERE FALSE"))
				}
				// The denied body primitive/operator also protects its own public
				// invocation; a different overload/primitive remains independent.
				run(deniedStep("SELECT " + tc.primitive + " AS v FROM og_body_source"))
				run(fixture.Select("SELECT "+tc.independent+" AS v FROM og_body_source", []string{"v"}, []string{tc.independentType}, [][]any{{tc.independentValue}}))
				if tc.nullExpression != "" {
					run(fixture.Select("SELECT "+tc.nullExpression+" AS v", []string{"v"}, []string{tc.resultType}, [][]any{{tc.nullValue}}))
				}
				if tc.deadExpression != "" {
					run(fixture.Select("SELECT "+tc.deadExpression+" AS v FROM og_body_source", []string{"v"}, []string{tc.resultType}, [][]any{{tc.value}}))
				}
			})
			fresh(wrapperDenied(query))
		}
		allowed := func() {
			t.Helper()
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(want)
				heldRun(want)
				run(fixture.Select(query+" WHERE FALSE", []string{"v"}, []string{tc.resultType}, nil))
				run(fixture.Select("SELECT "+tc.primitive+" AS v FROM og_body_source", []string{"v"}, []string{tc.primitiveType}, [][]any{{tc.primitiveValue}}))
			})
			fresh(want)
		}
		allowed()
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM PUBLIC", 0))
		denied()
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.target+" TO og_object_reader", 0))
		allowed()
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM og_object_reader", 0))
		denied()
		// Reading stored input never invokes the denied wrapper dependency.
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT count(*) AS n FROM og_body_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	})
}

func TestReferenceObjectGrantRoutineBodyBitLength(t *testing.T) {
	for _, tc := range []objectGrantBodyCase{
		{name: "text_octets", columns: "s TEXT,b BYTEA", values: "'é','\\x00ff'::bytea", expression: "bit_length(s)", nullExpression: "bit_length(NULL::TEXT)", deadExpression: "CASE WHEN FALSE THEN bit_length(s) ELSE 16 END", resultType: "INT4", value: int64(16), target: "pg_catalog.octet_length(text)", deniedName: "octet_length", primitive: "octet_length(s)", primitiveType: "INT4", primitiveValue: int64(2), independent: "bit_length(b)", independentType: "INT4", independentValue: int64(16)},
		{name: "bytea_octets", columns: "s TEXT,b BYTEA", values: "'é','\\x00ff'::bytea", expression: "bit_length(b)", nullExpression: "bit_length(NULL::BYTEA)", deadExpression: "CASE WHEN FALSE THEN bit_length(b) ELSE 16 END", resultType: "INT4", value: int64(16), target: "pg_catalog.octet_length(bytea)", deniedName: "octet_length", primitive: "octet_length(b)", primitiveType: "INT4", primitiveValue: int64(2), independent: "bit_length(s)", independentType: "INT4", independentValue: int64(16)},
		{name: "text_multiply", columns: "s TEXT,b BYTEA,n INT", values: "'é','\\x00ff'::bytea,3", expression: "bit_length(s)", nullExpression: "bit_length(NULL::TEXT)", deadExpression: "CASE WHEN FALSE THEN bit_length(s) ELSE 16 END", resultType: "INT4", value: int64(16), target: "pg_catalog.int4mul(integer,integer)", deniedName: "int4mul", primitive: "n * 8", primitiveType: "INT4", primitiveValue: int64(24), independent: "octet_length(s)", independentType: "INT4", independentValue: int64(2)},
		{name: "bytea_multiply", columns: "s TEXT,b BYTEA,n INT", values: "'é','\\x00ff'::bytea,3", expression: "bit_length(b)", nullExpression: "bit_length(NULL::BYTEA)", deadExpression: "CASE WHEN FALSE THEN bit_length(b) ELSE 16 END", resultType: "INT4", value: int64(16), target: "pg_catalog.int4mul(integer,integer)", deniedName: "int4mul", primitive: "n * 8", primitiveType: "INT4", primitiveValue: int64(24), independent: "octet_length(b)", independentType: "INT4", independentValue: int64(2)},
	} {
		t.Run(tc.name, func(t *testing.T) { objectGrantBodyConsumer(t, tc) })
	}
}

func TestReferenceObjectGrantRoutineBodyPadding(t *testing.T) {
	for _, name := range []string{"lpad", "rpad"} {
		value, custom := "  xy", ".xy"
		if name == "rpad" {
			value, custom = "xy  ", "xy."
		}
		t.Run(name, func(t *testing.T) {
			objectGrantBodyConsumer(t, objectGrantBodyCase{name: name, columns: "s TEXT,n INT", values: "'xy',4", expression: name + "(s,n)", nullExpression: name + "(NULL::TEXT,4)", deadExpression: "CASE WHEN FALSE THEN " + name + "(s,n) ELSE '" + value + "' END", resultType: "TEXT", value: value, target: "pg_catalog." + name + "(text,integer,text)", deniedName: name, primitive: name + "(s,3,'.')", primitiveType: "TEXT", primitiveValue: custom, independent: "length(s)", independentType: "INT4", independentValue: int64(2)})
		})
	}
}

func TestReferenceObjectGrantRoutineBodyNumeric(t *testing.T) {
	for _, name := range []string{"round", "trunc"} {
		value, primitive := "-13", "-12.6"
		if name == "trunc" {
			value, primitive = "-12", "-12.5"
		}
		t.Run(name, func(t *testing.T) {
			objectGrantBodyConsumer(t, objectGrantBodyCase{name: name, columns: "n NUMERIC", values: "-12.56", expression: name + "(n)", nullExpression: name + "(NULL::NUMERIC)", deadExpression: "CASE WHEN FALSE THEN " + name + "(n) ELSE " + value + "::NUMERIC END", resultType: "NUMERIC", value: value, target: "pg_catalog." + name + "(numeric,integer)", deniedName: name, primitive: name + "(n,1)", primitiveType: "NUMERIC", primitiveValue: primitive, independent: "n", independentType: "NUMERIC", independentValue: "-12.56"})
		})
	}
}
