package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeSourceUpdateMembership(t *testing.T) {
	for _, tc := range []struct {
		name, seed, source string
		seedCount, count   int64
		rows               [][]any
	}{
		{"ordinary", "(1,10,20),(2,30,40),(3,50,60)", "VALUES(1),(4)", 3, 2, [][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(41), int64(30)}, {int64(3), int64(61), int64(50)}}},
		{"emptySource", "(1,10,20),(2,30,40)", "SELECT 1 WHERE FALSE", 2, 2, [][]any{{int64(1), int64(21), int64(10)}, {int64(2), int64(41), int64(30)}}},
		{"emptyTarget", "", "VALUES(1)", 0, 0, [][]any{}},
		{"equalOccurrences", "(2,30,40),(2,30,40)", "VALUES(1)", 2, 2, [][]any{{int64(2), int64(41), int64(30)}, {int64(2), int64(41), int64(30)}}},
		{"duplicateMatches", "(1,10,20),(2,30,40)", "VALUES(1),(1)", 2, 1, [][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(41), int64(30)}}},
		{"manyNonmatches", "(2,30,40)", "VALUES(1),(3),(4)", 1, 1, [][]any{{int64(2), int64(41), int64(30)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,a INT,b INT)", 0))
				if tc.seed != "" {
					run(fixture.Exec("INSERT INTO t VALUES"+tc.seed, tc.seedCount))
				}
				run(fixture.Exec("MERGE INTO t USING ("+tc.source+") s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a=t.b+1,b=t.a", tc.count))
				check := fixture.Select("SELECT * FROM t", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, tc.rows)
				check.Bag = true
				run(check)
			})
		})
	}
}

func TestReferenceMergeSourceUpdateClassification(t *testing.T) {
	for _, tc := range []struct {
		name, seed, source, on string
		count                  int64
		rows                   [][]any
	}{
		{"onResidual", "(1,10)", "VALUES(1)", "t.id=s.id AND t.v>10", 1, [][]any{{int64(1), int64(11)}}},
		{"unknown", "(1,10)", "VALUES(1)", "NULL::boolean", 1, [][]any{{int64(1), int64(11)}}},
		{"true", "(1,10)", "VALUES(1)", "TRUE", 0, [][]any{{int64(1), int64(10)}}},
		{"trueEmptySource", "(1,10)", "SELECT 1 WHERE FALSE", "TRUE", 1, [][]any{{int64(1), int64(11)}}},
		{"nullEquality", "(NULL,10)", "VALUES(NULL::integer)", "t.id=s.id", 1, [][]any{{nil, int64(11)}}},
		{"realNullOccurrence", "(NULL,10)", "VALUES(NULL::integer)", "t.id IS NOT DISTINCT FROM s.id", 0, [][]any{{nil, int64(10)}}},
		{"absentNotNullOccurrence", "(NULL,10)", "SELECT NULL::integer WHERE FALSE", "t.id IS NOT DISTINCT FROM s.id", 1, [][]any{{nil, int64(11)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES"+tc.seed, 1))
				run(fixture.Exec("MERGE INTO t USING ("+tc.source+") s(id) ON "+tc.on+" WHEN MATCHED AND FALSE THEN UPDATE SET v=999 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+1", tc.count))
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, tc.rows))
			})
		})
	}
}

func TestReferenceMergeSourceUpdateConditions(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
		run(fixture.Exec("MERGE INTO t USING (VALUES(1),(4)) s(id) ON t.id=s.id WHEN MATCHED AND FALSE THEN UPDATE SET v=999 WHEN NOT MATCHED BY SOURCE AND FALSE THEN UPDATE SET v=1/(t.id-t.id) WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,40) WHEN NOT MATCHED BY SOURCE AND NULL::boolean THEN UPDATE SET v=998 WHEN NOT MATCHED BY SOURCE AND t.id=2 THEN UPDATE SET v=t.v+100 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+200", 3))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(120)}, {int64(3), int64(230)}, {int64(4), int64(40)}}))
		run(fixture.Exec("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v WHEN MATCHED THEN UPDATE SET v=t.v", 4))
	})
}

func TestReferenceMergeSourceUpdateThreeClasses(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		run(fixture.Exec("MERGE INTO t USING (VALUES(1),(3)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v+1 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100 WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,30)", 3))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(120)}, {int64(3), int64(30)}}))
	})
}
