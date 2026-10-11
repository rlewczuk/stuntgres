package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func refreshBag(name string, rows [][]any) fixture.Step {
	s := fixture.Select("SELECT i,s FROM "+name, []string{"i", "s"}, []string{"INT4", "TEXT"}, rows)
	s.Bag = true
	return s
}

func refreshSeed(run func(fixture.Step)) {
	materializedViewSeed(run)
	run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_mv AS SELECT i,s FROM mv_base", 3))
}

func TestReferenceMaterializedRefreshLifecycle(t *testing.T) { refreshLifecycle(t) }

func refreshLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			refreshSeed(run)
			q := "SELECT i,s FROM refresh_mv"
			selectStmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer selectStmt.Close()
			refreshStmt, err := conn.PrepareContext(p.Ctx, "REFRESH MATERIALIZED VIEW refresh_mv WITH DATA")
			if err != nil {
				t.Fatal(err)
			}
			defer refreshStmt.Close()
			run(fixture.Exec("INSERT INTO mv_base VALUES(3,'c')", 1))
			run(materializedViewBag("refresh_mv"))
			four := refreshBag("refresh_mv", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}})
			for _, statement := range []string{"REFRESH MATERIALIZED VIEW refresh_mv", "REFRESH MATERIALIZED VIEW refresh_mv WITH DATA"} {
				run(fixture.Exec(statement, 0))
				run(four)
				run(fixture.Prepared(four))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: selectStmt, SQL: q}, b.Recorder, four)
			}
			run(fixture.Exec("UPDATE mv_base SET s='changed' WHERE i=3", 1))
			run(fixture.Exec("DELETE FROM mv_base WHERE i=1", 2))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: refreshStmt, SQL: "REFRESH MATERIALIZED VIEW refresh_mv WITH DATA"}, b.Recorder, fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv WITH DATA", 0))
			run(refreshBag("refresh_mv", [][]any{{int64(2), nil}, {int64(3), "changed"}}))
			fixture.MaterializedViewPopulation(run, "refresh_mv", true)
			fixture.TableDDLMetadata(t, p.Ctx, conn, b.PG, true, q, []string{"i", "s"}, []string{"INT4", "TEXT"}, []bool{true, true}, 2)
		})
	}
}

func TestReferenceMaterializedRefreshPopulation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			refreshSeed(run)
			q := "SELECT * FROM refresh_mv"
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for range 2 {
				run(fixture.Prepared(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv WITH NO DATA", 0)))
				fixture.MaterializedViewPopulation(run, "refresh_mv", false)
				run(fixture.MaterializedViewUnpopulated("refresh_mv"))
				run(fixture.Prepared(fixture.MaterializedViewUnpopulated("refresh_mv")))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, fixture.MaterializedViewUnpopulated("refresh_mv"))
				run(fixture.Prepared(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv WITH DATA", 0)))
				fixture.MaterializedViewPopulation(run, "refresh_mv", true)
				run(materializedViewBag("refresh_mv"))
			}
			run(fixture.Exec("DELETE FROM mv_base", 3))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv", 0))
			fixture.MaterializedViewPopulation(run, "refresh_mv", true)
			run(refreshBag("refresh_mv", nil))
			fixture.TableDDLMetadata(t, p.Ctx, conn, b.PG, true, q, []string{"i", "s"}, []string{"INT4", "TEXT"}, []bool{true, true}, 0)
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_initial AS SELECT i,s FROM mv_base WITH NO DATA", 0))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_initial", 0))
			run(refreshBag("refresh_initial", nil))
			fixture.MaterializedViewPopulation(run, "refresh_initial", true)
		})
	}
}

func TestReferenceMaterializedRefreshAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("CREATE TABLE refresh_fail_base(i INT)", 0))
			run(fixture.Exec("INSERT INTO refresh_fail_base VALUES(1)", 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_fail AS SELECT 1/i AS x FROM refresh_fail_base", 1))
			run(fixture.Exec("INSERT INTO refresh_fail_base VALUES(0)", 1))
			var before bytes.Buffer
			if !b.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_fail WITH DATA", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(fixture.Select("SELECT x FROM refresh_fail", []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}}))
				fixture.MaterializedViewPopulation(run, "refresh_fail", true)
				if !b.PG {
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(before.Bytes(), after.Bytes()) {
						t.Fatal("failed refresh changed Dump")
					}
				}
			}
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_fail WITH NO DATA", 0))
			run(fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_fail", fixture.ControlsReferenceDiagnostic("22012", "division by zero")))
			fixture.MaterializedViewPopulation(run, "refresh_fail", false)
			run(fixture.MaterializedViewUnpopulated("refresh_fail"))
			run(fixture.Exec("DELETE FROM refresh_fail_base WHERE i=0", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_fail", 0))
			run(fixture.Select("SELECT x FROM refresh_fail", []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}

func TestReferenceMaterializedRefreshMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			refreshSeed(run)
			// Public regtype resolution observes the rowtype registry in both modes.
			// pg_class.reltype/pg_type.typrelid remain independent PG complements.
			identitySQL := "SELECT c.oid,'refresh_mv'::regtype::oid AS rowtype_oid,w.oid FROM pg_class c JOIN pg_rewrite w ON w.ev_class=c.oid WHERE c.relname='refresh_mv'"
			var oid, typ, rule int64
			if err := conn.QueryRowContext(p.Ctx, identitySQL).Scan(&oid, &typ, &rule); err != nil {
				t.Fatal(err)
			}
			if oid == 0 || typ == 0 || rule == 0 || oid == rule {
				t.Fatalf("invalid identity %d/%d/%d", oid, typ, rule)
			}
			identity := []any{oid, typ, rule}
			if b.PG {
				// pq transports OID as decimal text; native retains integer OID values.
				identity = []any{strconv.FormatInt(oid, 10), strconv.FormatInt(typ, 10), strconv.FormatInt(rule, 10)}
			}
			var owner string
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"WITH DATA", "WITH NO DATA", "WITH DATA"} {
				run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv "+suffix, 0))
				run(fixture.Select(identitySQL, []string{"oid", "rowtype_oid", "oid"}, []string{"OID", "OID", "OID"}, [][]any{identity}))
				if b.PG {
					run(fixture.Select("SELECT c.reltype='refresh_mv'::regtype::oid AS rowtype_link,t.typrelid=c.oid AS relation_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.relname='refresh_mv'", []string{"rowtype_link", "relation_link"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
				}
				run(fixture.Select("SELECT matviewowner,definition FROM pg_matviews WHERE matviewname='refresh_mv'", []string{"matviewowner", "definition"}, []string{"NAME", "TEXT"}, [][]any{{owner, " SELECT i,\n    s\n   FROM mv_base;"}}))
			}
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_typed AS SELECT 'abcd'::varchar(4) AS v,12.34::numeric(5,2) AS n", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_typed", 0))
			s := fixture.Select("SELECT * FROM refresh_typed", []string{"v", "n"}, []string{"VARCHAR", "NUMERIC"}, [][]any{{"abcd", "12.34"}})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, cols, s.Columns, s.Types, []bool{true, true})
				length, ok := cols[0].Length()
				if !ok || length != 4 {
					t.Fatalf("length=%d/%t", length, ok)
				}
				precision, scale, ok := cols[1].DecimalSize()
				if !ok || precision != 5 || scale != 2 {
					t.Fatalf("numeric=%d/%d/%t", precision, scale, ok)
				}
			}
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_zero AS SELECT FROM mv_base", 3))
			run(fixture.Exec("INSERT INTO mv_base VALUES(3,'c')", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_zero", 0))
			run(fixture.Select("SELECT * FROM refresh_zero", []string{}, []string{}, [][]any{{}, {}, {}, {}}))
		})
	}
}
