package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func infoCardinalNumberScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			for _, q := range []string{
				"SET search_path TO public",
				"CREATE TABLE public.info_cardinal_number_witness(id integer,v information_schema.cardinal_number)",
			} {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
			}
			body(t, p, backend, conn, recorder)
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %#v", notices)
			}
		})
	}
}

func infoCardinalNumberBoth(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, s)
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(s))
}

func infoCardinalNumberSeed(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO public.info_cardinal_number_witness VALUES (1,0),(2,7),(3,NULL)", 3))
}

func infoCardinalNumberColumnTypes(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 1 {
		t.Fatalf("columns=%d want 1", len(columns))
	}
	c := columns[0]
	length, lengthOK := c.Length()
	precision, scale, decimalOK := c.DecimalSize()
	nullable, nullableOK := c.Nullable()
	wantScan := reflect.TypeOf(int64(0))
	if pg {
		wantScan = reflect.TypeOf(int32(0))
	}
	if c.Name() != "v" || c.DatabaseTypeName() != "INT4" || c.ScanType() != wantScan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
		t.Fatalf("metadata name=%q type=%q scan=%v length=(%d,%v) decimal=(%d,%d,%v) nullable=(%v,%v)", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
	}
}

func infoCardinalNumberRows(pg bool, query string, want [][]any) fixture.Step {
	s := fixture.Select(query, []string{"v"}, []string{"INT4"}, want)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		infoCardinalNumberColumnTypes(t, columns, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		t.Helper()
		if !reflect.DeepEqual(value, want[row][column]) {
			t.Fatalf("raw row %d column %d=%T(%v) want %T(%v)", row, column, value, value, want[row][column], want[row][column])
		}
		return true
	}
	return s
}

func infoCardinalNumberStored(pg bool) fixture.Step {
	return infoCardinalNumberRows(pg, "SELECT v FROM public.info_cardinal_number_witness ORDER BY id", [][]any{{int64(0)}, {int64(7)}, {nil}})
}

func infoCardinalNumberStoredIDs() fixture.Step {
	return fixture.Select("SELECT id,v FROM public.info_cardinal_number_witness ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(7)}, {int64(3), nil}})
}

func TestReferenceInfoCardinalNumberValues(t *testing.T) {
	infoCardinalNumberScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, recorder, s) }
				run(fixture.Exec("INSERT INTO public.info_cardinal_number_witness VALUES (1,0),(2,7),(3,2147483647),(4,NULL)", 4))
				run(fixture.Exec("INSERT INTO public.info_cardinal_number_witness VALUES (5,$1),(6,$2),(7,$3)", 3, int64(0), "7", nil))
				run(fixture.Exec("INSERT INTO public.info_cardinal_number_witness(id) VALUES (8)", 1))
				run(fixture.Select("SELECT id,v FROM public.info_cardinal_number_witness ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(7)}, {int64(3), int64(2147483647)}, {int64(4), nil}, {int64(5), int64(0)}, {int64(6), int64(7)}, {int64(7), nil}, {int64(8), nil}}))
				run(infoCardinalNumberRows(backend.PG, "SELECT v FROM public.info_cardinal_number_witness ORDER BY id", [][]any{{int64(0)}, {int64(7)}, {int64(2147483647)}, {nil}, {int64(0)}, {int64(7)}, {nil}, {nil}}))
				run(fixture.Exec("DELETE FROM public.info_cardinal_number_witness", 8))
			})
		}
		for i, tc := range []struct {
			expression string
			args       []any
			value      any
		}{
			{"CAST(7 AS information_schema.cardinal_number)", nil, int64(7)},
			{"7::information_schema.cardinal_number", nil, int64(7)},
			{"CAST($1::integer AS information_schema.cardinal_number)", []any{int64(7)}, int64(7)},
			{"($1::integer)::information_schema.cardinal_number", []any{int64(7)}, int64(7)},
			{"CAST($1::integer AS information_schema.cardinal_number)", []any{nil}, nil},
			{"($1::integer)::information_schema.cardinal_number", []any{nil}, nil},
		} {
			t.Run(fmt.Sprintf("cast=%d", i), func(t *testing.T) {
				s := infoCardinalNumberRows(backend.PG, "SELECT "+tc.expression+" AS v", [][]any{{tc.value}})
				s.CheckMetadata = nil
				s.Args = tc.args
				infoCardinalNumberBoth(t, p, conn, recorder, s)
			})
		}
	})
}
