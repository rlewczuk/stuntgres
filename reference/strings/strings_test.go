package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringLiterals(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
			if err := f.B.PingContext(p.Ctx); err != nil {
				t.Fatal(err)
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, f.A, f.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,label TEXT,flag BOOL)", 0))
			want := [][]any{}
			for i, tc := range stringLiteralValues() {
				for _, prepared := range []bool{false, true} {
					id := len(want) + 1
					s := fixture.Exec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s,TRUE)", id, tc.source), 1)
					s.Prepared = prepared
					run(s)
					want = append(want, []any{int64(id), tc.text, true})
				}
				if tc.source == `$$a;'b$$` {
					s := fixture.Select(`SELECT id FROM t WHERE label=$$a;'b$$ ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2*i + 1)}, {int64(2*i + 2)}})
					run(s)
					run(fixture.Prepared(s))
				}
			}
			s := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, want)
			fixture.RunStep(t, p.Ctx, f.B, f.Recorder, s)
			rows, err := f.A.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, s, nil)
			run(fixture.Error("SELECT * FROM phantom", "42P01"))
			run(fixture.Prepared(fixture.Select(`SELECT id FROM t WHERE label=E'a\nb' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})))
			q := `INSERT INTO t VALUES ($1,$2,E'\164')`
			stmt, err := f.A.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: f.A, Statement: stmt, SQL: q}
			for _, id := range []int64{100, 101} {
				fixture.RunStep(t, p.Ctx, held, f.Recorder, fixture.Exec(q, 1, id, `a\nb`))
			}
			fixture.RunStep(t, p.Ctx, f.B, f.Recorder, fixture.Select("SELECT label,flag FROM t WHERE id=100", []string{"label", "flag"}, []string{"TEXT", "BOOL"}, [][]any{{`a\nb`, true}}))
		})
	}
}

func TestReferenceStringLiteralTypedEscapes(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE inputs(id INT,big BIGINT,flag BOOL)", 0))
	for _, prepared := range []bool{false, true} {
		s := fixture.Exec(`INSERT INTO inputs VALUES (E'\061',E'\062',E'\164')`, 1)
		s.Prepared = prepared
		p.Step(t, s)
	}
	sentinel := fixture.Select("SELECT * FROM inputs", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(1), int64(2), true}, {int64(1), int64(2), true}})
	sentinel.Bag = true
	p.Step(t, sentinel)
	for _, tc := range []struct{ column, typ string }{{"id", "integer"}, {"big", "bigint"}, {"flag", "boolean"}} {
		q := "INSERT INTO inputs (" + tc.column + ") VALUES ('1'),(E'\\x71')"
		position := fixture.Position(len("INSERT INTO inputs ("+tc.column+") VALUES ('1'),(") + 1)
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type " + tc.typ + `: "q"`, Position: position})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
			p.Step(t, sentinel)
		}
	}
}
