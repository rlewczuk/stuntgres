package reference_test

import (
	"testing"
)

func TestReferenceQueryLimitUnordered(t *testing.T) {
	p := referenceLimitFixture(t)
	p.step(t, referenceExec("CREATE TABLE duplicates(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO duplicates VALUES(1),(1),(2),(2),(3),(3)", 6))
	for _, backend := range p.backends() {
		db := backend.db
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q       string
				allowed map[int64]int
			}{{"SELECT id FROM t LIMIT 2", map[int64]int{1: 1, 2: 1, 3: 1, 4: 1}}, {"SELECT id FROM duplicates OFFSET 1 LIMIT 2", map[int64]int{1: 2, 2: 2, 3: 2}}} {
				func() {
					out := referenceRowQuery(p.ctx, db, tc.q, prepared)
					if out.stmt != nil {
						defer out.stmt.Close()
					}
					if out.rows != nil {
						defer out.rows.Close()
					}
					if out.err != nil {
						t.Fatal(out.err)
					}
					seen := make(map[int64]int)
					count := 0
					for out.rows.Next() {
						var id int64
						if err := out.rows.Scan(&id); err != nil {
							t.Fatal(err)
						}
						count++
						seen[id]++
						if seen[id] > tc.allowed[id] {
							t.Fatal("not a source submultiset", seen, tc.allowed)
						}
					}
					if err := out.rows.Err(); err != nil {
						t.Fatal(err)
					}
					if err := out.rows.Close(); err != nil {
						t.Fatal(err)
					}
					if out.stmt != nil {
						if err := out.stmt.Close(); err != nil {
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
	step := referenceSelect("SELECT 7 AS n FROM duplicates LIMIT 2", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
	p.step(t, step)
	p.step(t, referencePrepared(step))
}
