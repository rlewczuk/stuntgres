package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmallintEndpointsAndMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE typed(s SMALLINT NOT NULL,n int2)", 0))
	p.Step(t, fixture.Exec("INSERT INTO typed VALUES(-32768,NULL),(32767,NULL)", 2))
	step := fixture.Select("SELECT * FROM typed ORDER BY s", []string{"s", "n"}, []string{"INT2", "INT2"}, [][]any{{int64(-32768), nil}, {int64(32767), nil}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	for _, backend := range p.Backends() {
		for _, q := range []string{"SELECT * FROM typed ORDER BY s", "SELECT * FROM typed WHERE s=0"} {
			for _, prepared := range []bool{false, true} {
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
				func() {
					var executor fixture.Executor = backend.DB
					if prepared {
						stmt, err := backend.DB.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
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
						if backend.PG {
							scan = reflect.TypeFor[int16]()
						}
						nullable, known := column.Nullable()
						length, hasLength := column.Length()
						precision, scale, sized := column.DecimalSize()
						if column.DatabaseTypeName() != "INT2" || column.ScanType() != scan || known != !backend.PG || nullable != (!backend.PG && i == 1) || hasLength || length != 0 || sized || precision != 0 || scale != 0 {
							t.Fatal(column, nullable, known, length, hasLength, precision, scale, sized)
						}
					}
					for rows.Next() {
						var s, n any
						if err := rows.Scan(&s, &n); err != nil {
							t.Fatal(err)
						}
						fixture.AssertRawValue(t, backend.PG, "INT2", s)
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
					want.Query = q
					if q == "SELECT * FROM typed WHERE s=0" {
						want.Rows = nil
					}
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, want)
				}()
			}
		}
	}
}

func TestReferenceSmallintHeldFrames(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE typed(s SMALLINT,n int2)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE repeats(a SMALLINT,b SMALLINT)", 0))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			q := "INSERT INTO typed VALUES($1,$2)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			owned := []byte("-32768")
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, owned, nil))
			owned[0] = '0'
			for _, raw := range []any{int64(32767), "0x_FF", " +0007 ", "010", "08", "0o17", "0b10_10", "1_000", "-0X_10", "-0", "\t\n\r\f\v-8\t"} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, raw, nil))
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO typed(s) VALUES(9)", 1))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT * FROM typed ORDER BY s", []string{"s", "n"}, []string{"INT2", "INT2"}, [][]any{{int64(-32768), nil}, {int64(-16), nil}, {int64(-8), nil}, {int64(0), nil}, {int64(7), nil}, {int64(8), nil}, {int64(9), nil}, {int64(10), nil}, {int64(10), nil}, {int64(15), nil}, {int64(255), nil}, {int64(1000), nil}, {int64(32767), nil}}))
			q = "INSERT INTO repeats VALUES($1,$1)"
			repeat, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer repeat.Close()
			reused := fixture.HeldStatement{Executor: backend.DB, Statement: repeat, SQL: q}
			for _, n := range []int{7, 8} {
				fixture.RunStep(t, p.Ctx, reused, backend.Recorder, fixture.Exec(q, 1, n))
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT * FROM repeats ORDER BY a", []string{"a", "b"}, []string{"INT2", "INT2"}, [][]any{{int64(7), int64(7)}, {int64(8), int64(8)}}))
		})
	}
}
