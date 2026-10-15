package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringSliceStoredDefinitions(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN slice_domain AS text", 0),
			fixture.Exec("CREATE TABLE slice_stored(id int,s slice_domain DEFAULT substr('abcde',2,3),g text GENERATED ALWAYS AS (left(s,2)) STORED,CONSTRAINT slice_stored_ck CHECK(right(s,1)='d'))", 0),
			fixture.Exec("CREATE INDEX slice_stored_index ON slice_stored ((substring(s FROM 2 FOR 1)))", 0),
			fixture.Exec("INSERT INTO slice_stored(id) VALUES(1)", 1),
			fixture.Exec("INSERT INTO slice_stored(id,s) VALUES(2,NULL)", 1),
			fixture.Exec("CREATE VIEW slice_stored_view AS SELECT id,left(s,2) AS l,right(s,2) AS r,substr(s,2) AS a,substring(s FROM 2 FOR 1) AS n,substring(s FROM 'c(.)') AS p,substring(s SIMILAR '%#\"c_#\"' ESCAPE '#') AS q FROM slice_stored", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		want := fixture.Select("SELECT id,l,r,a,n,p,q FROM slice_stored_view ORDER BY id", []string{"id", "l", "r", "a", "n", "p", "q"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "bc", "cd", "cd", "c", "d", "cd"}, {int64(2), nil, nil, nil, nil, nil, nil}})
		stringPadBoth(t, p, b, c, want)
		stringPadBoth(t, p, b, c, fixture.Select("SELECT id,g FROM slice_stored ORDER BY id", []string{"id", "g"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "bc"}, {int64(2), nil}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE slice_stored RENAME COLUMN s TO renamed", 0))
		stringPadBoth(t, p, b, c, want)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE slice_stored SET renamed='zzd' WHERE id=1", 1))
		stringPadBoth(t, p, b, c, fixture.Select("SELECT id,g FROM slice_stored ORDER BY id", []string{"id", "g"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "zz"}, {int64(2), nil}}))
		for _, tc := range []struct{ name, expr, want string }{
			{"l", "left('abcde',2)", "ab"}, {"r", "right('abcde',2)", "de"},
			{"s", "substring('abcde' FROM 2 FOR 3)", "bcd"}, {"a", "substr('abcde',2,3)", "bcd"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					name := "slice_default_" + tc.name
					create := fixture.Exec("CREATE TABLE "+name+"(id int,v text DEFAULT "+tc.expr+")", 0)
					create.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, create)
					insert := fixture.Exec("INSERT INTO "+name+"(id) VALUES(1)", 1)
					insert.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, insert)
					stringPadBoth(t, p, b, c, fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE "+name, 0))
				}
			})
		}
	})
}

func TestReferenceStringSliceRendering(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ name, expr, rendered string }{
			{"left", "left(s,n)=e", `"left"(s, n) = e`},
			{"right", "right(s,n)=e", `"right"(s, n) = e`},
			{"substr", "substr(s,n,k)=e", "substr(s, n, k) = e"},
			{"comma", "substring(s,n,k)=e", `"substring"(s, n, k) = e`},
			{"sql", "substring(s FROM n FOR k)=e", "SUBSTRING(s FROM n FOR k) = e"},
			{"omitted", "substring(s FROM n)=e", "SUBSTRING(s FROM n) = e"},
			{"posix", "substring(s FROM p)=e", `"substring"(s, p) = e`},
			{"similar", `substring(s SIMILAR p ESCAPE x)=e`, "SUBSTRING(s SIMILAR p ESCAPE x) = e"},
		} {
			name := "slice_render_" + tc.name
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE "+name+"(s text,n int,k int,p text,x text,e text,CONSTRAINT "+name+"_ck CHECK("+tc.expr+"))", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW "+name+"_v AS SELECT "+tc.expr+" AS v FROM "+name, 0))
			for _, mode := range []struct{ flag, expression string }{
				{"false", "(" + tc.rendered + ")"},
				{"true", tc.rendered},
			} {
				for _, s := range []fixture.Step{
					fixture.Select("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") FROM pg_catalog.pg_constraint WHERE conname='"+name+"_ck'", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{mode.expression}}),
					fixture.Select("SELECT pg_get_constraintdef(oid,"+mode.flag+") FROM pg_catalog.pg_constraint WHERE conname='"+name+"_ck'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.expression + ")"}}),
					fixture.Select("SELECT pg_get_viewdef('"+name+"_v'::regclass,"+mode.flag+")", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{" SELECT " + mode.expression + " AS v\n   FROM " + name + ";"}}),
				} {
					t.Run(s.Query, func(t *testing.T) { stringPadBoth(t, p, b, c, s) })
				}
			}
		}
	})
}

func TestReferenceStringSliceFeatureSmoke(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT left('Aé🙂Z',-1) AS l,right('Aé🙂Z',-2) AS r,substring('abcde' FROM 0 FOR 3) AS s,substr('abcde',3) AS a,substring('foobar' FROM 'o(.)b') AS p,substring('foobar' SIMILAR '%#"o_b#"%' ESCAPE '#') AS q`
		stringPadBoth(t, p, b, c, fixture.Select(q, []string{"l", "r", "s", "a", "p", "q"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"Aé🙂", "🙂Z", "ab", "cde", "o", "oob"}}))
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE slice_smoke(id int,v text DEFAULT substr('abcde',2,3))", 0),
			fixture.Exec("INSERT INTO slice_smoke(id) VALUES(1)", 1),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		state := fixture.Select("SELECT id,v FROM slice_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "bcd"}})
		stringPadBoth(t, p, b, c, state)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("UPDATE slice_smoke SET v=substring(v,1,-1)", fixture.ControlsReferenceDiagnostic("22011", "negative substring length not allowed"))
			s.ErrorStage, s.Prepared = "execute", prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			stringPadBoth(t, p, b, c, state)
		}
		heldSQL := "SELECT substr($1,$2::int,$3::int) AS v"
		stmt, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: heldSQL}
		for _, tc := range []struct {
			start, count int
			want         string
		}{{2, 3, "bcd"}, {3, 2, "cd"}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(heldSQL, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, "abcde", tc.start, tc.count))
		}
	})
}
