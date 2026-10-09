package reference_test

import (
	"context"
	"reflect"
	"testing"
)

func TestReferenceSmallintEndpointsAndMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE typed(s SMALLINT NOT NULL,n int2)", 0))
	p.step(t, referenceExec("INSERT INTO typed VALUES(-32768,NULL),(32767,NULL)", 2))
	step := referenceSelect("SELECT * FROM typed ORDER BY s", []string{"s", "n"}, []string{"INT2", "INT2"}, [][]any{{int64(-32768), nil}, {int64(32767), nil}})
	p.step(t, step)
	p.step(t, referencePrepared(step))
	for _, backend := range p.backends() {
		for _, q := range []string{"SELECT * FROM typed ORDER BY s", "SELECT * FROM typed WHERE s=0"} {
			for _, prepared := range []bool{false, true} {
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
				func() {
					var executor referenceExecutor = backend.db
					if prepared {
						stmt, err := backend.db.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
					}
					rows, err := executor.QueryContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					types, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for i, column := range types {
						scan := reflect.TypeFor[int64]()
						if backend.pg {
							scan = reflect.TypeFor[int16]()
						}
						nullable, known := column.Nullable()
						length, hasLength := column.Length()
						precision, scale, sized := column.DecimalSize()
						if column.DatabaseTypeName() != "INT2" || column.ScanType() != scan || known != !backend.pg || nullable != (!backend.pg && i == 1) || hasLength || length != 0 || sized || precision != 0 || scale != 0 {
							t.Fatal(column, nullable, known, length, hasLength, precision, scale, sized)
						}
					}
					for rows.Next() {
						var s, n any
						if err := rows.Scan(&s, &n); err != nil {
							t.Fatal(err)
						}
						referenceAssertRawValue(t, backend.pg, "INT2", s)
						if n != nil {
							t.Fatal(n)
						}
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					want := step
					want.query = q
					if q == "SELECT * FROM typed WHERE s=0" {
						want.rows = nil
					}
					referenceRunStep(t, p.ctx, executor, backend.recorder, want)
				}()
			}
		}
	}
}

func TestReferenceSmallintHeldFrames(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE typed(s SMALLINT,n int2)", 0))
	p.step(t, referenceExec("CREATE TABLE repeats(a SMALLINT,b SMALLINT)", 0))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			q := "INSERT INTO typed VALUES($1,$2)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			owned := []byte("-32768")
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, owned, nil))
			owned[0] = '0'
			for _, raw := range []any{int64(32767), "0x_FF", " +0007 ", "010", "08", "0o17", "0b10_10", "1_000", "-0X_10", "-0", "\t\n\r\f\v-8\t"} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, raw, nil))
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO typed(s) VALUES(9)", 1))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT * FROM typed ORDER BY s", []string{"s", "n"}, []string{"INT2", "INT2"}, [][]any{{int64(-32768), nil}, {int64(-16), nil}, {int64(-8), nil}, {int64(0), nil}, {int64(7), nil}, {int64(8), nil}, {int64(9), nil}, {int64(10), nil}, {int64(10), nil}, {int64(15), nil}, {int64(255), nil}, {int64(1000), nil}, {int64(32767), nil}}))
			q = "INSERT INTO repeats VALUES($1,$1)"
			repeat, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer repeat.Close()
			reused := referenceHeldStatement{referenceExecutor: backend.db, statement: repeat, query: q}
			for _, n := range []int{7, 8} {
				referenceRunStep(t, p.ctx, reused, backend.recorder, referenceExec(q, 1, n))
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT * FROM repeats ORDER BY a", []string{"a", "b"}, []string{"INT2", "INT2"}, [][]any{{int64(7), int64(7)}, {int64(8), int64(8)}}))
		})
	}
}
