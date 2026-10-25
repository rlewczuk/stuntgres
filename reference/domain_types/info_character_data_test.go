package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func infoCharacterDataScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			for _, q := range []string{
				"SET search_path TO public",
				"CREATE TABLE public.info_character_data_witness(id integer,v information_schema.character_data)",
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

func infoCharacterDataBoth(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, s)
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(s))
}

func infoCharacterDataSeed(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder, prepared bool) [][]any {
	t.Helper()
	long := strings.Repeat("x", 80)
	for _, s := range []fixture.Step{
		fixture.Exec("INSERT INTO public.info_character_data_witness VALUES (1,'ordinary'),(2,''),(3,'tail  '),(4,'é🙂'),(5,'"+long+"'),(6,NULL)", 6),
		fixture.Exec("INSERT INTO public.info_character_data_witness VALUES (7,$1),(8,$2)", 2, "ordinary", nil),
		fixture.Exec("INSERT INTO public.info_character_data_witness(id) VALUES (9)", 1),
	} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, conn, recorder, s)
	}
	return [][]any{{int64(1), "ordinary"}, {int64(2), ""}, {int64(3), "tail  "}, {int64(4), "é🙂"}, {int64(5), long}, {int64(6), nil}, {int64(7), "ordinary"}, {int64(8), nil}, {int64(9), nil}}
}

func infoCharacterDataStored(want [][]any) fixture.Step {
	return fixture.Select("SELECT id,v FROM public.info_character_data_witness ORDER BY id", []string{"id", "v"}, []string{"INT4", "VARCHAR"}, want)
}

func infoCharacterDataRows(pg bool, query string, want [][]any) fixture.Step {
	s := fixture.Select(query, []string{"v"}, []string{"VARCHAR"}, want)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		infoCharacterDataColumnTypes(t, columns, pg, true)
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

func infoCharacterDataRead(t *testing.T, p *fixture.Pair, pg bool, conn fixture.Executor, recorder *fixture.Recorder, want [][]any) {
	t.Helper()
	infoCharacterDataBoth(t, p, conn, recorder, infoCharacterDataStored(want))
	values := make([][]any, len(want))
	for i, row := range want {
		values[i] = []any{row[1]}
	}
	infoCharacterDataBoth(t, p, conn, recorder, infoCharacterDataRows(pg, "SELECT v FROM public.info_character_data_witness ORDER BY id", values))
}

func TestReferenceInfoCharacterDataValues(t *testing.T) {
	infoCharacterDataScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
				want := infoCharacterDataSeed(t, p, conn, recorder, prepared)
				infoCharacterDataRead(t, p, backend.PG, conn, recorder, want)
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DELETE FROM public.info_character_data_witness", 9))
			})
		}
		for i, tc := range []struct {
			expression string
			args       []any
			value      any
		}{
			{"'tail  '::information_schema.character_data", nil, "tail  "},
			{"CAST('é🙂' AS information_schema.character_data)", nil, "é🙂"},
			{"$1::information_schema.character_data", []any{strings.Repeat("x", 80)}, strings.Repeat("x", 80)},
			{"CAST($1 AS information_schema.character_data)", []any{""}, ""},
			{"$1::information_schema.character_data", []any{nil}, nil},
			{"CAST(NULL AS information_schema.character_data)", nil, nil},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("expression=%d/prepared=%v", i, prepared), func(t *testing.T) {
					s := infoCharacterDataRows(backend.PG, "SELECT "+tc.expression+" AS v", [][]any{{tc.value}})
					// Expression nullability is separate from declared-column metadata.
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						columns, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						infoCharacterDataColumnTypes(t, columns, backend.PG, false)
					}
					s.Args, s.Prepared = tc.args, prepared
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
				})
			}
		}
	})
}
