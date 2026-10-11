package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// The shared MergeRun runner owns direct/prepared execution and the pq
// MERGE-tag boundary for both action classes.
func TestReferenceMergeUpdateMatching(t *testing.T) {
	for _, tc := range []struct {
		name, seed, source, on string
		n                      int64
		rows                   [][]any
	}{
		{"allPairs", "(1,10,20),(2,30,40)", "(9,1)", "TRUE", 2, [][]any{{int64(1), int64(21), int64(10)}, {int64(2), int64(41), int64(30)}}},
		{"equalOccurrences", "(1,10,20),(1,10,20)", "(1,1)", "t.id=s.id", 2, [][]any{{int64(1), int64(21), int64(10)}, {int64(1), int64(21), int64(10)}}},
		{"equality", "(1,10,20),(2,30,40)", "(1,1),(2,1),(3,1)", "t.id=s.id", 2, [][]any{{int64(1), int64(21), int64(10)}, {int64(2), int64(41), int64(30)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,a INT,b INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES"+tc.seed, 2))
				run(fixture.Exec("MERGE INTO t USING (VALUES"+tc.source+") s(id,delta) ON "+tc.on+" WHEN MATCHED THEN UPDATE SET a=t.b+s.delta,b=t.a", tc.n))
				check := fixture.Select("SELECT * FROM t", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, tc.rows)
				check.Bag = true
				run(check)
			})
		})
	}
}

func TestReferenceMergeUpdateConditions(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10,20),(2,30,40)", 2))
		run(fixture.Exec("MERGE INTO t USING (VALUES(1),(2),(3)) s(id) ON t.id=s.id WHEN MATCHED AND FALSE THEN UPDATE SET a=999 WHEN NOT MATCHED THEN INSERT VALUES(s.id,0,0) WHEN MATCHED AND NULL::boolean THEN UPDATE SET a=888 WHEN MATCHED THEN UPDATE SET a=t.b+1,b=t.a", 3))
		check := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(21), int64(10)}, {int64(2), int64(41), int64(30)}, {int64(3), int64(0), int64(0)}})
		run(check)
		run(fixture.Exec("MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED AND FALSE THEN UPDATE SET a=999 WHEN MATCHED AND NULL::boolean THEN UPDATE SET a=888 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99,99)", 0))
		run(check)
		run(fixture.Exec("MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) s ON TRUE WHEN MATCHED THEN UPDATE SET a=a", 0))
		run(fixture.Exec("MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET a=t.a", 2))
		run(check)
	})
}

func TestReferenceMergeUpdateExpressions(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,a INT,b INT,note TEXT,flag BOOL,w BIGINT,data BYTEA)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10,20,'old',TRUE,9000000000,'\\x0102')", 1))
		run(fixture.Exec("MERGE INTO t dst USING (VALUES($1::integer,$2::integer)) src(id,d) ON dst.id=src.id WHEN MATCHED THEN UPDATE SET a=(SELECT dst.b+src.d),b=dst.a,note=$3::text,flag=$4::boolean,w=dst.w+1,data=$5::bytea", 1, 1, 3, nil, false, []byte{3, 4}))
		run(fixture.Select("SELECT * FROM t", []string{"id", "a", "b", "note", "flag", "w", "data"}, []string{"INT4", "INT4", "INT4", "TEXT", "BOOL", "INT8", "BYTEA"}, [][]any{{int64(1), int64(23), int64(10), nil, false, int64(9000000001), "\x03\x04"}}))
		run(fixture.Exec("CREATE VIEW source_view AS SELECT id,a FROM t", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("UPDATE t SET a=30", 1))
		run(fixture.Exec("MERGE INTO t dst USING source_view src ON dst.id=src.id WHEN MATCHED THEN UPDATE SET a=src.a+1,b=dst.a", 1))
		run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(31), int64(30)}}))
		run(fixture.Exec("ROLLBACK", 0))
	})
}

func TestReferenceMergeUpdateArrays(t *testing.T) {
	for _, tc := range []struct{ name, set, want string }{
		{"oldFrame", "a[s.i]=t.a[1],a[1]=t.a[s.i]", "{20,10,30}"},
		{"overlap", "a[1:2]=ARRAY[7,8],a[2:3]=ARRAY[9,10]", "{7,9,10}"},
		{"boundsSubqueries", "a[(SELECT s.i):$1]=ARRAY[$2::integer,$3::integer]", "{10,7,8}"},
		{"extendLower", "a[-1]=s.v", "[-1:3]={7,NULL,10,20,30}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,a INT[])", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,ARRAY[10,20,30])", 1))
				q := "MERGE INTO t USING (VALUES(1,2,7)) s(id,i,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET " + tc.set
				step := fixture.Exec(q, 1)
				if tc.name == "boundsSubqueries" {
					step.Args = []any{3, 7, 8}
				}
				run(step)
				run(fixture.ArrayReferenceSelect("SELECT a FROM t", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{tc.want}}, b.PG))
			})
		})
	}
	for _, tc := range []struct{ name, set, code, message string }{
		{"nullIndex", "a[s.i]=7", "22004", "array subscript in assignment must not be null"},
		{"shortSlice", "a[1:2]=ARRAY[s.id]", "2202E", "source array too small"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,a INT[])", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,ARRAY[10,20,30])", 1))
				s := fixture.ImplicitError("MERGE INTO t USING (VALUES(1,NULL::integer)) s(id,i) ON t.id=s.id WHEN MATCHED THEN UPDATE SET "+tc.set, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.ErrorStage = "execute"
				run(s)
				run(fixture.ArrayReferenceSelect("SELECT a FROM t", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{10,20,30}"}}, b.PG))
			})
		})
	}
}
