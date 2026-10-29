package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncNormalCatalogCosts(t *testing.T) {
	normalCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE FUNCTION normal_part.cost_sql() RETURNS integer LANGUAGE SQL AS $$SELECT 7$$", 0))
		run(fixture.Exec("CREATE FUNCTION normal_part.cost_pl() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$", 0))

		// Released declaration costs, not values learned from the native backend:
		// pg_proc.dat supplies internal COST 10/100; system_functions.sql replaces
		// lpad(text,integer) with SQL COST 1. Ordinary SQL/PL declarations default
		// to 100, while tablefunc's C declaration and internal abs default to 1.
		for _, tc := range []struct {
			schema, name, args, language string
			cost                         float64
		}{
			{"normal_part", "normal_rand", "23 701 701", "c", 1},
			{"pg_catalog", "pg_table_is_visible", "26", "internal", 10},
			{"pg_catalog", "pg_get_catalog_foreign_keys", "", "internal", 10},
			{"pg_catalog", "to_tsvector", "3734 25", "internal", 100},
			{"pg_catalog", "lpad", "25 23", "sql", 1},
			{"pg_catalog", "abs", "23", "internal", 1},
			{"normal_part", "cost_sql", "", "sql", 100},
			{"normal_part", "cost_pl", "", "plpgsql", 100},
		} {
			s := fixture.Select("SELECT p.procost,l.lanname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname=$1 AND p.proname=$2 AND p.proargtypes::text=$3",
				[]string{"procost", "lanname"}, []string{"FLOAT4", "NAME"}, [][]any{{tc.cost, tc.language}}, tc.schema, tc.name, tc.args)
			run(s)
			run(fixture.Prepared(s))
		}

		run(fixture.Exec("CREATE SCHEMA normal_moved", 0))
		for _, tc := range []struct {
			name, signature, language string
			cost                      float64
		}{
			{"normal_rand", "integer,float8,float8", "c", 1},
			{"cost_sql", "", "sql", 100},
			{"cost_pl", "", "plpgsql", 100},
		} {
			var original int64
			if err := c.QueryRowContext(p.Ctx, "SELECT p.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='normal_part' AND p.proname=$1", tc.name).Scan(&original); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("ALTER FUNCTION normal_part."+tc.name+"("+tc.signature+") RENAME TO "+tc.name+"_renamed", 0))
			run(fixture.Exec("ALTER FUNCTION normal_part."+tc.name+"_renamed("+tc.signature+") SET SCHEMA normal_moved", 0))
			// Locate by the pre-alter identity: a replacement row or a name-based
			// default cannot satisfy this declaration-retention witness.
			s := fixture.Select("SELECT p.proname,n.nspname,p.procost,l.lanname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=$1::oid",
				[]string{"proname", "nspname", "procost", "lanname"}, []string{"NAME", "NAME", "FLOAT4", "NAME"}, [][]any{{tc.name + "_renamed", "normal_moved", tc.cost, tc.language}}, original)
			run(s)
			run(fixture.Prepared(s))
			if tc.name == "normal_rand" {
				source := fixture.Select("SELECT p.prosrc,p.prorows,d.refobjid=e.oid AS member FROM pg_proc p JOIN pg_depend d ON d.objid=p.oid AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid=$1::oid AND e.extname='tablefunc'", []string{"prosrc", "prorows", "member"}, []string{"TEXT", "FLOAT4", "BOOL"}, [][]any{{"normal_rand", float64(1000), true}}, original)
				run(source)
				run(fixture.Prepared(source))
				rows := fixture.TablefuncNormalSelect("SELECT * FROM normal_moved.normal_rand_renamed(2,9,0)", 2, 9)
				rows.Columns = []string{"normal_rand_renamed"}
				run(rows)
			}
		}
	})
}
