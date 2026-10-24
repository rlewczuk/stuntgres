package reference_test

import (
	"database/sql"
	"slices"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceUserOperatorRoutineAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"userop_login_owner", "userop_login_reader"})
	fixture.SchemaReferenceCleanup(t, "userop_login_schema")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn) {
		defer func() {
			admin.ExecContext(p.Ctx, "RESET ROLE")
			admin.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_catalog.int4pl(int4,int4) TO PUBLIC")
		}()
		userOperatorExec(t, p, b, admin, "CREATE ROLE userop_login_owner LOGIN PASSWORD 'role-core-fixture'", "CREATE ROLE userop_login_reader LOGIN PASSWORD 'role-core-fixture'", "CREATE SCHEMA userop_login_schema AUTHORIZATION userop_login_owner", "GRANT USAGE ON SCHEMA userop_login_schema TO userop_login_reader")
		dsn := "reference/" + strings.TrimSuffix(t.Name(), "/"+b.Name)
		driver := p.NativeConnector.Driver().(*stuntgres.Driver)
		ownerDB := fixture.RoleScenarioLoginPool(t, driver, b.PG, dsn, "userop_login_owner")
		readerDB := fixture.RoleScenarioLoginPool(t, driver, b.PG, dsn, "userop_login_reader")
		owner, err := ownerDB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		reader, err := readerDB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		// Login pools are raw connections, not the pair's middleware-wrapped
		// executors. Keep all row/diagnostic assertions without requesting a
		// recording that these connections cannot produce.
		raw := b
		raw.Recorder = nil
		userOperatorExec(t, p, raw, owner, "CREATE OPERATOR userop_login_schema.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)")
		query := "SELECT $1::int4 OPERATOR(userop_login_schema.#~#) $2::int4 AS v"
		stmt, err := reader.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: query}
		want := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, 2, 3)
		userOperatorRawRows(t, p, b, held, want)
		userOperatorExec(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.int4pl(int4,int4) FROM PUBLIC")
		denied := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function int4pl"), 2, 3)
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, nil, denied)
		userOperatorRawRows(t, p, b, reader, fixture.Select("SELECT NULL::int4 OPERATOR(userop_login_schema.#~#) 3 AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
		userOperatorRawRows(t, p, b, reader, fixture.Select("SELECT CASE WHEN false THEN 2 OPERATOR(userop_login_schema.#~#) 3 ELSE 42 END AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		userOperatorError(t, p, raw, owner, "CREATE OPERATOR userop_login_schema.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)", "42501", "permission denied for function int4pl", "", "", "", false)
		userOperatorExec(t, p, b, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.int4pl(int4,int4) TO PUBLIC")
		userOperatorRawRows(t, p, b, held, want)
		userOperatorError(t, p, raw, reader, "ALTER OPERATOR userop_login_schema.#~# (int4,int4) SET (RESTRICT=NONE)", "42501", "must be owner of operator #~#", "", "", "", false)
		userOperatorExec(t, p, b, admin, "REVOKE USAGE ON SCHEMA userop_login_schema FROM userop_login_reader")
		userOperatorError(t, p, raw, reader, "SELECT 2 OPERATOR(userop_login_schema.#~#) 3", "42501", "permission denied for schema userop_login_schema", "", "", "10", true)
		userOperatorExec(t, p, raw, reader, "SET search_path TO userop_login_schema,public")
		userOperatorError(t, p, raw, reader, "SELECT 2 #~# 3", "42883", "operator does not exist: integer #~# integer", "", "No operator matches the given name and argument types. You might need to add explicit type casts.", "10", true)
	})
}
func TestReferenceUserOperatorTransactions(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)")
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{"CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)", "ALTER OPERATOR public.#~# (int4,int4) SET (RESTRICT=NONE)", "DROP OPERATOR public.#~# (int4,int4)"} {
				userOperatorExec(t, p, b, c, "BEGIN")
				step := fixture.Exec(query, 0)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				if query == "CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)" {
					userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#?#) 3 AS v", "INT4", int64(-1))
				} else if query == "ALTER OPERATOR public.#~# (int4,int4) SET (RESTRICT=NONE)" {
					userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", int64(5))
				} else {
					userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#~#'", "INT8", int64(0))
				}
				userOperatorExec(t, p, b, c, "ROLLBACK")
				userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", int64(5))
				userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#?#'", "INT8", int64(0))
			}
			for _, tc := range []struct{ q, tag string }{{"CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)", "CREATE OPERATOR"}, {"ALTER OPERATOR public.#~# (int4,int4) SET (RESTRICT=NONE)", "ALTER OPERATOR"}, {"DROP OPERATOR public.#~# (int4,int4)", "DROP OPERATOR"}} {
				userOperatorExec(t, p, b, c, "BEGIN READ ONLY")
				step := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+tc.tag+" in a read-only transaction"))
				step.Prepared = prepared
				step.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				userOperatorExec(t, p, b, c, "ROLLBACK")
			}
		}
	})
}
func TestReferenceUserOperatorEdgeDefinitions(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi,FUNCTION=int4pl)",
			"CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,SORT1=OPERATOR(pg_catalog.<))",
			"CREATE OPERATOR public.#/# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)",
			"CREATE VIEW userop_edge_view AS SELECT (2 OPERATOR(public.#~#) (3*4)) AS v",
			"CREATE DOMAIN userop_edge_domain AS integer",
			"CREATE TABLE userop_edge_domain_input(v userop_edge_domain)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_edge_domain_input VALUES(2)", 1))
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT oprcanmerge AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT v FROM userop_edge_view", "INT4", int64(14))
		userOperatorValue(t, p, b, c, "SELECT v OPERATOR(public.#~#) 3 AS v FROM userop_edge_domain_input", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT 2 != 3 AS v", "BOOL", true)
		userOperatorExec(t, p, b, c, "DROP OPERATOR public.#~# (int4,int4),public.#/# (int4,int4) CASCADE")
		userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname IN ('#~#','#/#')", "INT8", int64(0))
		userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='userop_edge_view'", "INT8", int64(0))
	})
}

// Authenticated login pools deliberately bypass recording middleware. Assert
// their public metadata, raw transport values, rows and closure directly.
func userOperatorRawRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, step fixture.Step) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, step.Query, step.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !slices.Equal(columns, step.Columns) {
		t.Fatal("raw login columns", columns, step.Columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(step.Types) {
		t.Fatal("raw login types", types, step.Types, err)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != step.Types[i] {
			t.Fatal("raw login type", typ.DatabaseTypeName(), step.Types[i])
		}
	}
	values := make([][]any, 0)
	for rows.Next() {
		row, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range dest {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			fixture.AssertRawValue(t, b.PG, step.Types[i], value)
			row[i] = fixture.Normalize(value)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, matches := fixture.CompareRows(values, step.Rows, step.Bag); !matches {
		t.Fatal("raw login rows", values, step.Rows)
	}
}
