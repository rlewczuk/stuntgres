package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const cubeWrittenPrivate = "cube_default_private"
const cubeWrittenCaller = "cube_default_caller"
const cubeWrittenRole = "cube_default_reader"

func cubeWrittenDefaultChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, routineOwner string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, s := range []fixture.Step{
		fixture.Select("SELECT proowner=(SELECT oid FROM pg_roles WHERE rolname='"+routineOwner+"') AS owner_identity FROM pg_proc WHERE oid='"+cubeWrittenCaller+".f("+cubeWrittenPrivate+".cube)'::regprocedure::oid", []string{"owner_identity"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select("SELECT relowner=(SELECT oid FROM pg_roles WHERE rolname='"+cubeWrittenRole+"') AS owner_identity FROM pg_class WHERE oid='"+cubeWrittenCaller+".omitted'::regclass::oid", []string{"owner_identity"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select("SELECT value FROM "+cubeWrittenCaller+".explicit", []string{"value"}, []string{"BOOL"}, [][]any{{false}}),
	} {
		run(s)
		run(fixture.Prepared(s))
	}
	run(fixture.Exec("SET SESSION AUTHORIZATION "+cubeWrittenRole, 0))
	defer func() {
		if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Error(err)
		}
	}()
	for _, s := range []fixture.Step{
		fixture.Select("SELECT "+cubeWrittenCaller+".f() AS value", []string{"value"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select("SELECT value FROM "+cubeWrittenCaller+".omitted", []string{"value"}, []string{"BOOL"}, [][]any{{true}}),
	} {
		run(s)
		run(fixture.Prepared(s))
	}
	denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema "+cubeWrittenPrivate)
	denied.Position = "13"
	for _, prepared := range []bool{false, true} {
		cubeError(t, p, b, c, "SELECT '1'::"+cubeWrittenPrivate+".cube", prepared, true, true, denied)
	}
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}

func TestReferenceCubeComparisonWrittenDefaults(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{cubeWrittenRole})
	fixture.SchemaReferenceCleanup(t, cubeWrittenPrivate, cubeWrittenCaller)
	cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		phase := "setup"
		run := func(s fixture.Step) {
			if !t.Run(phase+"/"+s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }) {
				t.FailNow()
			}
		}
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		for _, q := range []string{
			"CREATE USER " + cubeWrittenRole,
			"CREATE SCHEMA " + cubeWrittenPrivate,
			"CREATE SCHEMA " + cubeWrittenCaller + " AUTHORIZATION " + cubeWrittenRole,
			"CREATE EXTENSION cube SCHEMA " + cubeWrittenPrivate,
			"SET search_path=" + cubeWrittenPrivate + ",pg_catalog",
			"CREATE FUNCTION " + cubeWrittenCaller + ".f(p " + cubeWrittenPrivate + ".cube DEFAULT NULLIF('1'::" + cubeWrittenPrivate + ".cube,'1'::" + cubeWrittenPrivate + ".cube)) RETURNS boolean LANGUAGE SQL RETURN p IS NULL",
			"CREATE VIEW " + cubeWrittenCaller + ".explicit AS SELECT " + cubeWrittenCaller + ".f(NULLIF('2'::" + cubeWrittenPrivate + ".cube,'1'::" + cubeWrittenPrivate + ".cube)) AS value",
			"SET SESSION AUTHORIZATION " + cubeWrittenRole,
			"CREATE VIEW " + cubeWrittenCaller + ".omitted AS SELECT " + cubeWrittenCaller + ".f() AS value",
			"RESET SESSION AUTHORIZATION",
		} {
			run(fixture.Exec(q, 0))
		}
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		cubeWrittenDefaultChecks(t, p, b, c, owner)
		if b.PG {
			return
		}
		phase = "native-control"
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatalf("written default comparison Dump: %#v", err)
		}
		text := dump.String()
		if !cubeComparisonDeclarationBound(t, text, "CREATE FUNCTION", `"cube_default_caller"."f"(`) {
			t.Fatal("routine declaration lost its checked-default binding window")
		}
		if cubeComparisonDeclarationBound(t, text, "CREATE VIEW", `"cube_default_caller"."omitted"`) {
			t.Fatal("omitted default leaked a comparison window into caller declaration")
		}
		if !cubeComparisonDeclarationBound(t, text, "CREATE VIEW", `"cube_default_caller"."explicit"`) {
			t.Fatal("explicit written comparison argument lost caller binding window")
		}
		run(fixture.Exec("SET search_path="+cubeWrittenCaller, 0))
		run(fixture.Exec("SET extra_float_digits=2", 0))
		settings := cubeComparisonSessionSettings(t, p, c)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(text)); err != nil {
			t.Fatalf("written default comparison Restore: %#v", err)
		}
		if got := cubeComparisonSessionSettings(t, p, c); got != settings {
			t.Fatal("written-default Restore changed caller settings", got, settings)
		}
		cubeWrittenDefaultChecks(t, p, b, c, owner)
		target := fixture.NewFixture(t, false, true)
		destination, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer destination.Close()
		if _, err := destination.ExecContext(target.Ctx, "CREATE USER "+cubeWrittenRole); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(text)); err != nil {
			t.Fatalf("fresh written default comparison Restore: %#v", err)
		}
		cubeWrittenDefaultChecks(t, target, fixture.Backend{Recorder: target.MemoryRecord}, destination, "postgres")
		if p.Postgres != nil {
			pgc, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pgc.Close()
			cleanup := "DROP SCHEMA IF EXISTS " + cubeWrittenCaller + " CASCADE; DROP SCHEMA IF EXISTS " + cubeWrittenPrivate + " CASCADE"
			if _, err := pgc.ExecContext(p.Ctx, cleanup); err != nil {
				t.Fatal(err)
			}
			if _, err := pgc.ExecContext(p.Ctx, "CREATE USER "+cubeWrittenRole); err != nil {
				t.Fatal("provision external replay owner", err)
			}
			defer func() {
				if _, err := pgc.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION; "+cleanup+"; DROP ROLE "+cubeWrittenRole); err != nil {
					t.Error(err)
				}
			}()
			if _, err := pgc.ExecContext(p.Ctx, text); err != nil {
				t.Fatalf("actual written-default dump PostgreSQL replay: %#v", err)
			}
			if _, err := pgc.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Fatal(err)
			}
			cubeWrittenDefaultChecks(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pgc, "postgres")
		}
	})
}
