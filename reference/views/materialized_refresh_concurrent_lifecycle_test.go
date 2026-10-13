package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

const concurrentRefreshSQL = "REFRESH MATERIALIZED VIEW CONCURRENTLY concurrent_mv"

func concurrentSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE concurrent_base(i INT,s TEXT)", 0))
	run(fixture.Exec("INSERT INTO concurrent_base VALUES(1,'old'),(2,NULL)", 2))
	run(fixture.Exec("CREATE MATERIALIZED VIEW concurrent_mv AS SELECT i,s FROM concurrent_base", 2))
}
func concurrentIndex(run func(fixture.Step)) {
	run(fixture.Exec("CREATE UNIQUE INDEX concurrent_u ON concurrent_mv(i) INCLUDE(s)", 0))
}
func concurrentChange(run func(fixture.Step)) {
	run(fixture.Exec("UPDATE concurrent_base SET s='new' WHERE i=1", 1))
	run(fixture.Exec("DELETE FROM concurrent_base WHERE i=2", 1))
	run(fixture.Exec("INSERT INTO concurrent_base VALUES(3,'three')", 1))
}
func concurrentOld() fixture.Step {
	return refreshBag("concurrent_mv", [][]any{{int64(1), "old"}, {int64(2), nil}})
}
func concurrentNew() fixture.Step {
	return refreshBag("concurrent_mv", [][]any{{int64(1), "new"}, {int64(3), "three"}})
}
func concurrentNoIndex(name string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("55000", `cannot refresh materialized view "public.`+name+`" concurrently`)
	d.Hint = "Create a unique index with no WHERE clause on one or more columns of the materialized view."
	return d
}

func TestReferenceMaterializedRefreshConcurrentLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			concurrentSeed(run)
			concurrentIndex(run)
			concurrentChange(run)
			run(concurrentOld())
			for _, suffix := range []string{"", " WITH DATA", ""} {
				run(fixture.Exec(concurrentRefreshSQL+suffix, 0))
				run(concurrentNew())
				run(fixture.Prepared(concurrentNew()))
			}
			fixture.TableDDLMetadata(t, p.Ctx, conn, b.PG, true, "SELECT i,s FROM concurrent_mv", []string{"i", "s"}, []string{"INT4", "TEXT"}, []bool{true, true}, 2)
			fixture.MaterializedViewPopulation(run, "concurrent_mv", true)
			run(fixture.Exec("DELETE FROM concurrent_base", 2))
			run(fixture.Exec(concurrentRefreshSQL, 0))
			run(refreshBag("concurrent_mv", nil))
			fixture.MaterializedViewPopulation(run, "concurrent_mv", true)
			fixture.TableDDLMetadata(t, p.Ctx, conn, b.PG, true, "SELECT i,s FROM concurrent_mv", []string{"i", "s"}, []string{"INT4", "TEXT"}, []bool{true, true}, 0)
		})
	}
}

func TestReferenceMaterializedRefreshConcurrentPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			concurrentSeed(run)
			for _, plain := range []bool{false, true} {
				var stmt *sql.Stmt
				var err error
				if plain {
					stmt, err = b.DB.Prepare(concurrentRefreshSQL)
				} else {
					stmt, err = conn.PrepareContext(p.Ctx, concurrentRefreshSQL)
				}
				if err != nil {
					t.Fatal("syntax-only prepare", err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: concurrentRefreshSQL}
				execute := func(s fixture.Step) {
					if plain {
						// Conn has no plain methods. Exercise the actual retained Stmt.Exec,
						// then keep its Context execution as the separately recorded companion.
						result, err := stmt.Exec()
						fixture.State(t, err, s.State)
						if err != nil {
							if result != nil {
								t.Fatal("failed plain Stmt.Exec exposed result")
							}
							fixture.AssertDiagnostic(t, err, s.Diagnostic)
						} else {
							if result == nil {
								t.Fatal("plain Stmt.Exec omitted result")
							}
							n, err := result.RowsAffected()
							if err != nil || n != s.Affected {
								t.Fatal("plain Stmt.Exec count", n, err)
							}
							if _, err := result.LastInsertId(); err == nil {
								t.Fatal("plain Stmt.Exec exposed LastInsertId")
							}
						}
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				fail := fixture.ErrorDiagnostic(concurrentRefreshSQL, concurrentNoIndex("concurrent_mv"))
				fail.ErrorStage = "execute"
				execute(fail)
				concurrentIndex(run)
				concurrentChangeOnce := plain == false
				if concurrentChangeOnce {
					concurrentChange(run)
				}
				ok := fixture.Exec(concurrentRefreshSQL, 0)
				execute(ok)
				run(concurrentNew())
				run(fixture.Exec("DROP INDEX concurrent_u", 0))
				execute(fail)
				concurrentIndex(run)
				execute(ok)
				run(concurrentNew())
				run(fixture.Exec("DROP INDEX concurrent_u", 0))
			}
			for _, target := range []string{"missing", "concurrent_mv"} {
				stmt, err := conn.PrepareContext(p.Ctx, "REFRESH MATERIALIZED VIEW CONCURRENTLY "+target)
				if err != nil {
					t.Fatal(err)
				}
				stmt.Close()
			}
		})
	}
}

func TestReferenceMaterializedRefreshConcurrentMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			concurrentSeed(run)
			concurrentIndex(run)
			q := "SELECT c.oid,'concurrent_mv'::regtype::oid AS rowtype_oid,w.oid FROM pg_class c JOIN pg_rewrite w ON w.ev_class=c.oid WHERE c.relname='concurrent_mv'"
			var oid, typ, rule, index int64
			if err := conn.QueryRowContext(p.Ctx, q).Scan(&oid, &typ, &rule); err != nil {
				t.Fatal(err)
			}
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='concurrent_u'").Scan(&index); err != nil {
				t.Fatal(err)
			}
			if oid <= 0 || typ <= 0 || rule <= 0 || index <= 0 {
				t.Fatal("invalid identities")
			}
			cells := []any{oid, typ, rule}
			idx := any(index)
			if b.PG {
				cells = []any{strconv.FormatInt(oid, 10), strconv.FormatInt(typ, 10), strconv.FormatInt(rule, 10)}
				idx = strconv.FormatInt(index, 10)
			}
			var owner string
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			var retained *sql.Rows
			if !b.PG {
				var err error
				retained, err = p.Native.QueryContext(p.Ctx, "SELECT i,s FROM concurrent_mv ORDER BY i")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
			}
			concurrentChange(run)
			run(fixture.Exec(concurrentRefreshSQL, 0))
			run(fixture.Select(q, []string{"oid", "rowtype_oid", "oid"}, []string{"OID", "OID", "OID"}, [][]any{cells}))
			run(fixture.Select("SELECT oid FROM pg_class WHERE relname='concurrent_u'", []string{"oid"}, []string{"OID"}, [][]any{{idx}}))
			run(fixture.Select("SELECT matviewowner,definition FROM pg_matviews WHERE matviewname='concurrent_mv'", []string{"matviewowner", "definition"}, []string{"NAME", "TEXT"}, [][]any{{owner, " SELECT i,\n    s\n   FROM concurrent_base;"}}))
			if retained != nil {
				cols, err := retained.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, false, cols, []string{"i", "s"}, []string{"INT4", "TEXT"}, []bool{true, true})
				for _, want := range []struct {
					i int64
					s any
				}{{1, "old"}, {2, nil}} {
					if !retained.Next() {
						t.Fatal("retained row missing")
					}
					var i int64
					var s any
					if err := retained.Scan(&i, &s); err != nil {
						t.Fatal(err)
					}
					if i != want.i || s != want.s {
						t.Fatal("retained values", i, s)
					}
				}
				if retained.Next() || retained.Err() != nil {
					t.Fatal("retained tail", retained.Err())
				}
				retained.Close()
			}
			run(fixture.Exec("CREATE MATERIALIZED VIEW concurrent_typed AS SELECT 1 AS i,'abcd'::varchar(4) AS v,12.34::numeric(5,2) AS n", 1))
			run(fixture.Exec("CREATE UNIQUE INDEX concurrent_typed_u ON concurrent_typed(i)", 0))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY concurrent_typed", 0))
			s := fixture.Select("SELECT v,n FROM concurrent_typed", []string{"v", "n"}, []string{"VARCHAR", "NUMERIC"}, [][]any{{"abcd", "12.34"}})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				l, ok := cols[0].Length()
				if !ok || l != 4 {
					t.Fatal("varchar typmod", l, ok)
				}
				precision, scale, ok := cols[1].DecimalSize()
				if !ok || precision != 5 || scale != 2 {
					t.Fatal("numeric typmod", precision, scale, ok)
				}
			}
			run(s)
			run(fixture.Prepared(s))
		})
	}
}

func TestReferenceMaterializedRefreshConcurrentFeatureSmoke(t *testing.T) {
	t.Run("lifecycle", TestReferenceMaterializedRefreshConcurrentLifecycle)
	t.Run("prepared", TestReferenceMaterializedRefreshConcurrentPrepared)
	t.Run("diagnostics", TestReferenceMaterializedRefreshConcurrentDiagnostics)
	t.Run("snapshots", TestReferenceMaterializedRefreshConcurrentSnapshots)
	t.Run("controls", TestReferenceMaterializedRefreshConcurrentControls)
}
