package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwPushdownBinding(run func(fixture.Step)) {
	for _, tc := range []struct{ query, code, message, token string }{
		{"SELECT missing FROM fdw_push_left", "42703", `column "missing" does not exist`, "missing"},
		{"SELECT k FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k", "42702", `column reference "k" is ambiguous`, "k"},
		{"SELECT k,count(*) FROM fdw_push_left", "42803", `column "fdw_push_left.k" must appear in the GROUP BY clause or be used in an aggregate function`, "k"},
		{"SELECT k FROM fdw_push_left LIMIT true", "42804", "argument of LIMIT must be type bigint, not type boolean", "true"},
	} {
		want := fdwDiagnostic(tc.code, tc.message)
		want.Position = strconv.Itoa(strings.Index(tc.query, tc.token) + 1)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(tc.query, want)
			s.Prepared = prepared
			run(s)
		}
	}
}

func fdwPushdownMappings(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	server := "CREATE SERVER fdw_second FOREIGN DATA WRAPPER fdw_native"
	mapping := "CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_second"
	if b.PG {
		server = "CREATE SERVER fdw_second FOREIGN DATA WRAPPER postgres_fdw OPTIONS(host '127.0.0.1',port '5432',dbname 'stuntgres_reference')"
		mapping += " OPTIONS(user 'stuntgres',password 'stuntgres')"
	}
	run(fixture.Exec(server, 0))
	run(fixture.Exec(mapping, 0))
	run(fixture.Exec(`CREATE FOREIGN TABLE fdw_push_other(k integer OPTIONS(column_name 'Key'),w integer OPTIONS(column_name 'Weight')) SERVER fdw_second OPTIONS(schema_name 'fdw_source',table_name 'Right input')`, 0))
	selected := 0
	r.setHook(func(ctx context.Context, method string) error {
		if method == "BeginForeignQueryScan" {
			selected++
		}
		return ctx.Err()
	})
	fdwPushdownValue(run, "SELECT count(*) FROM fdw_push_left l JOIN fdw_push_other r ON l.k=r.k", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}, false)
	r.setHook(nil)
	if !b.PG && selected != 0 {
		t.Fatal("different server/mapping absorbed into composite scan")
	}
	fdwSourceExec(t, p, b, r, run, "CREATE ROLE fdw_other LOGIN PASSWORD 'fdw-other'", 0)
	fdwSourceExec(t, p, b, r, run, "GRANT USAGE ON SCHEMA fdw_source TO fdw_other", 0)
	fdwSourceExec(t, p, b, r, run, `GRANT SELECT ON fdw_source."Left input" TO fdw_other`, 0)
	fdwSourceExec(t, p, b, r, run, `ALTER TABLE fdw_source."Left input" ENABLE ROW LEVEL SECURITY`, 0)
	fdwSourceExec(t, p, b, r, run, `CREATE POLICY push_visible ON fdw_source."Left input" USING("Key"=2)`, 0)
	query := "SELECT count(*) FROM fdw_push_left"
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
	change := "ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(ADD user 'fdw_other')"
	if b.PG {
		change = "ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(SET user 'fdw_other',SET password 'fdw-other')"
	}
	run(fixture.Exec(change, 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	fdwPushdownValue(run, query, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, false)
	fdwSourceExec(t, p, b, r, run, `REVOKE SELECT ON fdw_source."Left input" FROM fdw_other`, 0)
	fdwSourceExec(t, p, b, r, run, `GRANT SELECT("Key") ON fdw_source."Left input" TO fdw_other`, 0)
	fdwPushdownValue(run, "SELECT k FROM fdw_push_left ORDER BY k LIMIT 1", []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}}, false)
	fdwSourceExec(t, p, b, r, run, `REVOKE SELECT("Key") ON fdw_source."Left input" FROM fdw_other`, 0)
	denied := fixture.ErrorDiagnostic(query, fdwDiagnostic("42501", `permission denied for table Left input`))
	pg := fdwDiagnostic("42501", `permission denied for table Left input`)
	pg.Context = `remote SQL command: SELECT count(*) FROM fdw_source."Left input"`
	denied.PostgresDiagnostic = &pg
	run(denied)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
	fdwSourceExec(t, p, b, r, run, `GRANT SELECT ON fdw_source."Left input" TO fdw_other`, 0)
	change = "ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(SET user 'postgres')"
	if b.PG {
		change = "ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(SET user 'stuntgres',SET password 'stuntgres')"
	}
	run(fixture.Exec(change, 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
}
