package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func externalLanguageData(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	routineExec(t, p, b, c,
		"CREATE FUNCTION ext_add1(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$",
		"CREATE TABLE ext_sentinel(x integer)",
		"CREATE TABLE ext_defaults(x integer DEFAULT ext_add1(4))")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ext_sentinel VALUES (7)", 1))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ext_defaults DEFAULT VALUES", 1))
}

func externalLanguageDataCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	routineValue(t, p, b, c, "SELECT ext_add1(4) AS v", "v", "INT4", int64(5))
	for _, tc := range []struct {
		q     string
		value int64
	}{
		{"SELECT x FROM ext_sentinel", 7}, {"SELECT x FROM ext_defaults", 5},
	} {
		s := fixture.Select(tc.q, []string{"x"}, []string{"INT4"}, [][]any{{tc.value}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
}

func externalLanguageDump(t *testing.T, p *fixture.Pair) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func externalLanguageRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	if b.PG {
		return
	}
	dump := externalLanguageDump(t, p)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	externalLanguageDataCheck(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ext_defaults DEFAULT VALUES", 1))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT x FROM ext_defaults ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(5)}}))
}

func TestReferenceRoutineExternalLanguagesAtomicity(t *testing.T) {
	externalLanguageScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		externalLanguageData(t, p, b, c)
		var before []byte
		if !b.PG {
			before = externalLanguageDump(t, p)
		}
		unchanged := func() {
			t.Helper()
			if !b.PG && !bytes.Equal(before, externalLanguageDump(t, p)) {
				t.Error("rejected external declaration changed native Dump")
			}
			externalLanguageDataCheck(t, p, b, c)
		}
		// The body would mutate the sentinel if improperly executed. Language
		// admission must fail before body parsing/execution and publication.
		externalLanguageMissing(t, p, b, c, "CREATE FUNCTION ext_rejected(integer) RETURNS integer LANGUAGE ext_absent AS $$INSERT INTO ext_sentinel VALUES (99); SELECT 5$$", "ext_absent")
		externalLanguageMissingCall(t, p, b, c, "ext_rejected")
		unchanged()
		for _, tc := range []struct{ q, language string }{
			{"CREATE FUNCTION ext_atomic_internal(integer) RETURNS integer LANGUAGE internal AS 'int4abs'", "internal"},
			{"CREATE FUNCTION ext_atomic_c() RETURNS language_handler LANGUAGE c AS '$libdir/plpgsql','plpgsql_call_handler'", "c"},
		} {
			if b.PG {
				routineExec(t, p, b, c, tc.q)
			} else {
				externalLanguageCapability(t, p, b, c, tc.q, tc.language)
			}
			unchanged()
		}
		externalHandlerBoundary(t, p, b, c, "ext_atomic_custom")
		unchanged()
		externalLanguageMissing(t, p, b, c, "CREATE PROCEDURE ext_rejected_proc(integer) LANGUAGE ext_absent AS $$INSERT INTO ext_sentinel VALUES (99)$$", "ext_absent")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT proname FROM pg_catalog.pg_proc WHERE proname='ext_rejected_proc'", []string{"proname"}, []string{"NAME"}, nil))
		unchanged()
		if !b.PG {
			stream := "CREATE TABLE ext_restore_prefix(x integer); INSERT INTO ext_restore_prefix VALUES (99); CREATE FUNCTION ext_restore_bad(integer) RETURNS integer LANGUAGE internal AS 'int4abs';"
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(stream))
			if err == nil {
				t.Fatal("external Restore accepted")
			}
			want := fixture.ControlsReferenceDiagnostic("0A000", "LANGUAGE internal functions are unsupported in embedded mode")
			fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &want}, "execute")
			unchanged()
		}
		routineExec(t, p, b, c,
			"CREATE FUNCTION ext_rejected(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$",
			"CREATE PROCEDURE ext_rejected_proc(integer) LANGUAGE SQL AS $$SELECT $1$$")
		routineValue(t, p, b, c, "SELECT ext_rejected(4) AS v", "v", "INT4", int64(5))
		externalLanguageDataCheck(t, p, b, c)
	})
}

