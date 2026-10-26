package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func brinIndexScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, func(fixture.Step))) {
	t.Helper()
	for _, plain := range []bool{false, true} {
		t.Run(fmt.Sprintf("plain=%v", plain), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) {
						s.NonContext = plain
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					}
					body(t, p, b, run)
				})
			}
		})
	}
}

func brinIndexSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE t(id INT,n INT,s TEXT)", 0))
	run(fixture.Exec("INSERT INTO t VALUES(1,1,'a'),(2,9,'z'),(3,4,'d'),(4,NULL,NULL),(5,7,'g'),(6,4,'d')", 6))
}

func brinIndexIDs(predicate string, ids ...int64) fixture.Step {
	rows := make([][]any, len(ids))
	for i, id := range ids {
		rows[i] = []any{id}
	}
	return fixture.Select("SELECT id FROM t WHERE "+predicate+" ORDER BY id", []string{"id"}, []string{"INT4"}, rows)
}

func TestReferenceBrinIndexBuild(t *testing.T) {
	brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		brinIndexSeed(run)
		for _, q := range []string{
			"CREATE INDEX bn ON t USING brin(n)",
			"CREATE INDEX bs ON t USING brin(n pg_catalog.int4_minmax_ops,s pg_catalog.text_minmax_ops) WITH(pages_per_range=2,autosummarize=on)",
		} {
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Select("SELECT id,n,s FROM t ORDER BY id", []string{"id", "n", "s"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "a"}, {int64(2), int64(9), "z"}, {int64(3), int64(4), "d"}, {int64(4), nil, nil}, {int64(5), int64(7), "g"}, {int64(6), int64(4), "d"}}))
		run(fixture.Exec("CREATE TABLE empty_brin(n INT)", 0))
		run(fixture.Exec("CREATE INDEX be ON empty_brin USING brin(n)", 0))
		run(fixture.Exec("INSERT INTO empty_brin VALUES(NULL),(NULL)", 2))
		run(fixture.Exec("CREATE INDEX bnull ON empty_brin USING brin(n)", 0))
		run(fixture.Select("SELECT n FROM empty_brin WHERE n IS NULL", []string{"n"}, []string{"INT4"}, [][]any{{nil}, {nil}}))
	})
}

func TestReferenceBrinIndexBoundaries(t *testing.T) {
	brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		brinIndexSeed(run)
		q := "SELECT id FROM t WHERE n >= $1 ORDER BY id"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for phase := range 2 {
			if phase == 1 {
				run(fixture.Exec("CREATE INDEX bn ON t USING brin(n,s) WITH(pages_per_range=2)", 0))
			}
			for _, tc := range []struct {
				predicate string
				ids       []int64
			}{
				{"n<4", []int64{1}}, {"n<=4", []int64{1, 3, 6}}, {"n=4", []int64{3, 6}},
				{"n>=4", []int64{2, 3, 5, 6}}, {"n>4", []int64{2, 5}}, {"n=5", nil},
				{"n IS NULL", []int64{4}}, {"n IS NOT NULL", []int64{1, 2, 3, 5, 6}},
				{"n<>4", []int64{1, 2, 5}}, {"s LIKE 'd%'", []int64{3, 6}},
			} {
				s := brinIndexIDs(tc.predicate, tc.ids...)
				run(s)
				run(fixture.Prepared(s))
			}
			s := fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(5)}, {int64(6)}}, int64(4))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
		}
	})
}

func brinIndexMutate(run func(fixture.Step)) {
	run(fixture.Exec("INSERT INTO t VALUES(7,0,'')", 1))
	run(fixture.Exec("UPDATE t SET n=2 WHERE id=2", 1))
	run(fixture.Exec("DELETE FROM t WHERE id=3", 1))
	run(fixture.Select("SELECT id,n FROM t ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(4), nil}, {int64(5), int64(7)}, {int64(6), int64(4)}, {int64(7), int64(0)}}))
	run(brinIndexIDs("n=4", 6))
}

func TestReferenceBrinIndexMaintenance(t *testing.T) {
	brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		brinIndexSeed(run)
		run(fixture.Exec("CREATE INDEX bn ON t USING brin(n,s) WITH(pages_per_range=2,autosummarize=on)", 0))
		brinIndexMutate(run)
		run(fixture.Exec("ALTER INDEX bn SET(pages_per_range=1)", 0))
		run(fixture.Exec("VACUUM t", 0))
		run(brinIndexIDs("n>=4", 5, 6))
		run(fixture.Exec("REINDEX INDEX bn", 0))
		run(brinIndexIDs("n>=4", 5, 6))
	})
}

func TestReferenceBrinIndexSmoke(t *testing.T) {
	brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		brinIndexSeed(run)
		run(fixture.Exec("CREATE INDEX bn ON t USING brin(n) WITH(pages_per_range=2,autosummarize=on)", 0))
		q := "SELECT id FROM t WHERE n >= $1 ORDER BY id"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(5)}, {int64(6)}}, int64(4)))
		brinIndexMutate(run)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}}, int64(4)))
		brinIndexProperties(run)
		hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX retry ON t USING brin(n) WITH(pages_per_range=0)", fixture.Diagnostic{Code: "22023", Message: `value 0 out of bounds for option "pages_per_range"`, Detail: `Valid values are between "1" and "131072".`})
		run(fixture.Exec("CREATE INDEX retry ON t USING brin(n)", 0))
		brinIndexControls(t, p, b, run)
	})
}
