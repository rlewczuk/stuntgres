package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringReplaceStored(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ name, expr, compact, pretty, provider string }{
			{"os3", "overlay(v PLACING s FROM i)=e", `(OVERLAY(v PLACING s FROM i) = e)`, `OVERLAY(v PLACING s FROM i) = e`, "overlay"},
			{"os4", "overlay(v PLACING s FROM i FOR n)=e", `(OVERLAY(v PLACING s FROM i FOR n) = e)`, `OVERLAY(v PLACING s FROM i FOR n) = e`, "overlay"},
			{"of3", "\"overlay\"(v,s,i)=e", `("overlay"(v, s, i) = e)`, `"overlay"(v, s, i) = e`, "overlay"},
			{"of4", "overlay(v,s,i,n)=e", `("overlay"(v, s, i, n) = e)`, `"overlay"(v, s, i, n) = e`, "overlay"},
			{"r", "replace(v,s,e)=e", `(replace(v, s, e) = e)`, `replace(v, s, e) = e`, "replace"},
			{"t", "translate(v,s,e)=e", `(translate(v, s, e) = e)`, `translate(v, s, e) = e`, "translate"},
			{"f", "starts_with(v,s)", `starts_with(v, s)`, `starts_with(v, s)`, "starts_with"},
			{"p", "v ^@ s", `(v ^@ s)`, `v ^@ s`, "starts_with"},
			{"char", "replace(v::char(4),s::char(3),e::char(3))=e", `(replace(((v)::character(4))::text, ((s)::character(3))::text, ((e)::character(3))::text) = e)`, `replace(v::character(4)::text, s::character(3)::text, e::character(3)::text) = e`, "replace"},
			{"small", "overlay(v,s,i::smallint,n::smallint)=e", `("overlay"(v, s, ((i)::smallint)::integer, ((n)::smallint)::integer) = e)`, `"overlay"(v, s, i::smallint::integer, n::smallint::integer) = e`, "overlay"},
			{"specialchar", "overlay(v::char(4) PLACING s::char(3) FROM i::smallint)=e", `(OVERLAY((v)::character(4) PLACING (s)::character(3) FROM (i)::smallint) = e)`, `OVERLAY(v::character(4) PLACING s::character(3) FROM i::smallint) = e`, "overlay"},
			{"coll", `starts_with(v COLLATE "C",s COLLATE "C")`, `starts_with((v COLLATE "C"), (s COLLATE "C"))`, `starts_with(v COLLATE "C", s COLLATE "C")`, "starts_with"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE sr_st_"+tc.name+"(v text,s text,i int,n int,e text,CONSTRAINT sr_ck_"+tc.name+" CHECK("+tc.expr+"))", 0))
				for _, mode := range []struct{ flag, want string }{{"false", tc.compact}, {"true", tc.pretty}} {
					t.Run(mode.flag, func(t *testing.T) {
						stringReplaceBoth(t, p, b, c, fixture.Select("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='sr_ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}}))
						stringReplaceBoth(t, p, b, c, fixture.Select("SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='sr_ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.want + ")"}}))
					})
				}
				if !b.PG {
					var tree string
					if err := c.QueryRowContext(p.Ctx, "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='sr_ck_"+tc.name+"'").Scan(&tree); err != nil {
						t.Fatal(err)
					}
					if !strings.HasPrefix(tree, "{type 16 typmod -1 ") || !strings.Contains(tree, "String "+tc.provider) || !strings.Contains(tree, "Var attnum 1") {
						t.Fatal("typed structural carrier", tree)
					}
				}
			})
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sr_defaults(id int,s text DEFAULT (overlay(replace('abc','b','X') PLACING translate('YZ','Z','') FROM 2)),f bool DEFAULT (starts_with('abc','a')),p bool DEFAULT ('abc' ^@ 'a'),CONSTRAINT sr_check CHECK(starts_with(s,'a') AND s ^@ 'a'))", 0),
			fixture.Exec("INSERT INTO sr_defaults(id) VALUES(1)", 1),
			fixture.Select("SELECT id,s,f,p FROM sr_defaults", []string{"id", "s", "f", "p"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), "aYc", true, true}}),
			fixture.Exec("INSERT INTO sr_defaults VALUES(2,NULL,NULL,NULL)", 1),
			fixture.Exec("CREATE VIEW sr_view AS SELECT id,overlay(s,'Z',2) AS o,replace(s,'Y','X') AS r,translate(s,'Y','1') AS t,starts_with(s,'a') AS f,s ^@ 'a' AS p FROM sr_defaults", 0),
			fixture.Exec("ALTER TABLE sr_defaults RENAME COLUMN s TO renamed", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT id,o,r,t,f,p FROM sr_view ORDER BY id", []string{"id", "o", "r", "t", "f", "p"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), "aZc", "aXc", "a1c", true, true}, {int64(2), nil, nil, nil, nil, nil}}))
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "sr_defaults" violates check constraint "sr_check"`)
		d.Detail = "Failing row contains (3, bad, t, t)."
		d.Schema = "public"
		d.Table = "sr_defaults"
		d.Constraint = "sr_check"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO sr_defaults VALUES(3,'bad',true,true)", d)
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT pg_get_expr(conbin,conrelid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='sr_check'", []string{"d"}, []string{"TEXT"}, [][]any{{"starts_with(renamed, 'a'::text) AND renamed ^@ 'a'::text"}}))
	})
}
