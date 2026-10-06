//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceArithmeticComputedMetadata(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(i INT,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(NULL,NULL)", 1))
	for _, q := range []string{"SELECT i+1,b*2 FROM t", "SELECT i+1,b*2 FROM t WHERE FALSE"} {
		want := [][]any{{nil, nil}}
		if q == "SELECT i+1,b*2 FROM t WHERE FALSE" {
			want = nil
		}
		step := referenceSelect(q, []string{"?column?", "?column?"}, []string{"INT4", "INT8"}, want)
		p.step(t, step)
		p.step(t, referencePrepared(step))
		for _, db := range []*sql.DB{p.memory, p.postgres} {
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				var stmt *sql.Stmt
				if prepared {
					stmt, err = db.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					rows, err = stmt.QueryContext(p.ctx)
				} else {
					rows, err = db.QueryContext(p.ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for i, typ := range []string{"INT4", "INT8"} {
					if meta[i].Name() != "?column?" || meta[i].DatabaseTypeName() != typ {
						t.Fatal(meta[i])
					}
					if _, known := meta[i].Nullable(); known {
						t.Fatal("computed scalar nullability unexpectedly known")
					}
				}
				rows.Close()
				if stmt != nil {
					stmt.Close()
				}
			}
		}
	}
	// database/sql and the public driver expose no physical-origin field. Native
	// bound result-schema origin absence is asserted in the engine fixture, not
	// approximated from names or invented as a PostgreSQL metadata API.
}
