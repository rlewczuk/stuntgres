package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorFor(t *testing.T) {
	plCursorCases(t, []plCursorCase{
		{"sum", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; n integer:=0;", "FOR r IN c LOOP n:=n+r.i; END LOOP;", "n", "integer", int64(60)},
		{"bound_args", "c CURSOR(a integer) FOR SELECT i FROM pc_rows WHERE i>a ORDER BY i; n integer:=0;", "FOR r IN c(10) LOOP n:=n+r.i; END LOOP;", "n", "integer", int64(50)},
		{"found_nonempty", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i;", "FOR r IN c LOOP END LOOP;", "FOUND", "boolean", true},
		{"found_empty", "c CURSOR FOR SELECT i FROM pc_rows WHERE false;", "FOR r IN c LOOP END LOOP;", "FOUND", "boolean", false},
		{"repeat", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; n integer:=0;", "FOR r IN c LOOP n:=n+r.i; END LOOP; FOR r IN c LOOP n:=n+r.i; END LOOP;", "n", "integer", int64(120)},
		{"record_shadows_outer", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; r integer:=99; n integer:=0;", "FOR r IN c LOOP n:=n+r.i; END LOOP;", "r", "integer", int64(99)},
		{"restore_null", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i;", "FOR r IN c LOOP END LOOP;", "c IS NULL", "boolean", true},
		{"preserve_explicit", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i;", "c:='pc_loop'; FOR r IN c LOOP END LOOP;", "c", "refcursor", "pc_loop"},
		{"early_return", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i;", "c:='pc_early'; FOR r IN c LOOP RETURN r.i; END LOOP;", "0", "integer", int64(10)},
		{"body_moves_shared_portal", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; n integer:=0;", "FOR r IN c LOOP n:=n+r.i; MOVE FORWARD 1 FROM c; END LOOP;", "n", "integer", int64(40)},
		{"found_only_on_exit", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; f boolean:=false;", "FOR r IN c LOOP f:=f OR FOUND; END LOOP;", "NOT f AND FOUND", "boolean", true},
		{"body_fetch_changes_found", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; d refcursor; x integer; f boolean:=true;", "OPEN d FOR SELECT i FROM pc_rows WHERE false; FOR r IN c LOOP FETCH d INTO x; f:=f AND NOT FOUND; END LOOP; CLOSE d;", "f AND FOUND", "boolean", true},
	})
	for _, kind := range []string{"normal", "return", "error"} {
		t.Run("cleanup_"+kind, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorRows(t, p, b, c)
				statement := "n:=n+r.i;"
				if kind == "return" {
					statement = "RETURN r.i;"
				}
				if kind == "error" {
					statement = "n:=1/(r.i-10);"
				}
				plCursorFunction(t, p, b, c, "pc_cleanup", "", "integer", "DECLARE c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; n integer:=0; BEGIN c:='pc_owned_loop'; FOR r IN c LOOP "+statement+" END LOOP; RETURN n; END")
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				if kind == "error" {
					want := plCursorRuntime("22012", "division by zero", "pc_cleanup()", "assignment")
					cachedPG := want
					cachedPG.Context = "PL/pgSQL assignment \"n:=1/(r.i-10)\"\n" + want.Context
					plCursorError(t, p, b, c, "SELECT pc_cleanup() AS v", want, true, true, cachedPG)
				} else {
					n := int64(60)
					if kind == "return" {
						n = 10
					}
					plCursorValue(t, p, b, c, "SELECT pc_cleanup() AS v", "INT4", n)
				}
				plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name='pc_owned_loop'", "INT8", int64(0))
			})
		})
	}
}
