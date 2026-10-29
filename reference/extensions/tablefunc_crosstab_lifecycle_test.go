package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabLifecycle(t *testing.T) {
	t.Run("install_rollback", func(t *testing.T) {
		crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("CREATE EXTENSION tablefunc", 0))
			run(fixture.Select("SELECT * FROM crosstab('SELECT 7,0,11') AS t(k integer,a integer,b integer)", []string{"k", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(11), nil}}))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='tablefunc'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname IN ('crosstab','crosstab2','crosstab3','crosstab4')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname IN ('tablefunc_crosstab_2','tablefunc_crosstab_3','tablefunc_crosstab_4')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("CREATE EXTENSION tablefunc", 0))
			crosstabInventory(t, p, b, c, "public")
		})
	})
	t.Run("stored_identity", func(t *testing.T) {
		crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA ct_addon", 0))
			fixture.CrosstabSeedInSchema(t, p, b, c, "ct_addon")
			run(fixture.Exec("CREATE VIEW ct_generic_view AS SELECT * FROM ct_addon.crosstab('SELECT k,category,v FROM public.ct_source ORDER BY seq') AS t(k text,c1 text,c2 text,c3 text)", 0))
			run(fixture.Exec("CREATE VIEW ct_wrapper_view AS SELECT * FROM ct_addon.crosstab3('SELECT k,category,v FROM public.ct_source ORDER BY seq')", 0))
			q := "SELECT * FROM ct_addon.crosstab($1::text) AS t(k text,c1 text,c2 text,c3 text) ORDER BY k"
			h := fixture.CrosstabHeld(t, p, c, q)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.CrosstabWant(q, fixture.CrosstabSource))
			var proc, typ int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ct_addon.crosstab3(text)'::regprocedure::oid::bigint,'ct_addon.tablefunc_crosstab_3'::regtype::oid::bigint").Scan(&proc, &typ); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("ALTER FUNCTION ct_addon.crosstab3(text) RENAME TO pivot_three", 0))
			run(fixture.Exec("ALTER TYPE ct_addon.tablefunc_crosstab_3 RENAME TO pivot_result", 0))
			run(fixture.Select("SELECT 'ct_addon.pivot_three(text)'::regprocedure::oid::bigint AS p,'ct_addon.pivot_result'::regtype::oid::bigint AS t", []string{"p", "t"}, []string{"INT8", "INT8"}, [][]any{{proc, typ}}))
			run(fixture.CrosstabWant("SELECT * FROM ct_generic_view ORDER BY k"))
			wrapper := fixture.Select("SELECT * FROM ct_wrapper_view ORDER BY row_name", []string{"row_name", "category_1", "category_2", "category_3"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"g1", "A", "C", nil}, {"g2", "B1", "B2", "B3"}, {"g3", nil, nil, nil}})
			run(wrapper)
			run(fixture.Prepared(wrapper))
			// Renaming the independent wrapper preserves the held generic call.
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.CrosstabWant(q, fixture.CrosstabSource))
			// Rollback retains the exact captured members and view definitions.
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DROP EXTENSION tablefunc CASCADE", 0))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.CrosstabWant("SELECT * FROM ct_generic_view ORDER BY k"))
			run(wrapper)
			run(fixture.Exec("ALTER FUNCTION ct_addon.pivot_three(text) RENAME TO crosstab3", 0))
			run(fixture.Exec("ALTER TYPE ct_addon.pivot_result RENAME TO tablefunc_crosstab_3", 0))
			crosstabInventory(t, p, b, c, "ct_addon")
			run(fixture.Exec("DROP EXTENSION tablefunc CASCADE", 0))
			for _, name := range []string{"tablefunc_crosstab_2", "tablefunc_crosstab_3", "tablefunc_crosstab_4", "ct_generic_view", "ct_wrapper_view"} {
				run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname=$1", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, name))
			}
			run(fixture.Exec("CREATE EXTENSION tablefunc SCHEMA ct_addon", 0))
			run(fixture.CrosstabWant("SELECT * FROM ct_addon.crosstab($1::text) AS t(k text,c1 text,c2 text,c3 text) ORDER BY k", fixture.CrosstabSource))
		})
	})
	t.Run("install_collision", func(t *testing.T) {
		crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TYPE tablefunc_crosstab_2 AS (sentinel integer)", 0))
			d := fixture.ControlsReferenceDiagnostic("42710", "type \"tablefunc_crosstab_2\" already exists")
			d.Context = "SQL statement \"CREATE TYPE tablefunc_crosstab_2 AS\n(\n\trow_name TEXT,\n\tcategory_1 TEXT,\n\tcategory_2 TEXT\n)\"\nextension script file \"tablefunc--1.0.sql\", near line 18"
			s := fixture.ErrorDiagnostic("CREATE EXTENSION tablefunc", d)
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='tablefunc'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='crosstab'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT '(17)'::tablefunc_crosstab_2::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"(17)"}}))
		})
	})
	t.Run("trusted_owner", func(t *testing.T) {
		crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE USER ct_owner NOSUPERUSER", 0))
			var db string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&db); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("GRANT CREATE ON DATABASE \""+db+"\" TO ct_owner", 0))
			run(fixture.Exec("CREATE SCHEMA ct_addon AUTHORIZATION ct_owner", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION ct_owner", 0))
			run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"ct_owner", "ct_owner"}}))
			run(fixture.Select("SELECT rolsuper FROM pg_roles WHERE rolname=current_user", []string{"rolsuper"}, []string{"BOOL"}, [][]any{{false}}))
			run(fixture.Exec("CREATE EXTENSION tablefunc SCHEMA ct_addon", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT r.rolname FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='tablefunc'", []string{"rolname"}, []string{"NAME"}, [][]any{{"ct_owner"}}))
			// Trusted scripts use BOOTSTRAP_SUPERUSERID, whose fixture name
			// is stuntgres on PostgreSQL and postgres natively.
			run(fixture.Select("SELECT DISTINCT p.proowner::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ct_addon'", []string{"proowner"}, []string{"INT8"}, [][]any{{int64(10)}}))
			crosstabInventory(t, p, b, c, "ct_addon")
		})
	})
}
