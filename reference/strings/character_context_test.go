package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCharacterContextBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE seed(c CHAR(2))", 0))
	p.Step(t, fixture.Exec("INSERT INTO seed VALUES('a')", 1))
	for _, s := range []fixture.Step{
		fixture.Select("SELECT c FROM seed WHERE 1=1", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}}),
		fixture.Select("SELECT CAST(c AS text)='a',c='a   ',CAST('a ' AS text)=c FROM seed", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}}),
		fixture.Select("SELECT c FROM seed WHERE CAST(c AS text)='a' AND NOT CAST('a ' AS text)=c", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}}),
		fixture.Select("SELECT text(c),CAST(CAST('123 ' AS varchar) AS text)::int4 FROM seed", []string{"text", "int4"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(123)}}),
		fixture.Select("SELECT CAST(CAST(123 AS text) AS bpchar(4)) FROM seed", []string{"bpchar"}, []string{"BPCHAR"}, [][]any{{"123 "}}),
	} {
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	for _, tc := range []struct{ query, nativeCode, postgresCode string }{
		{`SELECT CAST('a' AS "VARCHAR") FROM seed`, "42704", "42704"},
		{`SELECT 'a'::"character" FROM seed`, "42704", "42704"},
	} {
		for _, backend := range p.Backends() {
			code := tc.nativeCode
			if backend.PG {
				code = tc.postgresCode
			}
			for _, prepared := range []bool{false, true} {
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
				var err error
				if prepared {
					stmt, e := backend.DB.PrepareContext(ctx, tc.query)
					err = e
					if stmt != nil {
						stmt.Close()
						t.Fatal("unsupported argument exposed statement")
					}
				} else {
					rows, e := backend.DB.QueryContext(ctx, tc.query)
					err = e
					if rows != nil {
						rows.Close()
						t.Fatal("unsupported argument exposed cursor")
					}
				}
				fixture.State(t, err, code)
			}
		}
	}
	p.Step(t, fixture.Select("SELECT c FROM seed", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}}))
}

func TestReferenceCharacterSelectedSingletonCastArguments(t *testing.T) {
	p := fixture.NewScenario(t)
	q := "SELECT num_nulls(CAST($1 AS bpchar),CAST($1 AS text)),num_nonnulls(CAST($1 AS bpchar),CAST($1 AS text))"
	for _, backend := range p.Backends() {
		func() {
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			for _, frame := range []struct {
				input any
				want  []any
			}{{"a  ", []any{int64(0), int64(2)}}, {nil, []any{int64(2), int64(0)}}, {"z ", []any{int64(0), int64(2)}}} {
				s := fixture.Select(q, []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{frame.want}, frame.input)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
			}
		}()
	}
}
