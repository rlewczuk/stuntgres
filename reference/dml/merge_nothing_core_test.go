package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func mergeNothingRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool), setup ...func(*testing.T)) {
	t.Helper()
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		body(t, p, b, prepared)
	}, setup...)
}

const mergeNothingMixed = "MERGE INTO t USING s ON t.id=s.id WHEN MATCHED AND s.id=1 THEN DO NOTHING WHEN MATCHED AND s.id=2 THEN UPDATE SET v=s.v WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE AND t.id=4 THEN DO NOTHING WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET AND s.id=6 THEN DO NOTHING WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)"

func mergeNothingSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,v INT)", 0), fixture.Exec("CREATE TABLE s(id INT,v INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40),(5,50)", 5), fixture.Exec("INSERT INTO s VALUES(1,10),(2,21),(3,30),(6,60),(7,70)", 5)} {
		mergeReturningRun(t, p, b, s)
	}
}
func mergeNothingSurvivors() fixture.Step {
	return fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(21)}, {int64(4), int64(40)}, {int64(7), int64(70)}})
}
func mergeNothingMixedStep(prepared bool) fixture.Step {
	return insertReturningBag(mergeNothingMixed+" RETURNING merge_action(),t.id,t.v,old.v,new.v", []string{"merge_action", "id", "v", "v", "v"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT4"}, [][]any{{"UPDATE", int64(2), int64(21), int64(20), int64(21)}, {"DELETE", int64(3), int64(30), int64(30), nil}, {"DELETE", int64(5), int64(50), int64(50), nil}, {"INSERT", int64(7), int64(70), nil, int64(70)}}, prepared)
}
func TestReferenceMergeNothingMixed(t *testing.T) {
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		mergeNothingSeed(t, p, b)
		run(fixture.Exec(mergeNothingMixed, 4))
		run(mergeNothingSurvivors())
		run(fixture.Exec("DELETE FROM t", 4))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40),(5,50)", 5))
		run(mergeNothingMixedStep(prepared))
		run(mergeNothingSurvivors())
	})
}
func TestReferenceMergeNothingOrdering(t *testing.T) {
	for _, kind := range []string{"MATCHED", "NOT MATCHED BY SOURCE", "NOT MATCHED", "NOT MATCHED BY TARGET"} {
		t.Run(kind, func(t *testing.T) {
			mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("CREATE TABLE source(id INT,den INT)", 0))
				run(fixture.Exec("INSERT INTO source VALUES(1,0),(2,0)", 2))
				for _, flag := range []any{true, false, nil} {
					run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
					source, mutation := "(SELECT * FROM source WHERE id=1) s", "UPDATE SET v=t.v+1"
					want := [][]any{{int64(1), int64(10)}}
					if kind == "NOT MATCHED BY SOURCE" {
						source, mutation = "(SELECT * FROM source WHERE id=2) s", "DELETE"
					}
					if kind == "NOT MATCHED" || kind == "NOT MATCHED BY TARGET" {
						source, mutation = "(SELECT * FROM source WHERE id=2) s", "INSERT VALUES(s.id,20)"
					}
					n := int64(0)
					if flag != true {
						n = 1
						switch kind {
						case "MATCHED":
							want = [][]any{{int64(1), int64(11)}}
						case "NOT MATCHED BY SOURCE":
							want = [][]any{}
						default:
							want = [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
						}
					}
					q := "MERGE INTO t USING " + source + " ON t.id=s.id WHEN " + kind + " AND $1::boolean THEN DO NOTHING WHEN " + kind + " THEN " + mutation
					run(fixture.Exec(q, n, flag))
					if flag == true {
						denominator := "s.den"
						if kind == "NOT MATCHED BY SOURCE" {
							denominator = "t.v-10"
						}
						run(fixture.Exec("MERGE INTO t USING "+source+" ON t.id=s.id WHEN "+kind+" AND TRUE THEN DO NOTHING WHEN "+kind+" AND 10/("+denominator+")>0 THEN "+mutation, 0))
					}
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
					run(fixture.Exec("DELETE FROM t", int64(len(want))))
				}
			})
		})
	}
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("CREATE TABLE source(id INT,den INT)", 0))
		run(fixture.Exec("INSERT INTO source VALUES(1,0)", 1))
		for _, q := range []string{
			"MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND FALSE THEN DO NOTHING WHEN MATCHED AND NULL THEN UPDATE SET v=99 WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)",
			"MERGE INTO t USING (VALUES(1),(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DO NOTHING",
			"MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED AND TRUE THEN DO NOTHING WHEN MATCHED AND 10/s.den>0 THEN UPDATE SET v=10/(t.v-10)",
			"MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN DO NOTHING",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
		run(fixture.Exec("DELETE FROM t", 1))
		run(fixture.Exec("MERGE INTO t USING (VALUES(2)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN DO NOTHING", 0))
		run(fixture.Exec("MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN DO NOTHING WHEN NOT MATCHED THEN DO NOTHING", 0))
	})
}
func TestReferenceMergeNothingSuppression(t *testing.T) {
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE SEQUENCE draws START 101 CACHE 1", 0))
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("CREATE TABLE source(id INT,den INT)", 0))
		run(fixture.Exec("INSERT INTO source VALUES(1,0),(2,0)", 2))
		for _, tc := range []struct{ source, kind, out string }{{"(SELECT * FROM source WHERE id=1) s", "MATCHED", "10/(t.v-10)"}, {"(SELECT * FROM source WHERE id=2) s", "NOT MATCHED BY SOURCE", "10/(t.v-10)"}, {"(SELECT * FROM source WHERE id=2) s", "NOT MATCHED BY TARGET", "10/s.den"}} {
			q := "MERGE INTO t USING " + tc.source + " ON t.id=s.id WHEN " + tc.kind + " THEN DO NOTHING RETURNING " + tc.out
			run(insertReturningBag(q, []string{"?column?"}, []string{"INT4"}, [][]any{}, prepared))
			run(fixture.Exec(q, 0))
			run(insertReturningBag("MERGE INTO t USING "+tc.source+" ON t.id=s.id WHEN "+tc.kind+" THEN DO NOTHING RETURNING nextval('draws')", []string{"nextval"}, []string{"INT8"}, [][]any{}, prepared))
		}
		run(fixture.Exec("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND TRUE THEN DO NOTHING WHEN MATCHED THEN UPDATE SET v=nextval('draws')", 0))
		run(fixture.Select("SELECT is_called FROM draws", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Exec("CREATE TABLE generated(id INT GENERATED ALWAYS AS IDENTITY,k INT,v BIGINT DEFAULT nextval('draws'),g INT GENERATED ALWAYS AS(10/k) STORED)", 0))
		run(fixture.Exec("CREATE TABLE generated_source(k INT)", 0))
		run(fixture.Exec("INSERT INTO generated_source VALUES(0),(2)", 2))
		run(fixture.Exec("MERGE INTO generated USING (SELECT k FROM generated_source ORDER BY k) s ON FALSE WHEN NOT MATCHED AND s.k=0 THEN DO NOTHING WHEN NOT MATCHED THEN INSERT(k) VALUES(s.k)", 1))
		run(fixture.Select("SELECT * FROM generated", []string{"id", "k", "v", "g"}, []string{"INT4", "INT4", "INT8", "INT4"}, [][]any{{int64(1), int64(2), int64(101), int64(5)}}))
		run(fixture.Select("SELECT nextval('draws')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(102)}}))
		run(fixture.Exec("MERGE INTO t USING (SELECT id,omit FROM (VALUES(1,TRUE),(1,FALSE)) x(id,omit) ORDER BY omit DESC) s ON t.id=s.id WHEN MATCHED AND s.omit THEN DO NOTHING WHEN MATCHED THEN UPDATE SET v=t.v+1", 1))
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}))
		d := fixture.Diagnostic{Code: "21000", Message: "MERGE command cannot affect row a second time", Hint: "Ensure that not more than one source row matches any one target row."}
		insertReturningFailure(t, p, b, b.DB, "MERGE INTO t USING (VALUES(1),(1)) s(id) ON t.id=s.id WHEN MATCHED AND FALSE THEN DO NOTHING WHEN MATCHED THEN UPDATE SET v=t.v+1 RETURNING t.id", prepared, true, d)
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}))
	})
}
