package reference_test

import (
	"context"
	"testing"
)

func TestReferenceCharacterContextBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE seed(c CHAR(2))", 0))
	p.step(t, referenceExec("INSERT INTO seed VALUES('a')", 1))
	for _, s := range []referenceStep{
		referenceSelect("SELECT c FROM seed WHERE 1=1", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}}),
		referenceSelect("SELECT CAST(c AS text)='a',c='a   ',CAST('a ' AS text)=c FROM seed", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}}),
		referenceSelect("SELECT c FROM seed WHERE CAST(c AS text)='a' AND NOT CAST('a ' AS text)=c", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}}),
		referenceSelect("SELECT text(c),CAST(CAST('123 ' AS varchar) AS text)::int4 FROM seed", []string{"text", "int4"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(123)}}),
		referenceSelect("SELECT CAST(CAST(123 AS text) AS bpchar(4)) FROM seed", []string{"bpchar"}, []string{"BPCHAR"}, [][]any{{"123 "}}),
	} {
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct{ query, nativeCode, postgresCode string }{
		{`SELECT CAST('a' AS "VARCHAR") FROM seed`, "42704", "42704"},
		{`SELECT 'a'::"character" FROM seed`, "42704", "42704"},
	} {
		for _, backend := range p.backends() {
			code := tc.nativeCode
			if backend.pg {
				code = tc.postgresCode
			}
			for _, prepared := range []bool{false, true} {
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
				var err error
				if prepared {
					stmt, e := backend.db.PrepareContext(ctx, tc.query)
					err = e
					if stmt != nil {
						stmt.Close()
						t.Fatal("unsupported argument exposed statement")
					}
				} else {
					rows, e := backend.db.QueryContext(ctx, tc.query)
					err = e
					if rows != nil {
						rows.Close()
						t.Fatal("unsupported argument exposed cursor")
					}
				}
				referenceState(t, err, code)
			}
		}
	}
	p.step(t, referenceSelect("SELECT c FROM seed", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}}))
}

func TestReferenceCharacterSelectedSingletonCastArguments(t *testing.T) {
	p := newReferenceScenario(t)
	q := "SELECT num_nulls(CAST($1 AS bpchar),CAST($1 AS text)),num_nonnulls(CAST($1 AS bpchar),CAST($1 AS text))"
	for _, backend := range p.backends() {
		func() {
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for _, frame := range []struct {
				input any
				want  []any
			}{{"a  ", []any{int64(0), int64(2)}}, {nil, []any{int64(2), int64(0)}}, {"z ", []any{int64(0), int64(2)}}} {
				s := referenceSelect(q, []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{frame.want}, frame.input)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				referenceRunStep(t, p.ctx, held, backend.recorder, s)
			}
		}()
	}
}
