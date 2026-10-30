package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWPushdownSafety(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwPushdownLive(t, p, b, r, run)
		for _, mode := range []string{"supported", "declined", "absent"} {
			if !b.PG {
				r.mu.Lock()
				r.queryPushdown = mode == "supported"
				r.mu.Unlock()
				if mode == "absent" {
					run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_scan_only_handler", 0))
				}
			}
			fdwPushdownValue(run, "SELECT l.k,count(*) AS n FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k GROUP BY l.k ORDER BY l.k LIMIT 1", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}}, false)
			fdwPushdownValue(run, "SELECT l.k,r.w FROM fdw_push_left l LEFT JOIN fdw_push_right r ON l.k=r.k AND l.v+0<0", []string{"k", "w"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(1), nil}, {int64(2), nil}, {nil, nil}, {int64(4), nil}}, true)
			fdwPushdownValue(run, "SELECT l.k,r.w FROM fdw_push_left l LEFT JOIN fdw_push_right r ON l.k=r.k WHERE l.v+0<0", []string{"k", "w"}, []string{"INT4", "INT4"}, nil, true)
			fdwPushdownValue(run, "SELECT k,count(*) AS n FROM fdw_push_left WHERE v+0 IS NOT NULL GROUP BY k", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(4), int64(1)}, {nil, int64(1)}}, true)
			fdwPushdownValue(run, "SELECT k FROM fdw_push_left GROUP BY k HAVING count(*)+0>1 ORDER BY k LIMIT 1", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}, false)
		}
		if !b.PG {
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_sql_handler", 0))
			r.mu.Lock()
			r.queryPushdown = true
			r.mu.Unlock()
		}
		run(fixture.Exec("CREATE SEQUENCE fdw_push_draw START 1", 0))
		volatileScans := 0
		r.setHook(func(ctx context.Context, method string) error {
			if method == "BeginForeignQueryScan" {
				volatileScans++
			}
			return ctx.Err()
		})
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				predicate string
				exactDraw bool
			}{
				{"l.k=r.k AND nextval('fdw_push_draw')<0", false},
				{"CASE WHEN l.k=r.k THEN nextval('fdw_push_draw')<0 ELSE false END", true},
			} {
				run(fixture.Exec("ALTER SEQUENCE fdw_push_draw RESTART WITH 1", 0))
				q := "SELECT l.k,r.w FROM fdw_push_left l LEFT JOIN fdw_push_right r ON " + tc.predicate
				s := fixture.Select(q, []string{"k", "w"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(1), nil}, {int64(2), nil}, {nil, nil}, {int64(4), nil}})
				s.Bag = true
				s.Prepared = prepared
				run(s)
				// AND operand demand is optimizer-dependent (PG18 §4.2.14).
				// CASE explicitly demands nextval for exactly four equal pairs.
				if tc.exactDraw {
					run(fixture.Select("SELECT nextval('fdw_push_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
				}
			}
		}
		r.setHook(nil)
		if !b.PG && volatileScans != 0 {
			t.Fatal("volatile ON was shipped", volatileScans)
		}
		run(fixture.Exec("CREATE FUNCTION fdw_push_false(integer) RETURNS boolean LANGUAGE SQL IMMUTABLE AS $$SELECT $1+0<0$$", 0))
		fdwPushdownValue(run, "SELECT l.k,r.w FROM fdw_push_left l LEFT JOIN fdw_push_right r ON l.k=r.k AND fdw_push_false(l.k)", []string{"k", "w"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(1), nil}, {int64(2), nil}, {nil, nil}, {int64(4), nil}}, true)
		run(fixture.Exec("CREATE VIEW fdw_push_barrier WITH(security_barrier=true) AS SELECT k,v FROM fdw_push_left WHERE v+0 IS NOT NULL", 0))
		fdwPushdownValue(run, "SELECT l.k,l.v,r.w FROM fdw_push_barrier l JOIN fdw_push_right r ON l.k=r.k", []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(100)}, {int64(1), int64(10), int64(100)}}, true)
		fdwSourceExec(t, p, b, r, run, `CREATE TABLE fdw_source.push_text(t text COLLATE "C")`, 0)
		fdwSourceExec(t, p, b, r, run, `INSERT INTO fdw_source.push_text VALUES('é'),('a'),('Z'),('a'),(NULL)`, 5)
		run(fixture.Exec(`CREATE FOREIGN TABLE fdw_push_text(t text COLLATE "C") SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'push_text')`, 0))
		for _, collation := range []string{"C", "POSIX"} {
			calls := 0
			r.setHook(func(ctx context.Context, method string) error {
				if method == "BeginForeignQueryScan" {
					calls++
				}
				return ctx.Err()
			})
			fdwPushdownValue(run, `SELECT t COLLATE "`+collation+`" AS t,count(*) AS n FROM fdw_push_text GROUP BY t COLLATE "`+collation+`" ORDER BY t COLLATE "`+collation+`" NULLS LAST`, []string{"t", "n"}, []string{"TEXT", "INT8"}, [][]any{{"Z", int64(1)}, {"a", int64(2)}, {"é", int64(1)}, {nil, int64(1)}}, false)
			r.setHook(nil)
			if !b.PG && (collation == "C" && calls == 0 || collation == "POSIX" && calls != 0) {
				t.Fatalf("collation %s selected composite scans=%d", collation, calls)
			}
		}
		fdwPushdownMappings(t, p, b, c, r, run)
		fdwPushdownBinding(run)
		fdwPushdownPermissions(t, p, b, c, r, run)
	})
}

func fdwPushdownPermissions(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE ROLE fdw_reader", 0))
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader", 0))
	run(fixture.Exec("GRANT SELECT ON fdw_push_left,fdw_push_right TO fdw_reader", 0))
	options := "user 'postgres'"
	if b.PG {
		options = "user 'stuntgres',password 'stuntgres',password_required 'false'"
	}
	run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_live OPTIONS("+options+")", 0))
	q := "SELECT l.k FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k ORDER BY r.w LIMIT 0"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	run(fixture.Exec("REVOKE SELECT ON fdw_push_right FROM fdw_reader", 0))
	run(fixture.Exec("GRANT SELECT(k) ON fdw_push_right TO fdw_reader", 0))
	run(fixture.Exec("SET ROLE fdw_reader", 0))
	before := r.snapshot()
	denied := fixture.ErrorDiagnostic(q, fdwDiagnostic("42501", "permission denied for foreign table fdw_push_right"))
	run(denied)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
	if !b.PG && r.snapshot() != before {
		t.Fatal("hidden-column authority denial entered provider")
	}
	for _, query := range []string{
		"SELECT sum(r.w) FROM fdw_push_left l LEFT JOIN fdw_push_right r ON false",
		"SELECT r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k WHERE false",
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(query, fdwDiagnostic("42501", "permission denied for foreign table fdw_push_right"))
			s.Prepared = prepared
			run(s)
		}
	}
	run(fixture.Exec("RESET ROLE", 0))
}
