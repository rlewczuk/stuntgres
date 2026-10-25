package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func infoYesOrNoScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			for _, q := range []string{
				"SET search_path TO public",
				"CREATE TABLE public.info_yes_or_no_witness(id integer,v information_schema.yes_or_no)",
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

func infoYesOrNoBoth(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, s)
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(s))
}

func infoYesOrNoSeed(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO public.info_yes_or_no_witness VALUES (1,'YES'),(2,'NO'),(3,NULL)", 3))
}

func infoYesOrNoColumnTypes(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 1 {
		t.Fatalf("columns=%d want 1", len(columns))
	}
	c := columns[0]
	length, lengthOK := c.Length()
	precision, scale, decimalOK := c.DecimalSize()
	nullable, nullableOK := c.Nullable()
	if c.Name() != "v" || c.DatabaseTypeName() != "VARCHAR" || c.ScanType() != reflect.TypeOf("") || length != 3 || !lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
		t.Fatalf("metadata name=%q type=%q scan=%v length=(%d,%v) decimal=(%d,%d,%v) nullable=(%v,%v); want v VARCHAR string (3,true) (0,0,false) (%v,%v)", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK, !pg, !pg)
	}
}

func infoYesOrNoRows(pg bool, query string, want [][]any) fixture.Step {
	s := fixture.Select(query, []string{"v"}, []string{"VARCHAR"}, want)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		infoYesOrNoColumnTypes(t, columns, pg)
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

func infoYesOrNoStored(pg bool) fixture.Step {
	return infoYesOrNoRows(pg, "SELECT v FROM public.info_yes_or_no_witness ORDER BY id", [][]any{{"YES"}, {"NO"}, {nil}})
}

func infoYesOrNoStoredIDs() fixture.Step {
	return fixture.Select("SELECT id,v FROM public.info_yes_or_no_witness ORDER BY id", []string{"id", "v"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(1), "YES"}, {int64(2), "NO"}, {int64(3), nil}})
}

func TestReferenceInfoYesOrNoValues(t *testing.T) {
	infoYesOrNoScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
				}
				run(fixture.Exec("INSERT INTO public.info_yes_or_no_witness VALUES (1,'YES'),(2,'NO'),(3,NULL)", 3))
				run(fixture.Exec("INSERT INTO public.info_yes_or_no_witness VALUES (4,$1),(5,$2),(6,$3)", 3, "YES", "NO", nil))
				run(fixture.Exec("INSERT INTO public.info_yes_or_no_witness(id) VALUES (7)", 1))
				run(fixture.Exec("INSERT INTO public.info_yes_or_no_witness VALUES (8,'YES   ')", 1))
				run(fixture.Select("SELECT id,v FROM public.info_yes_or_no_witness ORDER BY id", []string{"id", "v"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(1), "YES"}, {int64(2), "NO"}, {int64(3), nil}, {int64(4), "YES"}, {int64(5), "NO"}, {int64(6), nil}, {int64(7), nil}, {int64(8), "YES"}}))
				run(infoYesOrNoRows(backend.PG, "SELECT v FROM public.info_yes_or_no_witness ORDER BY id", [][]any{{"YES"}, {"NO"}, {nil}, {"YES"}, {"NO"}, {nil}, {nil}, {"YES"}}))
				run(fixture.Exec("DELETE FROM public.info_yes_or_no_witness", 8))
			})
		}
		// Explicit conversion truncates; assignment (tested separately) must not.
		for i, tc := range []struct {
			expression string
			args       []any
		}{
			{"CAST('YESx' AS information_schema.yes_or_no)", nil},
			{"'YESx'::information_schema.yes_or_no", nil},
			{"CAST($1::varchar AS information_schema.yes_or_no)", []any{"YESx"}},
			{"($1::varchar)::information_schema.yes_or_no", []any{"YESx"}},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("explicit=%d/prepared=%v", i, prepared), func(t *testing.T) {
					s := infoYesOrNoRows(backend.PG, "SELECT "+tc.expression+" AS v", [][]any{{"YES"}})
					// Expression nullability is not the declared-column contract.
					s.CheckMetadata = nil
					s.Args, s.Prepared = tc.args, prepared
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
				})
			}
		}
	})
}
