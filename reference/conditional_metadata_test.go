package reference_test

import (
	"math"
	"testing"
)

func TestReferenceConditionalMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT,v INT,b BOOL,a VARCHAR(3),c VARCHAR(3),d VARCHAR(4))", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,0,TRUE,'x','y','z'),(2,NULL,FALSE,NULL,'y','z')", 2))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, tc := range []struct {
				expr, name, typ        string
				nativeLength, pgLength int64
				rows                   [][]any
			}{
				{"COALESCE(a,c)", "coalesce", "VARCHAR", 3, 3, [][]any{{"x"}, {"y"}}},
				{"GREATEST(a,c)", "greatest", "VARCHAR", 3, 3, [][]any{{"y"}, {"y"}}},
				{"COALESCE(a,d)", "coalesce", "VARCHAR", math.MaxInt64, -5, [][]any{{"x"}, {"z"}}},
				{"CASE WHEN b THEN a ELSE c END", "c", "VARCHAR", 3, 3, [][]any{{"x"}, {"y"}}},
				{"CASE WHEN b THEN a END", "case", "VARCHAR", math.MaxInt64, -5, [][]any{{"x"}, {nil}}},
				{"NULLIF(a,c)", "nullif", "TEXT", math.MaxInt64, math.MaxInt64, [][]any{{"x"}, {nil}}},
			} {
				q := "SELECT " + tc.expr + " FROM t"
				held, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := held.Close(); err != nil {
						t.Error(err)
					}
				}()
				for _, prepared := range []bool{false, true} {
					rows, err := backend.db.QueryContext(p.ctx, q)
					if prepared {
						if rows != nil {
							rows.Close()
						}
						rows, err = held.QueryContext(p.ctx)
					}
					if err != nil {
						held.Close()
						t.Fatal(err)
					}
					defer rows.Close()
					metadata, err := rows.ColumnTypes()
					if err != nil || len(metadata) != 1 {
						rows.Close()
						held.Close()
						t.Fatal(metadata, err)
					}
					want := tc.nativeLength
					if backend.pg {
						want = tc.pgLength
					}
					if length, known := metadata[0].Length(); !known || length != want {
						rows.Close()
						held.Close()
						t.Fatalf("%s Length=(%d,%v), want (%d,true)", tc.expr, length, known, want)
					}
					if nullable, known := metadata[0].Nullable(); nullable || known {
						rows.Close()
						held.Close()
						t.Fatal("invented conditional nullability", nullable, known)
					}
					scalarRows(t, backend.pg, rows, []string{tc.name}, []string{tc.typ}, tc.rows, true)
				}
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
