package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitstringRepeatedVaryingWidths(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE bs_repeated_width(a VARBIT(2),b VARBIT(5))", 0))
		q := "INSERT INTO bs_repeated_width(a,b) VALUES($1,$1)"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			var want [][]any
			check := func() {
				s := fixture.Select("SELECT a,b FROM bs_repeated_width", []string{"a", "b"}, []string{"VARBIT", "VARBIT"}, want)
				s.Bag = true
				s.CheckMetadata = bitstringReferenceMetadata(backend.PG, []int64{2, 5}, []bool{true, true})
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			for _, executor := range []fixture.Executor{backend.DB, held} {
				for _, good := range []string{"01", ""} {
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Exec(q, 1, good))
					want = append(want, []any{good, good})
				}
				fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Exec(q, 1, nil))
				want = append(want, []any{nil, nil})
				check()
				// Both values fit the wider occurrence. Only the narrow destination
				// can reject them; sizing must not be lost when the slot is shared.
				for _, bad := range []string{"001", "10101"} {
					bitstringReferenceError(t, p, backend, executor, q, []any{bad}, false, false, "22001", "bit string too long for type bit varying(2)", "")
					check()
				}
				fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Exec(q, 1, "10"))
				want = append(want, []any{"10", "10"})
				check()
			}
		}()
	}
}

func TestReferenceBitstringHeldSelectArguments(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE bs_held_args(id INTEGER,f BIT(3) NOT NULL,v VARBIT(5),u VARBIT)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO bs_held_args VALUES(1,B'001',B'',X'0f'),(2,B'100',NULL,B'000')", 2))
		q := "SELECT f,v,u FROM bs_held_args WHERE id >= $1 ORDER BY id"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			metadata := bitstringReferenceMetadata(backend.PG, []int64{3, 5, -1}, []bool{false, true, true})
			check := func(arg any, want [][]any) {
				s := fixture.Select(q, []string{"f", "v", "u"}, []string{"BIT", "VARBIT", "VARBIT"}, want, arg)
				s.CheckMetadata = metadata
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
			}
			first := [][]any{{"001", "", "00001111"}, {"100", nil, "000"}}
			var retained *sql.Rows
			if backend.PG {
				check(1, first)
			} else {
				retained, err = stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), 1)
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				metadata(t, retained)
			}
			check(2, [][]any{{"100", nil, "000"}})
			check(nil, nil)
			check(99, nil)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO bs_held_args VALUES(3,B'010',B'01',B'')", 1))
			check(3, [][]any{{"010", "01", ""}})
			if retained != nil {
				types, err := retained.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range first {
					if !retained.Next() {
						t.Fatal("retained parameterized row missing", retained.Err())
					}
					values := make([]any, 3)
					if err := retained.Scan(&values[0], &values[1], &values[2]); err != nil {
						t.Fatal(err)
					}
					for i, value := range values {
						fixture.AssertRawValue(t, false, []string{"BIT", "VARBIT", "VARBIT"}[i], value)
					}
					if !reflect.DeepEqual(values, want) {
						t.Fatal("retained parameterized frame changed", values, want)
					}
				}
				if retained.Next() || retained.Err() != nil {
					t.Fatal("retained cursor includes later execution/insert", retained.Err())
				}
				// database/sql closes Rows at EOF. Inspect the retained ColumnType
				// descriptors below rather than re-querying metadata on closed Rows.
				for i, typ := range types {
					if typ.Name() != []string{"f", "v", "u"}[i] || typ.DatabaseTypeName() != []string{"BIT", "VARBIT", "VARBIT"}[i] || typ.ScanType() != reflect.TypeFor[string]() {
						t.Fatal("captured type changed", i, typ)
					}
					if width, known := typ.Length(); !known || width != []int64{3, 5, -1}[i] {
						t.Fatal("captured width changed", i, width, known)
					}
					if precision, scale, known := typ.DecimalSize(); known {
						t.Fatal("captured decimal metadata changed", i, precision, scale)
					}
					if nullable, known := typ.Nullable(); !known || nullable != (i != 0) {
						t.Fatal("captured nullability changed", i, nullable, known)
					}
				}
			}
		}()
	}
}
