package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently expected publication and loop ownership boundaries, including
// promoted motion, mutable reference values and protected portal teardown.
func TestReferencePLpgSQLCursorPortalOwnership(t *testing.T) {
	for _, action := range []string{"fetch", "move"} {
		for _, transaction := range []string{"autocommit", "commit", "rollback"} {
			for _, prepared := range []bool{false, true} {
				name := "held_publication/" + action + "/" + transaction + map[bool]string{false: "/direct", true: "/prepared"}[prepared]
				t.Run(name, func(t *testing.T) {
					sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						plCursorRows(t, p, b, c)
						typ, metadata := "integer", "INT4"
						var want any = int64(10)
						body := "DECLARE x integer; BEGIN FETCH NEXT FROM p INTO x; RETURN x; END"
						if action == "move" {
							typ, metadata, want = "boolean", "BOOL", true
							body = "BEGIN MOVE FORWARD 1 FROM p; RETURN FOUND; END"
						}
						sqlCursorStep(t, p, b, c, fixture.Exec("CREATE TABLE pc_held_sink(v "+typ+")", 0))
						plCursorFunction(t, p, b, c, "pc_held_step", "p refcursor", typ, body)
						sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
						sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE pc_held SCROLL CURSOR WITH HOLD FOR SELECT i FROM pc_rows ORDER BY i", 0))
						sqlCursorStep(t, p, b, c, fixture.Exec("COMMIT", 0))
						if transaction != "autocommit" {
							sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
						}
						write := fixture.Exec("INSERT INTO pc_held_sink SELECT pc_held_step('pc_held'::refcursor)", 1)
						write.Prepared = prepared
						sqlCursorStep(t, p, b, c, write)
						rows := [][]any{{want}}
						sqlCursorStep(t, p, b, c, fixture.Select("SELECT v FROM pc_held_sink", []string{"v"}, []string{metadata}, rows))
						if transaction != "autocommit" {
							command := "COMMIT"
							if transaction == "rollback" {
								command, rows = "ROLLBACK", nil
							}
							sqlCursorStep(t, p, b, c, fixture.Exec(command, 0))
							sqlCursorStep(t, p, b, c, fixture.Select("SELECT v FROM pc_held_sink", []string{"v"}, []string{metadata}, rows))
						}
						// Movement belongs to the existing held portal even when the
						// containing write is rolled back; its first row was consumed.
						sqlCursorStep(t, p, b, c, fixture.Select("FETCH NEXT FROM pc_held", []string{"i"}, []string{"INT4"}, [][]any{{int64(20)}}))
						sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pc_held", 0))
					})
				})
			}
		}
	}

	for _, initial := range []string{"null", "explicit"} {
		for _, assignment := range []struct{ name, expression, result string }{
			{"missing", "'pc_redirect_missing'", "pc_redirect_missing"},
			{"null", "NULL", "NULL"},
			{"other_live", "'pc_other_live'", "pc_other_live"},
		} {
			t.Run("loop_identity/"+initial+"/"+assignment.name, func(t *testing.T) {
				sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					plCursorRows(t, p, b, c)
					prefix, suffix := "", "NULL"
					if initial == "explicit" {
						prefix, suffix = "c:='pc_loop_original';", assignment.result
					}
					body := "DECLARE c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; n integer:=0; BEGIN " + prefix + " FOR r IN c LOOP n:=n+r.i; c:=" + assignment.expression + "; END LOOP; RETURN n::text||'/'||coalesce(c::text,'NULL'); END"
					plCursorFunction(t, p, b, c, "pc_loop_identity", "", "text", body)
					sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
					sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE pc_other_live SCROLL CURSOR FOR SELECT i FROM (VALUES(100),(200)) AS other_rows(i) ORDER BY i", 0))
					// Direct, fresh prepared, then two invocations of one held
					// statement must all retain the original 10/20/30 producer.
					plCursorValue(t, p, b, c, "SELECT pc_loop_identity() AS v", "TEXT", "60/"+suffix)
					plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name<>'' AND name<>'pc_other_live'", "INT8", int64(0))
					sqlCursorStep(t, p, b, c, fixture.Select("FETCH NEXT FROM pc_other_live", []string{"i"}, []string{"INT4"}, [][]any{{int64(100)}}))
					sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pc_other_live", 0))
					sqlCursorStep(t, p, b, c, fixture.Exec("COMMIT", 0))
				})
			})
		}
	}

	for _, route := range []string{"close_then_return", "copied_reference", "helper"} {
		t.Run("pinned_close/"+route, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorRows(t, p, b, c)
				sqlCursorStep(t, p, b, c, fixture.Exec("CREATE TABLE pc_pin_sink(v integer)", 0))
				sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO pc_pin_sink VALUES(7)", 1))
				statement := "CLOSE c;"
				if route == "copied_reference" {
					statement = "alias_ref:=c; CLOSE alias_ref;"
				}
				if route == "helper" {
					plCursorFunction(t, p, b, c, "pc_pin_drop", "p refcursor", "integer", "BEGIN CLOSE p; RETURN 0; END")
					statement = "n:=pc_pin_drop(c);"
				}
				plCursorFunction(t, p, b, c, "pc_pin_case", "", "integer", "DECLARE c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; alias_ref refcursor; n integer:=0; BEGIN c:='pc_pinned'; FOR r IN c LOOP "+statement+" RETURN r.i; END LOOP; RETURN 0; END")
				plCursorFunction(t, p, b, c, "pc_pin_recover", "", "integer", "DECLARE c CURSOR FOR SELECT i FROM pc_rows ORDER BY i; n integer:=0; BEGIN c:='pc_pinned'; FOR r IN c LOOP n:=n+r.i; END LOOP; RETURN n; END")
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				// Full PostgreSQL18.6 diagnostic records differ only in the
				// prepared helper call's incidental cached-assignment trace.
				want := plCursorRuntime("24000", "cannot drop pinned portal \"pc_pinned\"", "pc_pin_case()", "CLOSE")
				cachedPG := want
				if route == "helper" {
					inner := "PL/pgSQL function pc_pin_drop(refcursor) line 1 at CLOSE"
					outer := "PL/pgSQL function pc_pin_case() line 1 at assignment"
					want.Context = inner + "\n" + outer
					cachedPG = want
					cachedPG.Context = inner + "\nPL/pgSQL assignment \"n:=pc_pin_drop(c)\"\n" + outer
				}
				plCursorError(t, p, b, c, "UPDATE pc_pin_sink SET v=pc_pin_case()", want, false, true, cachedPG)
				plCursorValue(t, p, b, c, "SELECT v FROM pc_pin_sink AS v", "INT4", int64(7))
				plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name='pc_pinned'", "INT8", int64(0))
				plCursorValue(t, p, b, c, "SELECT pc_pin_recover() AS v", "INT4", int64(60))
				sqlCursorStep(t, p, b, c, fixture.Exec("COMMIT", 0))
			})
		})
	}
}
