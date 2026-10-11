package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatStored(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE DOMAIN format_stored_name AS name[]", 0))
		run(fixture.Exec("CREATE TABLE format_stored(id int,v text DEFAULT (format('%s:%s','x'::char(3),5.00::numeric)),a format_stored_name DEFAULT '{a,b}',CONSTRAINT format_stored_check CHECK(format('%s',v)<>'bad'))", 0))
		run(fixture.Exec("INSERT INTO format_stored(id) VALUES(1)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO format_stored VALUES(2,format('%s','z'),'{c,d}')", 1)))
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT id,v,format('%2$s/%1$s',VARIADIC a) AS names FROM format_stored ORDER BY id", []string{"id", "v", "names"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x  :5.00", "b/a"}, {int64(2), "z", "d/c"}}))
		for _, tc := range []struct{ flag, want string }{
			{"false", `(format('%s'::text, v) <> 'bad'::text)`},
			{"true", `format('%s'::text, v) <> 'bad'::text`},
		} {
			t.Run(tc.flag, func(t *testing.T) {
				formatReferenceBoth(t, p, b, c, fixture.Select("SELECT pg_get_expr(conbin,conrelid,"+tc.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='format_stored_check'", []string{"d"}, []string{"TEXT"}, [][]any{{tc.want}}))
				formatReferenceBoth(t, p, b, c, fixture.Select("SELECT pg_get_constraintdef(oid,"+tc.flag+") AS d FROM pg_catalog.pg_constraint WHERE conname='format_stored_check'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + tc.want + ")"}}))
			})
		}
		if !b.PG {
			var tree string
			if err := c.QueryRowContext(p.Ctx, "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='format_stored_check'").Scan(&tree); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(tree, "Format") || !strings.Contains(tree, "Var attnum 2") || !strings.HasPrefix(tree, "{type 16 typmod -1 ") {
				t.Fatalf("logical format carrier: %s", tree)
			}
		}
		run(fixture.Exec("CREATE VIEW format_stored_view AS SELECT id,format('%s',v) AS rendered,format('%2$s/%1$s',VARIADIC a) AS names FROM format_stored", 0))
		q := "SELECT id,rendered,names FROM format_stored_view ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		want := fixture.Select(q, []string{"id", "rendered", "names"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x  :5.00", "b/a"}, {int64(2), "z", "d/c"}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("ALTER TABLE format_stored RENAME COLUMN v TO renamed", 0))
		run(fixture.Exec("ALTER TABLE format_stored RENAME TO format_stored_renamed", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT pg_get_expr(conbin,conrelid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='format_stored_check'", []string{"d"}, []string{"TEXT"}, [][]any{{`format('%s'::text, renamed) <> 'bad'::text`}}))
		for _, pretty := range []string{"false", "true"} {
			t.Run("view-"+pretty, func(t *testing.T) {
				definition := " SELECT id,\n    format('%s'::text, renamed) AS rendered,\n    format('%2$s/%1$s'::text, VARIADIC a) AS names\n   FROM format_stored_renamed;"
				formatReferenceBoth(t, p, b, c, fixture.Select("SELECT pg_get_viewdef('format_stored_view'::regclass,"+pretty+") AS d", []string{"d"}, []string{"TEXT"}, [][]any{{definition}}))
			})
		}
		run(fixture.Exec("CREATE TABLE format_rewrite(v char(3),CONSTRAINT format_rewrite_check CHECK(format('%s',v)<>'bad'))", 0))
		run(fixture.Exec("INSERT INTO format_rewrite VALUES('x')", 1))
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT format('%s',v) FROM format_rewrite", []string{"format"}, []string{"TEXT"}, [][]any{{"x  "}}))
		run(fixture.Exec("ALTER TABLE format_rewrite ALTER COLUMN v TYPE char(4)", 0))
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT format('%s',v) FROM format_rewrite", []string{"format"}, []string{"TEXT"}, [][]any{{"x   "}}))
		run(fixture.Exec("CREATE TABLE format_variadic_default(v text DEFAULT (format('%2$s/%1$s',VARIADIC '{a,b}'::name[])))", 0))
		run(fixture.Exec("INSERT INTO format_variadic_default DEFAULT VALUES", 1))
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT v FROM format_variadic_default", []string{"v"}, []string{"TEXT"}, [][]any{{"b/a"}}))
		for _, pretty := range []string{"false", "true"} {
			formatReferenceBoth(t, p, b, c, fixture.Select("SELECT pg_get_expr(d.adbin,d.adrelid,"+pretty+") AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='format_variadic_default'", []string{"d"}, []string{"TEXT"}, [][]any{{`format('%2$s/%1$s'::text, VARIADIC '{a,b}'::name[])`}}))
		}
	})
}

func TestReferenceFormatVolatility(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TABLE format_generated(v text,g text GENERATED ALWAYS AS (format('%s',v)) STORED)",
			"CREATE TABLE format_generated(v text,g text GENERATED ALWAYS AS (format('constant')) STORED)",
		} {
			t.Run(q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable")
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, d)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE format_index(v text)", 0))
		for _, expression := range []string{"format('%s',v)", "format('constant')"} {
			t.Run(expression, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE")
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("CREATE INDEX format_stable_index ON format_index ("+expression+")", d)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
	})
}