func TestReferenceRoutineExternalLanguagesPreservation(t *testing.T) {
	externalLanguageScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		externalLanguageData(t, p, b, c)
		q := "SELECT ext_add1($1) AS v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		checkHeld := func() {
			t.Helper()
			s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, int64(4))
			s.CheckMetadata = routineScalarMetadata(b, "v", "INT4", false, false)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
		}
		checkHeld()
		externalLanguageMissing(t, p, b, c, "CREATE FUNCTION ext_preserve_bad(integer) RETURNS integer LANGUAGE pltcl AS 'SELECT 5'", "pltcl")
		checkHeld()
		plpgsql := "CREATE FUNCTION ext_plpgsql(integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN $1+1; END$$"
		routineExec(t, p, b, c, plpgsql)
		routineValue(t, p, b, c, "SELECT ext_plpgsql(4) AS v", "v", "INT4", int64(5))
		checkHeld()
		externalLanguageInventory(t, p, b, c)
		externalLanguageDataCheck(t, p, b, c)
		externalLanguageRoundtrip(t, p, b, c)
	})
}

func TestReferenceRoutineExternalLanguagesFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ext_smoke_reader"})
	fixture.SchemaReferenceCleanup(t, "ext_smoke_authority")
	externalLanguageScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		externalLanguageData(t, p, b, c)
		externalLanguageInventory(t, p, b, c)
		externalLanguageMissing(t, p, b, c, "CREATE FUNCTION ext_smoke_bad(integer) RETURNS integer LANGUAGE pltcl AS 'SELECT 5'", "pltcl")
		externalLanguageMissingCall(t, p, b, c, "ext_smoke_bad")
		routineExec(t, p, b, c, "CREATE ROLE ext_smoke_reader", "CREATE SCHEMA ext_smoke_authority", "GRANT USAGE, CREATE ON SCHEMA ext_smoke_authority TO ext_smoke_reader", "SET SESSION AUTHORIZATION ext_smoke_reader")
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		externalLanguageError(t, p, b, c, "CREATE FUNCTION ext_smoke_authority.ext_smoke_c() RETURNS integer LANGUAGE c AS 'missing'", fixture.ControlsReferenceDiagnostic("42501", "permission denied for language c"), false)
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "DROP SCHEMA ext_smoke_authority")
		q := "CREATE FUNCTION ext_smoke_internal(integer) RETURNS integer LANGUAGE internal AS 'int4abs'"
		if b.PG {
			routineExec(t, p, b, c, q)
			routineValue(t, p, b, c, "SELECT ext_smoke_internal(-5) AS v", "v", "INT4", int64(5))
		} else {
			externalLanguageCapability(t, p, b, c, q, "internal")
			externalLanguageMissingCall(t, p, b, c, "ext_smoke_internal")
		}
		externalHandlerBoundary(t, p, b, c, "ext_smoke_custom")
		q = "CREATE FUNCTION ext_smoke_custom_fn(integer) RETURNS integer LANGUAGE ext_smoke_custom AS $$BEGIN RETURN $1+1; END$$"
		if b.PG {
			routineExec(t, p, b, c, q)
			routineValue(t, p, b, c, "SELECT ext_smoke_custom_fn(4) AS v", "v", "INT4", int64(5))
			routineExec(t, p, b, c, "DROP FUNCTION ext_smoke_custom_fn(integer)")
		} else {
			externalLanguageMissing(t, p, b, c, q, "ext_smoke_custom")
			externalLanguageMissingCall(t, p, b, c, "ext_smoke_custom_fn")
		}
		externalLanguageDataCheck(t, p, b, c)
		externalLanguageRoundtrip(t, p, b, c)
	})
}
