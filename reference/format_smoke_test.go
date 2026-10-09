package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceFormatSmoke(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			call string
			want any
		}{
			{"format('%2$s/%s/%1$s','a','b','c')", "b/c/a"},
			{"format('|%1$*2$s|%s','x',5,'y')", "|    x|5"},
			{"format('|%3s|%I|%L|%L|','é','select','O''Reilly',E'a\\\\b')", `|  é|"select"|'O''Reilly'|E'a\\b'|`},
			{"format('%s/%s',true,'x'::char(3))", "t/x  "},
			{"format(NULL)", nil}, {"format('%s/%L',NULL,NULL)", "/NULL"},
			{`format('%s','{a,NULL,"NULL"}'::name[])`, `{a,NULL,"NULL"}`},
			{`format('%s/%L/%s',VARIADIC '{a,NULL,"NULL"}'::name[])`, "a/NULL/NULL"},
			{"format('ok',VARIADIC NULL::name[])", "ok"},
			{`format('%s/%s',VARIADIC '{1,2}'::integer[])`, "1/2"},
			{`format('%s/%s',VARIADIC '{true,false}'::boolean[])`, "t/f"},
			{`format('|%1$*2$s|',VARIADIC '{7,4}'::integer[])`, "|   7|"},
		} {
			t.Run(tc.call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(tc.call, tc.want)) })
		}
		formatReferenceFailure(t, p, b, c, "SELECT format('%I',NULL)", controlsReferenceDiagnostic("22004", "null values cannot be formatted as an SQL identifier"), false)
		q := "SELECT format($1,$2::text,$3::text,$4::text) AS rendered"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal("quiet smoke Prepare", err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct{ pattern, a, b, c, want string }{
			{"%s", "x", "", "", "x"},
			{"%2$s/%s/%1$s", "a", "b", "c", "b/c/a"},
			{"|%1$*2$s|%s", "x", "5", "y", "|    x|5"},
			{"|%3s|%I|%L", "é", "select", "O'Reilly", `|  é|"select"|'O''Reilly'`},
		} {
			step := referenceSelect(q, []string{"rendered"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.pattern, tc.a, tc.b, tc.c)
			referenceRunStep(t, p.ctx, c, b.recorder, step)
			referenceRunStep(t, p.ctx, held, b.recorder, step)
		}
		d := controlsReferenceDiagnostic("22023", `unrecognized format() type specifier "d"`)
		d.Hint = `For a single "%" use "%%".`
		s := referenceErrorDiagnostic(q, d, "%*d", "bad", "x", "unused")
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"rendered"}, []string{"TEXT"}, [][]any{{nil}}, nil, "unused", "unused", "unused"))
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE format_smoke(id int,v text DEFAULT (format('%s:%s',0,'default')),CONSTRAINT format_smoke_check CHECK(format('%s',v)<>'bad'))", 0))
		empty := referenceSelect("SELECT format('%s',v) AS rendered FROM format_smoke", []string{"rendered"}, []string{"TEXT"}, [][]any{})
		empty.checkMetadata = func(t *testing.T, rows *sql.Rows) { formatReferenceMetadata(t, b.pg, rows, false) }
		formatReferenceBoth(t, p, b, c, empty)
		run(referenceExec("INSERT INTO format_smoke(id) VALUES(1)", 1))
		run(referencePrepared(referenceExec("INSERT INTO format_smoke VALUES(2,format('%I','select'))", 1)))
		run(referenceSelect("UPDATE format_smoke SET v=format('%s:%s',id,'updated') WHERE id=1 RETURNING format('%s',v) AS rendered", []string{"rendered"}, []string{"TEXT"}, [][]any{{"1:updated"}}))
		d = controlsReferenceDiagnostic("23514", `new row for relation "format_smoke" violates check constraint "format_smoke_check"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, bad).", "public", "format_smoke", "format_smoke_check"
		s = referenceErrorDiagnostic("INSERT INTO format_smoke VALUES(3,format('%s','bad'))", d)
		s.errorStage = "execute"
		run(s)
		run(referenceExec("CREATE TABLE format_smoke_source(id int,p text,v text)", 0))
		run(referenceExec("INSERT INTO format_smoke_source VALUES(3,'%s','first'),(4,'%I',NULL)", 2))
		d = controlsReferenceDiagnostic("22004", "null values cannot be formatted as an SQL identifier")
		s = referencePrepared(referenceErrorDiagnostic("INSERT INTO format_smoke SELECT id,format(p,v) FROM format_smoke_source ORDER BY id", d))
		s.errorStage = "execute"
		run(s)
		run(referenceExec("CREATE VIEW format_smoke_view AS SELECT id,format('%s',v) AS rendered FROM format_smoke", 0))
		want := referenceSelect("SELECT id,rendered FROM format_smoke_view ORDER BY id", []string{"id", "rendered"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1:updated"}, {int64(2), `"select"`}})
		formatReferenceBoth(t, p, b, c, want)
		formatReferenceControlPhase(t, p, b, c, want)
	})
}
