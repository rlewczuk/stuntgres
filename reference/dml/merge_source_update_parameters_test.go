package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// A genuinely held handle crosses TRUE/FALSE/NULL/TRUE with independently
// specified membership, exact native counts and PostgreSQL18.0 planning legality.
func TestReferenceMergeSourceUpdateParameters(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, false)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		q := "MERGE INTO t USING (VALUES(1,7)) s(id,x) ON $1::boolean WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100 WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.x)"
		var held *sql.Stmt
		var err error
		if prepared {
			held, err = b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
		}
		for _, arg := range []any{true, false, nil, true} {
			run(fixture.Exec("INSERT INTO t VALUES(2,10)", 1))
			var r sql.Result
			if prepared {
				r, err = held.ExecContext(p.Ctx, arg)
			} else {
				r, err = b.DB.ExecContext(p.Ctx, q, arg)
			}
			n := int64(2)
			want := [][]any{{int64(1), int64(7)}, {int64(2), int64(110)}}
			if arg == true {
				n = 0
				want = [][]any{{int64(2), int64(10)}}
			}
			mergeInsertAffected(t, b, r, err, n)
			run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
			run(fixture.Exec("DELETE FROM t", int64(len(want))))
		}
	})
}
