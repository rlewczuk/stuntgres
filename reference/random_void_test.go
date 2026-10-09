package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceRandomVoid(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			for _, tc := range []struct {
				q    string
				want []any
			}{
				{"SELECT setseed(0),setseed(NULL)", []any{"", nil}},
				{"SELECT setseed(0)", []any{""}},
				{"SELECT setseed(NULL)", []any{nil}},
			} {
				for _, prepared := range []bool{false, true} {
					ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, e := b.db.PrepareContext(ctx, tc.q)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = b.db.QueryContext(ctx, tc.q)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					labels, err := rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != len(tc.want) {
						t.Fatal(types, err)
					}
					names := make([]string, len(types))
					typeNames := make([]string, len(types))
					for i, typ := range types {
						names[i] = "setseed"
						typeNames[i] = "VOID"
						if typ.Name() != names[i] || typ.DatabaseTypeName() != typeNames[i] || labels[i] != names[i] {
							t.Fatal(typ, labels)
						}
						want := reflect.TypeFor[string]()
						if b.pg {
							want = reflect.TypeFor[any]()
						}
						if typ.ScanType() != want {
							t.Fatal("VOID ScanType", typ.ScanType(), want)
						}
						if _, ok := typ.Length(); ok {
							t.Fatal("VOID length")
						}
						if _, _, ok := typ.DecimalSize(); ok {
							t.Fatal("VOID decimal size")
						}
					}
					if !rows.Next() {
						t.Fatal("VOID missing", rows.Err())
					}
					values := make([]any, len(types))
					dest := make([]any, len(types))
					for i := range values {
						dest[i] = &values[i]
					}
					if err := rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					for i, v := range values {
						referenceAssertRawValue(t, b.pg, "VOID", v)
						if tc.want[i] == nil {
							if v != nil {
								t.Fatal("VOID NULL", v)
							}
						} else if b.pg {
							raw, ok := v.([]byte)
							if !ok || raw == nil || len(raw) != 0 {
								t.Fatalf("pq nonnull VOID %T %#v", v, v)
							}
						} else if v != "" {
							t.Fatal("native nonnull VOID", v)
						}
						values[i] = referenceNormalize(v)
					}
					if !reflect.DeepEqual(values, tc.want) {
						t.Fatal(values, tc.want)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("VOID exhaustion", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					randomRecorded(t, b, tc.q, names, typeNames, [][]any{values})
				}
			}
		})
	}
}
