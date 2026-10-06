//go:build reference

package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

func TestReferenceByteaNullAndEmptyMetadata(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db *sql.DB
		pg bool
	}{{p.memory, false}, {p.postgres, true}} {
		for _, tc := range []struct {
			name, decl string
			nullable   bool
			nullRow    bool
		}{
			{"empty", "BYTEA", true, false}, {"nulls", "BYTEA", true, true}, {"required", "BYTEA NOT NULL", false, false},
		} {
			if _, err := backend.db.ExecContext(p.ctx, "CREATE TABLE "+tc.name+"(v "+tc.decl+")"); err != nil {
				t.Fatal(err)
			}
			if tc.nullRow {
				if _, err := backend.db.ExecContext(p.ctx, "INSERT INTO "+tc.name+" VALUES(NULL)"); err != nil {
					t.Fatal(err)
				}
			}
			for _, prepared := range []bool{false, true} {
				q := "SELECT v FROM " + tc.name
				var rows *sql.Rows
				var err error
				if prepared {
					s, e := backend.db.PrepareContext(p.ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					rows, err = s.QueryContext(p.ctx)
					s.Close()
				} else {
					rows, err = backend.db.QueryContext(p.ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				check := func() {
					t.Helper()
					c := types[0]
					if c.Name() != "v" || c.DatabaseTypeName() != "BYTEA" || c.ScanType() != reflect.TypeFor[[]byte]() {
						t.Fatal(c)
					}
					n, k := c.Nullable()
					if backend.pg {
						if k {
							t.Fatal(n, k)
						}
					} else if !k || n != tc.nullable {
						t.Fatal(n, k)
					}
					l, k := c.Length()
					if backend.pg {
						if !k || l != math.MaxInt64 {
							t.Fatal(l, k)
						}
					} else if k {
						t.Fatal(l, k)
					}
					if _, _, k := c.DecimalSize(); k {
						t.Fatal("decimal")
					}
				}
				check()
				n := 0
				for rows.Next() {
					var v any
					if err := rows.Scan(&v); err != nil || v != nil {
						t.Fatal(v, err)
					}
					n++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				check()
				rows.Close()
				check()
				want := 0
				if tc.nullRow {
					want = 1
				}
				if n != want {
					t.Fatal(n, want)
				}
			}
		}
	}
}
