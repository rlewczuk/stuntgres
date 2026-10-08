//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryLimitUnordered(t *testing.T) {
	p := referenceLimitFixture(t)
	p.step(t, referenceExec("CREATE TABLE duplicates(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO duplicates VALUES(1),(1),(2),(2),(3),(3)", 6))
	for _, db := range []*sql.DB{p.postgres, p.memory} {
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q       string
				allowed map[int64]int
			}{{"SELECT id FROM t LIMIT 2", map[int64]int{1: 1, 2: 1, 3: 1, 4: 1}}, {"SELECT id FROM duplicates OFFSET 1 LIMIT 2", map[int64]int{1: 2, 2: 2, 3: 2}}} {
				out := referenceRowQuery(p.ctx, db, tc.q, prepared)
				if out.err != nil {
					t.Fatal(out.err)
				}
				seen := make(map[int64]int)
				count := 0
				for out.rows.Next() {
					var id int64
					if err := out.rows.Scan(&id); err != nil {
						out.rows.Close()
						t.Fatal(err)
					}
					count++
					seen[id]++
					if seen[id] > tc.allowed[id] {
						out.rows.Close()
						t.Fatal("not a source submultiset", seen, tc.allowed)
					}
				}
				if err := out.rows.Err(); err != nil {
					out.rows.Close()
					t.Fatal(err)
				}
				out.rows.Close()
				if count != 2 {
					t.Fatal("wrong unordered cardinality", tc.q, count)
				}
			}
		}
	}
	// This portable duplicate datum supplements, rather than replaces, the
	// source-connected per-backend membership/multiplicity assertions above.
	step := referenceSelect("SELECT 7 AS n FROM duplicates LIMIT 2", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
	p.step(t, step)
	p.step(t, referencePrepared(step))
}
