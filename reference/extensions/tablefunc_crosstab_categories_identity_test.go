package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabCategoriesIdentity(t *testing.T) {
	t.Run("selection_and_strict", func(t *testing.T) {
		crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			crosstabSeed(t, p, b, c)
			crosstabCategoriesDeclaration(t, p, b, c, "public")
			q := crosstabCategoriesSQL("crosstab", "k text,a text,b text") + " ORDER BY k"
			h := fixture.CrosstabHeld(t, p, c, q)
			for _, args := range [][]any{{nil, "SELECT )"}, {"SELECT missing_ct", nil}, {nil, nil}} {
				s := fixture.Select(q, []string{"k", "a", "b"}, []string{"TEXT", "TEXT", "TEXT"}, nil, args...)
				crosstabCategoriesBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			}
			// Unknown second SQL literals resolve to TEXT, not the obsolete INT4 form.
			literal := "SELECT * FROM crosstab('SELECT 7,2,11','SELECT 2') AS t(k integer,v integer)"
			crosstabCategoriesBoth(t, p, b, c, fixture.Select(literal, []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(11)}}))
			integer := "SELECT * FROM crosstab($1::text,$2::integer) AS t(k text,c1 text,c2 text,c3 text) ORDER BY k"
			s := fixture.CrosstabWant(integer, fixture.CrosstabSource, int64(-1))
			crosstabCategoriesBoth(t, p, b, c, s)
			ih := fixture.CrosstabHeld(t, p, c, integer)
			fixture.RunStep(t, p.Ctx, ih, b.Recorder, s)
			// Untyped held parameters resolve through the same ordinary overload selector.
			unknown := "SELECT * FROM crosstab($1,$2) AS t(k integer,v integer)"
			uh := fixture.CrosstabHeld(t, p, c, unknown)
			us := fixture.Select(unknown, []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(11)}}, "SELECT 7,2,11", "SELECT 2")
			crosstabCategoriesBoth(t, p, b, c, us)
			fixture.RunStep(t, p.Ctx, uh, b.Recorder, us)
		})
	})
	t.Run("caller_authority", func(t *testing.T) {
		crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA ct_addon", 0))
			fixture.CrosstabSeedInSchema(t, p, b, c, "ct_addon")
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE ROLE ct_reader", 0),
				fixture.Exec("GRANT USAGE ON SCHEMA public TO ct_reader", 0),
				fixture.Exec("CREATE TABLE ct_categories(seq integer,category text)", 0),
				fixture.Exec("INSERT INTO ct_categories VALUES(1,'z'),(2,'a'),(3,'x')", 3),
			} {
				run(s)
			}
			q := crosstabCategoriesSQL("ct_addon.crosstab", "k text,z text,a text,x text") + " ORDER BY k"
			h := fixture.CrosstabHeld(t, p, c, q)
			source := "SELECT k,category,v FROM public.ct_source ORDER BY seq"
			categories := "SELECT category FROM public.ct_categories ORDER BY seq"
			want := fixture.Select(q, []string{"k", "z", "a", "x"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"g1", "A", "C", nil}, {"g2", nil, nil, "B2"}, {"g3", nil, nil, nil}}, source, categories)
			run(fixture.Exec("SET ROLE ct_reader", 0))
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema ct_addon")
			d.Position = fixture.Position(15)
			crosstabFailure(t, p, b, c, q, d, "prepare", nil, "SELECT )")
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA ct_addon TO ct_reader", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ct_addon.crosstab(text,text) FROM PUBLIC", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			d = fixture.ControlsReferenceDiagnostic("42501", "permission denied for function crosstab")
			crosstabFailure(t, p, b, c, q, d, "execute", nil, "SELECT )")
			crosstabFailure(t, p, b, c, q, d, "execute", "SELECT missing_ct", nil)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION ct_addon.crosstab(text,text) TO ct_reader", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			d = fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ct_categories")
			d.Context = "SQL statement \"" + categories + "\""
			crosstabFailure(t, p, b, c, q, d, "execute", source, categories)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT SELECT ON ct_categories TO ct_reader", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			d = fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ct_source")
			d.Context = "SQL statement \"" + source + "\""
			crosstabFailure(t, p, b, c, q, d, "execute", source, categories)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT SELECT ON ct_source TO ct_reader", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			crosstabCategoriesBoth(t, p, b, c, want)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("REVOKE SELECT ON ct_source FROM ct_reader", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			s := fixture.ErrorDiagnostic(q, d, source, categories)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT SELECT ON ct_source TO ct_reader", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
			run(fixture.Exec("RESET ROLE", 0))
			// Caller search_path applies independently to both nested strings.
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE SCHEMA ct_path", 0),
				fixture.Exec("CREATE TABLE ct_path.ct_source(seq integer,k text,category text,v text)", 0),
				fixture.Exec("INSERT INTO ct_path.ct_source VALUES(1,'path','x','caller')", 1),
				fixture.Exec("CREATE TABLE ct_path.ct_categories(seq integer,category text)", 0),
				fixture.Exec("INSERT INTO ct_path.ct_categories VALUES(1,'x'),(2,'z'),(3,'a')", 3),
				fixture.Exec("SET search_path TO ct_path,public", 0),
			} {
				run(s)
			}
			path := want
			path.Args, path.Rows = []any{fixture.CrosstabSource, "SELECT category FROM ct_categories ORDER BY seq"}, [][]any{{"path", "caller", nil, nil}}
			fixture.RunStep(t, p.Ctx, h, b.Recorder, path)
			run(fixture.Exec("SET search_path TO public", 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
		})
	})
	t.Run("stored_identity_and_freshness", func(t *testing.T) {
		crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA ct_addon", 0))
			run(fixture.Exec("CREATE SCHEMA ct_moved", 0))
			fixture.CrosstabSeedInSchema(t, p, b, c, "ct_addon")
			q := crosstabCategoriesSQL("ct_addon.crosstab", "k text,z text,a text,x text") + " ORDER BY k"
			categories := crosstabCategoriesOrder("z", "a", "x")
			source := "SELECT k,category,v FROM public.ct_source ORDER BY seq"
			view := "SELECT * FROM ct_category_view ORDER BY k"
			run(fixture.Exec("CREATE VIEW ct_category_view AS SELECT * FROM ct_addon.crosstab("+fixture.CrosstabQuote(source)+","+fixture.CrosstabQuote(categories)+") AS t(k text,z text,a text,x text)", 0))
			want := fixture.Select(q, []string{"k", "z", "a", "x"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"g1", "A", "C", nil}, {"g2", nil, nil, "B2"}, {"g3", nil, nil, nil}}, source, categories)
			h := fixture.CrosstabHeld(t, p, c, q)
			vh := fixture.CrosstabHeld(t, p, c, view)
			check := func() {
				fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
				v := want
				v.Query, v.Args = view, nil
				crosstabCategoriesBoth(t, p, b, c, v)
				fixture.RunStep(t, p.Ctx, vh, b.Recorder, v)
			}
			check()
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("UPDATE ct_source SET v='transaction' WHERE seq=1", 1))
			want.Rows[0][1] = "transaction"
			check()
			run(fixture.Exec("ROLLBACK", 0))
			want.Rows[0][1] = "A"
			check()
			run(fixture.Exec("UPDATE ct_source SET v='fresh' WHERE seq=1", 1))
			want.Rows[0][1] = "fresh"
			check()
			var proc int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ct_addon.crosstab(text,text)'::regprocedure::oid::bigint").Scan(&proc); err != nil || proc <= 0 {
				t.Fatal("hash procedure identity", proc, err)
			}
			run(fixture.Exec("ALTER FUNCTION ct_addon.crosstab(text,text) RENAME TO ct_pivot", 0))
			run(fixture.Exec("ALTER FUNCTION ct_addon.ct_pivot(text,text) SET SCHEMA ct_moved", 0))
			run(fixture.Select("SELECT 'ct_moved.ct_pivot(text,text)'::regprocedure::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{proc}}))
			moved := want
			moved.Query = crosstabCategoriesSQL("ct_moved.ct_pivot", "k text,z text,a text,x text") + " ORDER BY k"
			crosstabCategoriesBoth(t, p, b, c, moved)
			v := want
			v.Query, v.Args = view, nil
			fixture.RunStep(t, p.Ctx, vh, b.Recorder, v)
			// A supported ordinary function shadow/replacement must not acquire C-provider authority.
			run(fixture.Exec("CREATE FUNCTION ct_addon.crosstab(text,text) RETURNS integer LANGUAGE SQL AS 'SELECT 73'", 0))
			run(fixture.Exec("SET search_path TO ct_addon,public", 0))
			crosstabCategoriesBoth(t, p, b, c, fixture.Select("SELECT crosstab('x','y') AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(73)}}))
			fixture.RunStep(t, p.Ctx, vh, b.Recorder, v)
			crosstabCategoriesBoth(t, p, b, c, moved)
			run(fixture.Exec("SET search_path TO public", 0))
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE DOMAIN ct_addon.ct_cell AS integer", 0),
				fixture.Exec("CREATE VIEW ct_domain_pivot AS SELECT * FROM ct_moved.ct_pivot('SELECT ''g''::text,''a''::text,''10''::text','SELECT ''a''::text') AS t(k text,v ct_addon.ct_cell)", 0),
				fixture.Exec("ALTER DOMAIN ct_addon.ct_cell RENAME TO ct_cell_renamed", 0),
				fixture.Exec("ALTER DOMAIN ct_addon.ct_cell_renamed SET SCHEMA ct_moved", 0),
			} {
				run(s)
			}
			crosstabCategoriesBoth(t, p, b, c, fixture.Select("SELECT k,v::integer AS v FROM ct_domain_pivot", []string{"k", "v"}, []string{"TEXT", "INT4"}, [][]any{{"g", int64(10)}}))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DROP EXTENSION tablefunc CASCADE", 0))
			run(fixture.Exec("ROLLBACK", 0))
			fixture.RunStep(t, p.Ctx, vh, b.Recorder, v)
		})
	})
	for _, side := range []string{"category", "source"} {
		t.Run(side+"_cancellation", func(t *testing.T) {
			crosstabCategoriesCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION tablefunc", 0))
				source, categories := "SELECT 'g'::text,'a'::text,'v'::text", "SELECT 'a'::text"
				sleep := "SELECT pg_sleep(600)::text"
				if side == "category" {
					categories = sleep
				} else {
					sleep = "SELECT 'g'::text,'a'::text,pg_sleep(600)::text"
					source = sleep
				}
				q := "SELECT * FROM crosstab("+fixture.CrosstabQuote(source)+","+fixture.CrosstabQuote(categories)+") AS t(k text,v text)"
				h := fixture.CrosstabHeld(t, p, c, q)
				for _, prepared := range []bool{false, true} {
					fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \""+sleep+"\"", nil, func(ctx context.Context) fixture.RelationOutcome {
						var rows *sql.Rows
						var err error
						if prepared {
							rows, err = h.Statement.QueryContext(ctx)
						} else {
							rows, err = c.QueryContext(ctx, q)
						}
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					good := crosstabCategoriesSQL("crosstab", "k text,v text")
					crosstabCategoriesBoth(t, p, b, c, fixture.Select(good, []string{"k", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"g", "v"}}, "SELECT 'g'::text,'a'::text,'v'::text", "SELECT 'a'::text"))
				}
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := c.QueryContext(ctx, q)
					if rows != nil {
						rows.Close()
						t.Error("pre-canceled query published rows")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatal("pre-canceled ordinary context error", err)
					}
				}
			})
		})
	}
}

func crosstabCategoriesDeclaration(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema string) {
	t.Helper()
	q := "SELECT p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin,l.lanname,p.pronargdefaults,p.provariadic::integer AS variadic,p.proargnames::text AS names FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname=$1 AND p.proname='crosstab' AND p.proargtypes::text='25 25'"
	crosstabCategoriesBoth(t, p, b, c, fixture.Select(q, []string{"args", "result", "proisstrict", "provolatile", "proretset", "proparallel", "procost", "prorows", "prosrc", "probin", "lanname", "pronargdefaults", "variadic", "names"}, []string{"TEXT", "INT8", "BOOL", "CHAR", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "TEXT", "TEXT", "NAME", "INT2", "INT4", "TEXT"}, [][]any{{"25 25", int64(2249), true, "s", true, "u", float64(1), float64(1000), "crosstab_hash", "$libdir/tablefunc", "c", int64(0), int64(0), nil}}, schema))
}
