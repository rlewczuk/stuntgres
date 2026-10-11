package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringPadStored(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ name, expr, compact, pretty string }{
			{"l", "lpad(s,n,f)=e", "(lpad(s, n, f) = e)", "lpad(s, n, f) = e"},
			{"r", "rpad(s,n)=e", "(rpad(s, n) = e)", "rpad(s, n) = e"},
			{"t", "repeat(s,n)=e", "(repeat(s, n) = e)", "repeat(s, n) = e"},
			{"v", "reverse(s)=e", "(reverse(s) = e)", "reverse(s) = e"},
			{"char", "lpad(s::char(4),n::smallint,f::char(3))=e", `(lpad(((s)::character(4))::text, ((n)::smallint)::integer, ((f)::character(3))::text) = e)`, `lpad(s::character(4)::text, n::smallint::integer, f::character(3)::text) = e`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE sp_st_"+tc.name+"(s text,n int,f text,e text,CONSTRAINT sp_ck_"+tc.name+" CHECK("+tc.expr+"))", 0))
				for _, mode := range []struct{ flag, want string }{{"false", tc.compact}, {"true", tc.pretty}} {
					stringPadBoth(t, p, b, c, fixture.Select("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='sp_ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}}))
					stringPadBoth(t, p, b, c, fixture.Select("SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='sp_ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.want + ")"}}))
				}
			})
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sp_defaults(id int,s text DEFAULT (lpad('a',3,'x')),CONSTRAINT sp_check CHECK(reverse(s)='axx'))", 0),
			fixture.Exec("INSERT INTO sp_defaults(id) VALUES(1)", 1), fixture.Exec("INSERT INTO sp_defaults VALUES(2,NULL)", 1),
			fixture.Exec("CREATE VIEW sp_view AS SELECT id,lpad(s,4,'y') AS l,rpad(s,4,'y') AS r,repeat(s,2) AS t,reverse(s) AS v FROM sp_defaults", 0),
			fixture.Exec("ALTER TABLE sp_defaults RENAME COLUMN s TO renamed", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		stringPadBoth(t, p, b, c, fixture.Select("SELECT id,l,r,t,v FROM sp_view ORDER BY id", []string{"id", "l", "r", "t", "v"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "yxxa", "xxay", "xxaxxa", "axx"}, {int64(2), nil, nil, nil, nil}}))
		stringPadBoth(t, p, b, c, fixture.Select("SELECT pg_get_expr(conbin,conrelid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='sp_check'", []string{"d"}, []string{"TEXT"}, [][]any{{"reverse(renamed) = 'axx'::text"}}))
		stringPadBoth(t, p, b, c, fixture.Select("SELECT pg_get_viewdef('sp_view'::regclass,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{" SELECT id,\n    lpad(renamed, 4, 'y'::text) AS l,\n    rpad(renamed, 4, 'y'::text) AS r,\n    repeat(renamed, 2) AS t,\n    reverse(renamed) AS v\n   FROM sp_defaults;"}}))
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "sp_defaults" violates check constraint "sp_check"`)
		d.Detail = "Failing row contains (3, bad)."
		d.Schema = "public"
		d.Table = "sp_defaults"
		d.Constraint = "sp_check"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO sp_defaults VALUES(3,'bad')", d)
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sp_generated(id int,s text,v bytea,g text GENERATED ALWAYS AS (reverse(s)) STORED,h bytea GENERATED ALWAYS AS (reverse(v)) STORED)", 0),
			fixture.Exec("CREATE INDEX sp_text_index ON sp_generated ((lpad(s,3,'x')))", 0),
			fixture.Exec("CREATE INDEX sp_binary_index ON sp_generated ((reverse(v)))", 0),
			fixture.Exec(`INSERT INTO sp_generated(id,s,v) VALUES(1,'ab','\x00ff'::bytea),(2,NULL,NULL)`, 2),
			fixture.Exec("CREATE VIEW sp_binary_view AS SELECT id,reverse(v) AS r FROM sp_generated", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		stringPadBoth(t, p, b, c, fixture.Select("SELECT id,g,h FROM sp_generated ORDER BY id", []string{"id", "g", "h"}, []string{"INT4", "TEXT", "BYTEA"}, [][]any{{int64(1), "ba", string([]byte{0xff, 0})}, {int64(2), nil, nil}}))
		stringPadBoth(t, p, b, c, fixture.Select("SELECT id,r FROM sp_binary_view ORDER BY id", []string{"id", "r"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), string([]byte{0xff, 0})}, {int64(2), nil}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE sp_generated SET s='cd',v='AB'::bytea WHERE id=1", 1))
		stringPadBoth(t, p, b, c, fixture.Select("SELECT g,h FROM sp_generated WHERE id=1", []string{"g", "h"}, []string{"TEXT", "BYTEA"}, [][]any{{"dc", "BA"}}))
	})
}
