package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityCheckIdentity(t *testing.T) {
	pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pvRun(t, p, b, c, fixture.Exec("CREATE SCHEMA pv_moved", 0))
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1))
		pvFreeze(t, p, b, c)
		original := pvOID(t, p, c, "pv_part.heap")
		for _, q := range []string{
			"CREATE VIEW pv_part.visible_source AS SELECT * FROM pv_part.pg_check_visible('pv_part.heap'::regclass)",
			"CREATE VIEW pv_part.frozen_source AS SELECT * FROM pv_part.pg_check_frozen('pv_part.heap'::regclass)",
			"CREATE VIEW pv_part.bound_mutator AS SELECT pv_part.pg_truncate_visibility_map('pv_part.heap'::regclass) IS NULL AS was_null",
			"CREATE VIEW pv_part.dynamic_mutator AS SELECT pv_part.pg_truncate_visibility_map('pv_part.heap'::text::regclass) IS NULL AS was_null",
		} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		pvOneFrozen(t, p, b, c) // CREATE VIEW did not execute a VOLATILE mutator.
		for _, q := range []string{"ALTER TABLE pv_part.heap RENAME TO kept", "ALTER TABLE pv_part.kept SET SCHEMA pv_moved", "CREATE TABLE pv_part.heap(k integer)", "INSERT INTO pv_part.heap VALUES(2)", "VACUUM (FREEZE) pv_part.heap"} {
			n := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				n = 1
			}
			pvRun(t, p, b, c, fixture.Exec(q, n))
		}
		if got := pvOID(t, p, c, "pv_moved.kept"); got != original {
			t.Fatal("rename changed relation identity", got, original)
		}
		probeSchema := "pv_part"
		post := func() {
			for _, view := range []string{"visible_source", "frozen_source"} {
				pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part."+view+" ORDER BY t_ctid", []string{"t_ctid"}, []string{"TID"}, nil))
			}
			pvBoth(t, p, b, c, fixture.Select("SELECT was_null FROM pv_part.bound_mutator", []string{"was_null"}, []string{"BOOL"}, [][]any{{false}}))
			pvBoth(t, p, b, c, fixture.Select("SELECT * FROM "+probeSchema+".pg_visibility($1::regclass,0)", pvPageNames, pvPageTypes, [][]any{{false, false, true}}, original))
			pvBoth(t, p, b, c, fixture.Select("SELECT * FROM "+probeSchema+".pg_visibility('pv_part.heap'::regclass,0)", pvPageNames, pvPageTypes, [][]any{{true, true, true}}))
		}
		post()
		for i, fn := range pvcFunctions {
			renamed := "renamed_" + fn
			for _, q := range []string{"ALTER FUNCTION pv_part." + fn + "(regclass) RENAME TO " + renamed, "ALTER FUNCTION pv_part." + renamed + "(regclass) SET SCHEMA pv_moved"} {
				pvRun(t, p, b, c, fixture.Exec(q, 0))
				post()
			}
			var definition string
			view := "visible_source"
			if i == 1 {
				view = "frozen_source"
			}
			if i == 2 {
				view = "bound_mutator"
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef($1::regclass,true)", "pv_part."+view).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(definition, renamed) {
				t.Fatal("deparse lost current routine identity", definition)
			}
			for _, q := range []string{"ALTER FUNCTION pv_moved." + renamed + "(regclass) SET SCHEMA pv_part", "ALTER FUNCTION pv_part." + renamed + "(regclass) RENAME TO " + fn} {
				pvRun(t, p, b, c, fixture.Exec(q, 0))
			}
		}
		pvRun(t, p, b, c, fixture.Exec("ALTER EXTENSION pg_visibility SET SCHEMA pv_moved", 0))
		probeSchema = "pv_moved"
		post()
		for _, fn := range pvcFunctions {
			pvRun(t, p, b, c, fixture.Exec("CREATE FUNCTION pv_part."+fn+"(regclass) RETURNS integer LANGUAGE SQL AS 'SELECT 77'", 0))
			pvBoth(t, p, b, c, fixture.Select("SELECT pv_part."+fn+"('pv_part.heap') AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(77)}}))
			post()
			pvRun(t, p, b, c, fixture.Exec("DROP FUNCTION pv_part."+fn+"(regclass)", 0))
		}
		// Dynamic text resolves the reused relation name, not the stored OID.
		pvBoth(t, p, b, c, fixture.Select("SELECT was_null FROM pv_part.dynamic_mutator", []string{"was_null"}, []string{"BOOL"}, [][]any{{false}}))
		pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_moved.pg_visibility('pv_part.heap'::regclass,0)", pvPageNames, pvPageTypes, [][]any{{false, false, true}}))
		for _, q := range []string{"DROP EXTENSION pg_visibility CASCADE", "CREATE EXTENSION pg_visibility SCHEMA pv_part"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		pvcChecks(t, p, b, c, original, nil)
		pvcTruncate(t, p, b, c, original)
		pvBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pv_part' AND c.relname IN ('visible_source','frozen_source','bound_mutator','dynamic_mutator')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
