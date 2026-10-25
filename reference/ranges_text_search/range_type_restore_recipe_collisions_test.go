package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeRestoreRecipeCollisions(t *testing.T) {
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{
			`CREATE TYPE public."left" AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=public."right")`,
			`CREATE DOMAIN recipe_domain AS public."left"`,
			`CREATE TABLE recipe_saved(k integer,r public."left",m public."right",a public."left"[],d recipe_domain)`,
			`CREATE TABLE recipe_sink(k integer)`,
			`CREATE FUNCTION public."left"(bigint,bigint,text) RETURNS integer LANGUAGE SQL AS $$SELECT 17$$`,
			`CREATE FUNCTION public.recipe_default_value() RETURNS integer LANGUAGE SQL AS $$SELECT public."left"(1::bigint,3::bigint,'[]')$$`,
			`CREATE TABLE recipe_default(k integer DEFAULT public.recipe_default_value())`,
		} {
			run(fixture.Exec(q, 0))
		}
		r := `public."left"(1,3,'[]')`
		last := `public."left"(8,9,'[]')`
		run(fixture.Exec(`INSERT INTO recipe_saved VALUES(1,`+r+`,public."right"(`+last+`,`+r+`),array_cat(ARRAY[`+r+`],ARRAY[`+last+`]),`+r+`),(2,'empty',public."right"(),ARRAY[]::public."left"[],'empty')`, 2))
		run(fixture.Exec(`INSERT INTO recipe_default DEFAULT VALUES`, 1))
		for _, tc := range []struct{ q, want string }{{`SELECT public."left"(1,3,'[]')::text AS v`, "[1,3]"}, {`SELECT public."right"()::text AS v`, "{}"}, {`SELECT public."right"(` + r + `)::text AS v`, "{[1,3]}"}, {`SELECT public."right"(` + last + `,` + r + `)::text AS v`, "{[1,3],[8,9]}"}} {
			rangeTypeRemedyRoutes(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		rangeTypeRemedyRoutes(t, p, b, c, fixture.Select(`SELECT public."left"(1::bigint,3::bigint,'[]') AS k`, []string{"k"}, []string{"INT4"}, [][]any{{int64(17)}}))
		saved := fixture.Select(`SELECT k,r::text AS r,m::text AS m,a[1]::text AS first,d::text AS d FROM recipe_saved ORDER BY k`, []string{"k", "r", "m", "first", "d"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "[1,3]", "{[1,3],[8,9]}", "[1,3]", "[1,3]"}, {int64(2), "empty", "{}", nil, "empty"}})
		check := func(backend fixture.Backend, executor fixture.Executor) {
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, saved)
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select(`SELECT k FROM recipe_default`, []string{"k"}, []string{"INT4"}, [][]any{{int64(17)}}))
		}
		check(b, c)
		rangeTypeRemedyReplay(t, p, b, c, nil, check, fixture.Prepared(fixture.Exec(`INSERT INTO recipe_saved VALUES(3,public."left"($1::int4,$2::int4,'[]'),public."right"(),ARRAY[]::public."left"[],'empty')`, 1, int64(20), int64(21))), nil)
		if b.PG {
			return
		}
		var original bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &original); err != nil {
			t.Fatal(err)
		}
		held, err := c.QueryContext(p.Ctx, saved.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, suffix := range []string{
			`INSERT INTO recipe_sink VALUES(public."left"(1::bigint,3::bigint,'[]'));`,
			`CREATE SCHEMA recipe_forged; CREATE FUNCTION recipe_forged."left"(integer,integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT 17$$; INSERT INTO recipe_sink VALUES(recipe_forged."left"(1,3,'[]'));`,
			`INSERT INTO recipe_saved(k,r) VALUES(3,"left"(1,3,'[]'));`,
			`CREATE FUNCTION recipe_unlisted() RETURNS integer LANGUAGE SQL AS $$SELECT 17$$; INSERT INTO recipe_sink VALUES(public.recipe_unlisted());`,
		} {
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(original.String()+suffix))
			if err == nil || err.Error() != "stuntgres: invalid restore scalar" {
				t.Fatal("nonconstructor/static-shape collision crossed replay authority", err)
			}
			check(b, c)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(original.Bytes(), after.Bytes()) {
				t.Fatal("failed recipe Restore changed prior dump", err)
			}
		}
		parameterInput := original.String() + `INSERT INTO recipe_saved(k,m) VALUES(3,public."right"(NULL::public."left",public."left"($1::int4,3,'[]')));`
		parameterError := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		parameterError.Position = fixture.Position(strings.LastIndex(parameterInput, "$1") + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(parameterInput)), &parameterError)
		check(b, c)
		var unchanged bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil || !bytes.Equal(original.Bytes(), unchanged.Bytes()) {
			t.Fatal("nested recipe parameter changed prior dump", err)
		}
		fixture.RangeConsumeHeld(t, held, saved)
		// Existing stored/default SQL bodies keep their authority while the
		// enclosing INSERT is replayed. The same-name ordinary overload is
		// forbidden only as a newly supplemental written value above.
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(original.String()+`INSERT INTO recipe_default DEFAULT VALUES;`)); err != nil {
			t.Fatal("incumbent stored SQL recipe acquired constructor-only authority", err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT count(*) AS n,sum(k)::integer AS total FROM recipe_default`, []string{"n", "total"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(34)}}))
	})
}
