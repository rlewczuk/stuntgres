package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Independent expectations: REL_18_6 gram.y (LOAD file_name/Sconst and
// preparable statements) and dfmgr.c (restriction before expansion/lookup).
// These constants are not learned from the native implementation.
func TestReferenceLoadSyntax(t *testing.T) {
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ sql, message, position string }{
			{"LOAD", "syntax error at end of input", "5"},
			{"LOAD missing", "syntax error at or near \"missing\"", "6"},
			{"LOAD $1", "syntax error at or near \"$1\"", "6"},
			{"LOAD 'x' garbage", "syntax error at or near \"garbage\"", "10"},
			{"PREPARE l AS LOAD 'x'", "syntax error at or near \"LOAD\"", "14"},
		} {
			t.Run(tc.sql, func(t *testing.T) {
				want := loadDiagnostic("42601", tc.message)
				want.Position = tc.position
				loadError(t, p, b, c, tc.sql, want, true)
			})
		}
		loadExec(t, p, b, c, "CREATE TABLE load(load INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO load VALUES(7)", 1))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT load FROM load", []string{"load"}, []string{"INT4"}, [][]any{{int64(7)}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		loadRole(t, p, b, c, "load_syntax_reader", "")
		loadExec(t, p, b, c, "SET ROLE load_syntax_reader")
		for _, tc := range []struct{ sql, decoded string }{
			{"lOaD /* filename */ 'missing';", "missing"},
			{"LOAD 'quo''te'", "quo'te"},
			{`LOAD E'line\nname'`, "line\nname"},
			{`LOAD U&'caf\00E9'`, "café"},
			{"LOAD $name$quoted$name$", "quoted"},
			{"LOAD 'join'\n'ed'", "joined"},
		} {
			t.Run(tc.sql, func(t *testing.T) {
				loadError(t, p, b, c, tc.sql, loadDenied(tc.decoded), false)
			})
		}
	})
}

func TestReferenceLoadAuthorization(t *testing.T) {
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		loadRole(t, p, b, c, "load_auth_super", "SUPERUSER")
		loadRole(t, p, b, c, "load_auth_reader", "INHERIT")
		loadExec(t, p, b, c, "GRANT load_auth_super TO load_auth_reader WITH INHERIT TRUE, SET TRUE")
		loadExec(t, p, b, c, "SET ROLE load_auth_reader")
		for _, filename := range []string{
			loadMissing, "$libdir/plpgsql", "$libdir/plugins/../missing",
			"$libdir/plugins/a/b", "$libdir/plugins//missing",
			"$LIBDIR/plugins/missing", "$libdir/Plugins/missing",
			"$bad/missing", "", "/tmp/stuntgres_load_contract_absent/missing",
		} {
			t.Run(filename, func(t *testing.T) {
				loadError(t, p, b, c, loadSQL(filename), loadDenied(filename), false)
			})
		}
		loadError(t, p, b, c, loadSQL(loadPlugin), loadBoundary(b, loadPlugin), false)
		// Linux treats a backslash in the basename literally, not as traversal.
		filename := "$libdir/plugins/stuntgres_load_contract_back\\slash"
		loadError(t, p, b, c, loadSQL(filename), loadBoundary(b, filename), false)
	})
}

func TestReferenceLoadResolution(t *testing.T) {
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, filename := range []string{"$bad/missing", "$LIBDIR/missing"} {
			t.Run(filename, func(t *testing.T) {
				loadError(t, p, b, c, loadSQL(filename), loadDiagnostic("42602", "invalid macro name in path: "+filename), false)
			})
		}
		for _, filename := range []string{
			loadMissing, loadPlugin, "$unknown",
			"/tmp/stuntgres_load_contract_absent/missing",
			"stuntgres_load_contract_absent/missing",
		} {
			t.Run(filename, func(t *testing.T) {
				loadError(t, p, b, c, loadSQL(filename), loadBoundary(b, filename), false)
			})
		}
		// The container precondition proves the installed shared object exists;
		// pg_extension alone is not evidence that LOAD can open that library.
		for range 2 {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("LOAD 'plpgsql'", 0)
				if !b.PG {
					s = fixture.ErrorDiagnostic("LOAD 'plpgsql'", loadDiagnostic("0A000", loadUnsupported))
					s.ErrorStage = "execute"
				}
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}
