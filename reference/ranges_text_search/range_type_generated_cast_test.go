package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 typecmds.c creates an e/f cast using the single-range
// constructor; pg_cast.c records all three dependencies with INTERNAL kind.
func TestReferenceRangeTypeGeneratedCast(t *testing.T) {
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{
			"CREATE TYPE cast_span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=cast_many)",
			"CREATE TYPE cast_other AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=cast_other_many)",
			"CREATE TABLE cast_saved(k integer,r cast_span,m cast_many)",
			"CREATE TABLE cast_dest(m cast_many)",
			"CREATE VIEW cast_view AS SELECT ('[1,3]'::cast_span)::cast_many AS m",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO cast_saved VALUES(1,'[1,3]',('[1,3]'::cast_span)::cast_many),(2,'[1,4)',('[1,4)'::cast_span)::cast_many)", 2))
		for _, tc := range []struct {
			input any
			want  any
		}{{"[1,3]", "{[1,3]}"}, {"[1,4)", "{[1,4)}"}, {"empty", "{}"}, {nil, nil}} {
			s := rangeTypeSelect(b, "SELECT ($1::cast_span)::cast_many AS m", []string{"m"}, []string{"CAST_MANY"}, [][]any{{tc.want}}, nil, tc.input)
			rangeTypeRemedyRoutes(t, p, b, c, s)
		}
		stmt, err := c.PrepareContext(p.Ctx, "SELECT ($1::cast_span)::cast_many AS m")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			input any
			want  any
		}{{"[1,3]", "{[1,3]}"}, {"empty", "{}"}, {nil, nil}} {
			s := rangeTypeSelect(b, "SELECT ($1::cast_span)::cast_many AS m", []string{"m"}, []string{"CAST_MANY"}, [][]any{{tc.want}}, nil, tc.input)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
		}
		rangeTypeRemedyRoutes(t, p, b, c, fixture.Select("SELECT a[1]::text AS first,a[2]::text AS second,a[3] IS NULL AS absent FROM (SELECT ($1::cast_span[])::cast_many[] AS a) s", []string{"first", "second", "absent"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"{[1,3]}", "{}", true}}, `{"[1,3]",empty,NULL}`))
		q := "INSERT INTO cast_dest(m) SELECT $1::cast_span"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42804", Message: `column "m" is of type cast_many but expression is of type cast_span`, Hint: "You will need to rewrite or cast the expression.", Position: fixture.Position(strings.Index(q, "$1") + 1)}, "[1,3]")
		s.ErrorStage = "execute"
		run(s)
		s.Prepared = true
		s.ErrorStage = "prepare"
		run(s)
		s = fixture.ImplicitError("SELECT $1::cast_span::cast_other_many", fixture.Diagnostic{Code: "42846", Message: "cannot cast type cast_span to cast_other_many", Position: "21"}, "[1,3]")
		s.ErrorStage = "execute"
		run(s)
		s.Prepared = true
		s.ErrorStage = "prepare"
		run(s)
		check := func(backend fixture.Backend, executor fixture.Executor) {
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT castcontext::text AS context,castmethod::text AS method,castfunc='public.cast_many(public.cast_span)'::regprocedure::oid AS correct_function FROM pg_cast WHERE castsource='cast_span'::regtype AND casttarget='cast_many'::regtype", []string{"context", "method", "correct_function"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"e", "f", true}}))
			for _, edge := range []struct{ class, object string }{{"pg_type", "'cast_span'::regtype::oid"}, {"pg_type", "'cast_many'::regtype::oid"}, {"pg_proc", "'public.cast_many(public.cast_span)'::regprocedure::oid"}} {
				fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_cast c ON d.classid='pg_cast'::regclass AND d.objid=c.oid WHERE c.castsource='cast_span'::regtype AND c.casttarget='cast_many'::regtype AND d.refclassid='"+edge.class+"'::regclass AND d.refobjid="+edge.object+" AND d.deptype='i'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			}
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_cast c ON d.classid='pg_cast'::regclass AND d.objid=c.oid WHERE c.castsource='cast_span'::regtype AND c.casttarget='cast_many'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT k,r::text AS r,m::text AS m FROM cast_saved ORDER BY k", []string{"k", "r", "m"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "[1,3]", "{[1,3]}"}, {int64(2), "[1,4)", "{[1,4)}"}}))
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT m::text AS m FROM cast_view", []string{"m"}, []string{"TEXT"}, [][]any{{"{[1,3]}"}}))
		}
		check(b, c)
		drop := fixture.ImplicitError("DROP CAST (cast_span AS cast_many)", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop cast from cast_span to cast_many because function cast_many(cast_span) requires it", Hint: "You can drop function cast_many(cast_span) instead."})
		drop.ErrorStage = "execute"
		rangeTypeRemedyRoutes(t, p, b, c, drop)
		check(b, c)
		rangeTypeRemedyReplay(t, p, b, c, nil, check, fixture.Prepared(fixture.Exec("INSERT INTO cast_dest VALUES(($1::cast_span)::cast_many)", 1, "[8,9]")), nil)
		var castID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_cast WHERE castsource='cast_span'::regtype AND casttarget='cast_many'::regtype").Scan(&castID); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("DROP TYPE cast_span CASCADE", 0))
		run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE oid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, castID))
		run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_other'::regtype AND casttarget='cast_other_many'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}
