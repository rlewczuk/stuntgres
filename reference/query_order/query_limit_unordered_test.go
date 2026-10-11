package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLimitUnordered(t *testing.T) {
	p := referenceLimitFixture(t)
	p.Step(t, fixture.Exec("CREATE TABLE duplicates(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO duplicates VALUES(1),(1),(2),(2),(3),(3)", 6))
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q       string
				allowed map[int64]int
			}{{"SELECT id FROM t LIMIT 2", map[int64]int{1: 1, 2: 1, 3: 1, 4: 1}}, {"SELECT id FROM duplicates OFFSET 1 LIMIT 2", map[int64]int{1: 2, 2: 2, 3: 2}}} {
				func() {
					out := fixture.RowQuery(p.Ctx, db, tc.q, prepared)
					if out.Stmt != nil {
						defer out.Stmt.Close()
					}
					if out.Rows != nil {
						defer out.Rows.Close()
					}
					if out.Err != nil {
						t.Fatal(out.Err)
					}
					seen := make(map[int64]int)
					count := 0
					for out.Rows.Next() {
						var id int64
						if err := out.Rows.Scan(&id); err != nil {
							t.Fatal(err)
						}
						count++
						seen[id]++
						if seen[id] > tc.allowed[id] {
							t.Fatal("not a source submultiset", seen, tc.allowed)
						}
					}
					if err := out.Rows.Err(); err != nil {
						t.Fatal(err)
					}
					if err := out.Rows.Close(); err != nil {
						t.Fatal(err)
					}
					if out.Stmt != nil {
						if err := out.Stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if count != 2 {
						t.Fatal("wrong unordered cardinality", tc.q, count)
					}
				}()
			}
		}
	}
	// This portable duplicate datum supplements, rather than replaces, the
	// source-connected per-backend membership/multiplicity assertions above.
	step := fixture.Select("SELECT 7 AS n FROM duplicates LIMIT 2", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
}
