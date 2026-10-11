package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGroupingSetsConsumers(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT a,b,GROUPING(a,b),count(*),sum(v) FROM gs GROUP BY ROLLUP(a,b) HAVING GROUPING(a,b)=3", []string{"a", "b", "grouping", "count", "sum"}, []string{"INT4", "INT4", "INT4", "INT8", "INT8"}, [][]any{{nil, nil, int64(3), int64(3), int64(10)}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT a,b,GROUPING(a,b) AS g,count(*) OVER () AS n FROM gs GROUP BY ROLLUP(a,b)", []string{"a", "b", "g", "n"}, []string{"INT4", "INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(0), int64(6)}, {int64(1), int64(20), int64(0), int64(6)}, {nil, int64(10), int64(0), int64(6)}, {int64(1), nil, int64(1), int64(6)}, {nil, nil, int64(1), int64(6)}, {nil, nil, int64(3), int64(6)}}))
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT GROUPING(a,b) AS g,count(*) OVER () AS n FROM gs GROUP BY ROLLUP(a,b) HAVING GROUPING(a,b)=3", []string{"g", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(1)}}))
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT a,b,row_number() OVER(ORDER BY GROUPING(a,b),a NULLS FIRST,b NULLS FIRST) AS r FROM gs GROUP BY ROLLUP(a,b) ORDER BY r", []string{"a", "b", "r"}, []string{"INT4", "INT4", "INT8"}, [][]any{{nil, int64(10), int64(1)}, {int64(1), int64(10), int64(2)}, {int64(1), int64(20), int64(3)}, {nil, nil, int64(4)}, {int64(1), nil, int64(5)}, {nil, nil, int64(6)}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT GROUPING(a,b) AS g,count(*) OVER(PARTITION BY GROUPING(a,b)) AS n FROM gs GROUP BY ROLLUP(a,b)", []string{"g", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(3)}, {int64(0), int64(3)}, {int64(0), int64(3)}, {int64(1), int64(2)}, {int64(1), int64(2)}, {int64(3), int64(1)}}))
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT CASE WHEN GROUPING(a,b)=3 THEN 'total' ELSE 'detail' END AS label FROM gs GROUP BY ROLLUP(a,b) ORDER BY GROUPING(a,b) DESC", []string{"label"}, []string{"TEXT"}, [][]any{{"total"}, {"detail"}, {"detail"}, {"detail"}, {"detail"}, {"detail"}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsLevel("SELECT a,b,g AS grouping,n AS count,s AS sum FROM (SELECT a,b,GROUPING(a,b) AS g,count(*) AS n,sum(v) AS s FROM gs GROUP BY ROLLUP(a,b)) d", false))
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT (SELECT count(*) FROM gs WHERE FALSE GROUP BY ()) AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT GROUPING(a) AS g,count(*) AS n FROM gs GROUP BY ROLLUP(a) UNION ALL SELECT GROUPING(a),count(*) FROM gs_empty GROUP BY ROLLUP(a)", []string{"g", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(2)}, {int64(0), int64(1)}, {int64(1), int64(3)}, {int64(1), int64(0)}}))
	})
}

func TestReferenceGroupingSetsAtomicity(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		groupingSetsInsertAtomicity(t, run)
		for _, prepared := range []bool{false, true} {
			for _, clause := range []struct {
				sql string
				n   int64
			}{{"CUBE(a,b)", 8}, {"ALL GROUPING SETS((a),(a),())", 5}, {"DISTINCT GROUPING SETS((a),(a),())", 3}} {
				s := fixture.Exec("INSERT INTO gs_out SELECT a,GROUPING(a),count(*) FROM gs GROUP BY "+clause.sql, clause.n)
				s.Prepared = prepared
				run(s)
				run(fixture.Exec("DELETE FROM gs_out", clause.n+6))
				run(fixture.Exec("INSERT INTO gs_out SELECT a,GROUPING(a),count(*) FROM gs GROUP BY ROLLUP(a,b)", 6))
			}
		}
		run(fixture.Exec("CREATE TABLE gs_copy AS SELECT a,b,GROUPING(a,b) AS g,count(*) AS n,sum(v) AS s FROM gs GROUP BY ROLLUP(a,b)", 6))
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsLevel("SELECT a,b,g AS grouping,n AS count,s AS sum FROM gs_copy", false))
		run(fixture.Exec("CREATE TABLE gs_no_data AS SELECT a,b,GROUPING(a,b) AS g,count(*) AS n FROM gs GROUP BY ROLLUP(a,b) WITH NO DATA", 0))
		fixture.GroupingSetsBoth(run, fixture.Select("SELECT a,b,g,n FROM gs_no_data", []string{"a", "b", "g", "n"}, []string{"INT4", "INT4", "INT4", "INT8"}, nil))
		// Query witnesses fail on every group, so no successful early group can
		// cause pq to return a cursor before a later server error. INSERT retains
		// the late-failure formulas: earlier legal groups must never publish writes.
		for _, expr := range []struct{ query, insert string }{
			{"sum(10/(v-v))", "sum(10/(v-3))"},
			{"10/(count(*)-count(*))", "10/(count(*)-2)"},
		} {
			q := "SELECT " + expr.query + " AS n FROM gs GROUP BY ROLLUP(a)"
			s := fixture.GroupingSetsError(q, "22012", "division by zero", "")
			s.ErrorStage = "execute"
			fixture.GroupingSetsBoth(run, s)
			if !b.PG {
				// Native eager grouping has a stronger nil-cursor publication
				// contract than pq's potentially streaming late error timing.
				late := fixture.GroupingSetsError("SELECT "+expr.insert+" AS n FROM gs GROUP BY ROLLUP(a)", "22012", "division by zero", "")
				late.ErrorStage = "execute"
				fixture.GroupingSetsBoth(run, late)
			}
			q = "INSERT INTO gs_fail SELECT " + expr.insert + " FROM gs GROUP BY ROLLUP(a)"
			s = fixture.GroupingSetsError(q, "22012", "division by zero", "")
			s.SelectRows, s.ErrorStage = false, "execute"
			fixture.GroupingSetsBoth(run, s)
			run(fixture.Select("SELECT n FROM gs_fail", []string{"n"}, []string{"INT8"}, [][]any{{int64(99)}}))
		}
	})
}

