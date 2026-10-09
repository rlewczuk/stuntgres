package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceStringLiterals(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, p, backend.pg)
			if err := f.b.PingContext(p.ctx); err != nil {
				t.Fatal(err)
			}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, f.a, f.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT,label TEXT,flag BOOL)", 0))
			want := [][]any{}
			for i, tc := range stringLiteralValues() {
				for _, prepared := range []bool{false, true} {
					id := len(want) + 1
					s := referenceExec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s,TRUE)", id, tc.source), 1)
					s.prepared = prepared
					run(s)
					want = append(want, []any{int64(id), tc.text, true})
				}
				if tc.source == `$$a;'b$$` {
					s := referenceSelect(`SELECT id FROM t WHERE label=$$a;'b$$ ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2*i + 1)}, {int64(2*i + 2)}})
					run(s)
					run(referencePrepared(s))
				}
			}
			s := referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, want)
			referenceRunStep(t, p.ctx, f.b, f.recorder, s)
			rows, err := f.a.QueryContext(p.ctx, s.query)
			if err != nil {
				t.Fatal(err)
			}
			lexicalRows(t, backend.pg, rows, s, nil)
			run(referenceError("SELECT * FROM phantom", "42P01"))
			run(referencePrepared(referenceSelect(`SELECT id FROM t WHERE label=E'a\nb' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})))
			q := `INSERT INTO t VALUES ($1,$2,E'\164')`
			stmt, err := f.a.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: f.a, statement: stmt, query: q}
			for _, id := range []int64{100, 101} {
				referenceRunStep(t, p.ctx, held, f.recorder, referenceExec(q, 1, id, `a\nb`))
			}
			referenceRunStep(t, p.ctx, f.b, f.recorder, referenceSelect("SELECT label,flag FROM t WHERE id=100", []string{"label", "flag"}, []string{"TEXT", "BOOL"}, [][]any{{`a\nb`, true}}))
		})
	}
}

func TestReferenceStringLiteralTypedEscapes(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE inputs(id INT,big BIGINT,flag BOOL)", 0))
	for _, prepared := range []bool{false, true} {
		s := referenceExec(`INSERT INTO inputs VALUES (E'\061',E'\062',E'\164')`, 1)
		s.prepared = prepared
		p.step(t, s)
	}
	sentinel := referenceSelect("SELECT * FROM inputs", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(1), int64(2), true}, {int64(1), int64(2), true}})
	sentinel.bag = true
	p.step(t, sentinel)
	for _, tc := range []struct{ column, typ string }{{"id", "integer"}, {"big", "bigint"}, {"flag", "boolean"}} {
		q := "INSERT INTO inputs (" + tc.column + ") VALUES ('1'),(E'\\x71')"
		position := referencePosition(len("INSERT INTO inputs ("+tc.column+") VALUES ('1'),(") + 1)
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type " + tc.typ + `: "q"`, Position: position})
			s.prepared = prepared
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			p.step(t, s)
			p.step(t, sentinel)
		}
	}
}
