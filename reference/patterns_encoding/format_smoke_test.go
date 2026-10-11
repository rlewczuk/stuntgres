package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatSmoke(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
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
		formatReferenceFailure(t, p, b, c, "SELECT format('%I',NULL)", fixture.ControlsReferenceDiagnostic("22004", "null values cannot be formatted as an SQL identifier"), false)
		q := "SELECT format($1,$2::text,$3::text,$4::text) AS rendered"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("quiet smoke Prepare", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ pattern, a, b, c, want string }{
			{"%s", "x", "", "", "x"},
			{"%2$s/%s/%1$s", "a", "b", "c", "b/c/a"},
			{"|%1$*2$s|%s", "x", "5", "y", "|    x|5"},
			{"|%3s|%I|%L", "é", "select", "O'Reilly", `|  é|"select"|'O''Reilly'`},
		} {
			step := fixture.Select(q, []string{"rendered"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.pattern, tc.a, tc.b, tc.c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		d := fixture.ControlsReferenceDiagnostic("22023", `unrecognized format() type specifier "d"`)
		d.Hint = `For a single "%" use "%%".`
		s := fixture.ErrorDiagnostic(q, d, "%*d", "bad", "x", "unused")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"rendered"}, []string{"TEXT"}, [][]any{{nil}}, nil, "unused", "unused", "unused"))
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE format_smoke(id int,v text DEFAULT (format('%s:%s',0,'default')),CONSTRAINT format_smoke_check CHECK(format('%s',v)<>'bad'))", 0))
		empty := fixture.Select("SELECT format('%s',v) AS rendered FROM format_smoke", []string{"rendered"}, []string{"TEXT"}, [][]any{})
		empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.FormatReferenceMetadata(t, b.PG, rows, false) }
		formatReferenceBoth(t, p, b, c, empty)
		run(fixture.Exec("INSERT INTO format_smoke(id) VALUES(1)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO format_smoke VALUES(2,format('%I','select'))", 1)))
		run(fixture.Select("UPDATE format_smoke SET v=format('%s:%s',id,'updated') WHERE id=1 RETURNING format('%s',v) AS rendered", []string{"rendered"}, []string{"TEXT"}, [][]any{{"1:updated"}}))
		d = fixture.ControlsReferenceDiagnostic("23514", `new row for relation "format_smoke" violates check constraint "format_smoke_check"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, bad).", "public", "format_smoke", "format_smoke_check"
		s = fixture.ErrorDiagnostic("INSERT INTO format_smoke VALUES(3,format('%s','bad'))", d)
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("CREATE TABLE format_smoke_source(id int,p text,v text)", 0))
		run(fixture.Exec("INSERT INTO format_smoke_source VALUES(3,'%s','first'),(4,'%I',NULL)", 2))
		d = fixture.ControlsReferenceDiagnostic("22004", "null values cannot be formatted as an SQL identifier")
		s = fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO format_smoke SELECT id,format(p,v) FROM format_smoke_source ORDER BY id", d))
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("CREATE VIEW format_smoke_view AS SELECT id,format('%s',v) AS rendered FROM format_smoke", 0))
		want := fixture.Select("SELECT id,rendered FROM format_smoke_view ORDER BY id", []string{"id", "rendered"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1:updated"}, {int64(2), `"select"`}})
		formatReferenceBoth(t, p, b, c, want)
		formatReferenceControlPhase(t, p, b, c, want)
	})
}
