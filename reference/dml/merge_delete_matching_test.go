package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeDeleteMatching(t *testing.T) {
	for _, tc := range []struct {
		name, seed, source, on, arms string
		n                            int64
		rows                         [][]any
	}{
		{"matched", "(1,10),(2,20),(3,30)", "(1)", "t.id=s.id", "WHEN MATCHED THEN DELETE", 1, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}},
		{"bySource", "(1,10),(2,20),(3,30)", "(1)", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN DELETE", 2, [][]any{{int64(1), int64(10)}}},
		{"both", "(1,10),(2,20),(3,30)", "(1)", "t.id=s.id", "WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE", 3, nil},
		{"targetCondition", "(1,10),(2,20),(3,30)", "(1)", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE AND id=2 THEN DELETE", 1, [][]any{{int64(1), int64(10)}, {int64(3), int64(30)}}},
		{"distinctPhysicalTargets", "(1,10),(1,10)", "(1)", "TRUE", "WHEN MATCHED THEN DELETE", 2, nil},
		{"oncePerUnmatchedTarget", "(1,10),(2,20)", "(8),(9)", "t.id=s.id", "WHEN NOT MATCHED BY SOURCE THEN DELETE", 2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				n := int64(3)
				if tc.name == "distinctPhysicalTargets" || tc.name == "oncePerUnmatchedTarget" {
					n = 2
				}
				run(fixture.Exec("INSERT INTO t VALUES"+tc.seed, n))
				run(fixture.Exec("MERGE INTO t USING (VALUES"+tc.source+") s(id) ON "+tc.on+" "+tc.arms, tc.n))
				check := fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, tc.rows)
				check.Bag = true
				run(check)
				run(fixture.Exec("MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) s ON t.id=s.id WHEN MATCHED THEN DELETE", 0))
				run(check)
			})
		})
	}
}

func TestReferenceMergeDeleteEmpty(t *testing.T) {
	for _, tc := range []struct {
		name           string
		target, source bool
		arms           string
		n              int64
		rows           [][]any
	}{
		{"emptySourceMatched", true, false, "WHEN MATCHED THEN DELETE", 0, [][]any{{int64(1), int64(10)}}},
		{"emptySourceBySource", true, false, "WHEN NOT MATCHED BY SOURCE THEN DELETE", 1, nil},
		{"emptyTarget", false, true, "WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE", 0, nil},
		{"bothEmpty", false, false, "WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE", 0, nil},
		{"emptyTargetInsert", false, true, "WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,20)", 1, [][]any{{int64(2), int64(20)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT CONSTRAINT t_key UNIQUE,v INT DEFAULT 70)", 0))
				run(fixture.Exec("CREATE TABLE source(id INT)", 0))
				if tc.target {
					run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				}
				if tc.source {
					run(fixture.Exec("INSERT INTO source VALUES(2)", 1))
				}
				run(fixture.Exec("MERGE INTO t USING source s ON t.id=s.id "+tc.arms, tc.n))
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, tc.rows))
				run(fixture.Exec("INSERT INTO t(id) VALUES(7)", 1))
				run(fixture.Select("SELECT v FROM t WHERE id=7", []string{"v"}, []string{"INT4"}, [][]any{{int64(70)}}))
			})
		})
	}
}

func TestReferenceMergeDeleteConditions(t *testing.T) {
	for _, tc := range []struct {
		name, arms string
		n          int64
		rows       [][]any
	}{
		{"matchedFalse", "WHEN MATCHED AND FALSE THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE", 1, [][]any{{int64(1), int64(10)}}},
		{"matchedNull", "WHEN MATCHED AND NULL::boolean THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE", 1, [][]any{{int64(1), int64(10)}}},
		{"fallthrough", "WHEN MATCHED AND FALSE THEN DELETE WHEN MATCHED AND NULL::boolean THEN DELETE WHEN MATCHED AND s.id=1 THEN DELETE WHEN MATCHED AND 10/(s.id-1)>0 THEN UPDATE SET v=99 WHEN NOT MATCHED BY SOURCE AND FALSE THEN DELETE WHEN NOT MATCHED BY SOURCE AND NULL::boolean THEN DELETE WHEN NOT MATCHED BY SOURCE AND t.v=30 THEN DELETE", 2, nil},
		{"threeClassesExplicit", "WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,40) WHEN NOT MATCHED BY SOURCE THEN DELETE", 3, [][]any{{int64(4), int64(40)}}},
		{"threeClassesImplicit", "WHEN MATCHED THEN DELETE WHEN NOT MATCHED THEN INSERT VALUES(s.id,40) WHEN NOT MATCHED BY SOURCE THEN DELETE", 3, [][]any{{int64(4), int64(40)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(3,30)", 2))
				run(fixture.Exec("MERGE INTO t USING (VALUES(1),(4)) s(id) ON t.id=s.id "+tc.arms, tc.n))
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, tc.rows))
			})
		})
	}
}