func groupingSetsInsertAtomicity(t *testing.T, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE TABLE gs_out(a INT,g INT,n BIGINT)", 0))
	run(fixture.Exec("INSERT INTO gs_out SELECT a,GROUPING(a),count(*) FROM gs GROUP BY ROLLUP(a,b)", 6))
	run(fixture.Select("SELECT count(*) FROM gs_out", []string{"count"}, []string{"INT8"}, [][]any{{int64(6)}}))
	run(fixture.Exec("CREATE TABLE gs_nn(a INT NOT NULL)", 0))
	run(fixture.Exec("INSERT INTO gs_nn VALUES(99)", 1))
	s := fixture.GroupingSetsError("INSERT INTO gs_nn SELECT a FROM gs WHERE a IS NOT NULL GROUP BY ROLLUP(a)", "23502", "null value in column \"a\" of relation \"gs_nn\" violates not-null constraint", "")
	s.SelectRows, s.ErrorStage = false, "execute"
	s.Diagnostic.Detail = "Failing row contains (null)."
	s.Diagnostic.Schema, s.Diagnostic.Table, s.Diagnostic.Column = "public", "gs_nn", "a"
	fixture.GroupingSetsBoth(run, s)
	run(fixture.Select("SELECT a FROM gs_nn", []string{"a"}, []string{"INT4"}, [][]any{{int64(99)}}))
	run(fixture.Exec("CREATE TABLE gs_fail(n BIGINT)", 0))
	run(fixture.Exec("INSERT INTO gs_fail VALUES(99)", 1))
}

func TestReferenceGroupingSetsMetadata(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		groupingSetsMetadata(t, b, run)
	})
}

func groupingSetsMetadata(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE TABLE gs_meta(a INT NOT NULL,b INT NOT NULL,v INT)", 0))
	run(fixture.Exec("INSERT INTO gs_meta VALUES(1,10,2)", 1))
	for _, empty := range []bool{false, true} {
		q := "SELECT a,b,GROUPING(b),count(*),sum(v) FROM gs_meta"
		rows := [][]any{{int64(1), int64(10), int64(0), int64(1), int64(2)}, {int64(1), nil, int64(1), int64(1), int64(2)}}
		if empty {
			q += " WHERE FALSE"
			rows = nil
		}
		q += " GROUP BY a,ROLLUP(b)"
		s := fixture.GroupingSetsBag(q, []string{"a", "b", "grouping", "count", "sum"}, []string{"INT4", "INT4", "INT4", "INT8", "INT8"}, rows)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(meta) != 5 {
				t.Fatalf("private grouping slots exposed: %d", len(meta))
			}
			for i, m := range meta {
				wantScan := reflect.TypeFor[int64]()
				if b.PG && i < 3 {
					wantScan = reflect.TypeFor[int32]()
				}
				if m.ScanType() != wantScan {
					t.Fatalf("%s ScanType %v want %v", m.Name(), m.ScanType(), wantScan)
				}
				wantNullable, wantKnown := i == 1 || i == 4, true
				if b.PG {
					wantNullable, wantKnown = false, false
				}
				if nullable, known := m.Nullable(); nullable != wantNullable || known != wantKnown {
					t.Fatalf("%s Nullable %t/%t want %t/%t", m.Name(), nullable, known, wantNullable, wantKnown)
				}
				if length, known := m.Length(); length != 0 || known {
					t.Fatalf("%s Length %d/%t", m.Name(), length, known)
				}
				if precision, scale, known := m.DecimalSize(); precision != 0 || scale != 0 || known {
					t.Fatalf("%s DecimalSize %d/%d/%t", m.Name(), precision, scale, known)
				}
			}
		}
		fixture.GroupingSetsBoth(run, s)
	}
}
