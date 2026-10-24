package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func earthCoordinateNewDefault(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_coordinate_defaults(id) VALUES (2)", 1))
	earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT r,latitude(p) AS plat,longitude(p) AS plon,lat,lon,chord,arc FROM earth_coordinate_defaults WHERE id=2", []string{"r", "plat", "plon", "lat", "lon", "chord", "arc"}, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(6378168), float64(0), float64(0), float64(0), float64(0), float64(0), float64(0)}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM earth_coordinate_defaults WHERE id=2", 1))
}

func earthCoordinateGeneration(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	q := "SELECT public.radius_now() AS r,latitude(earth_coordinate_control_moved.ll_to_earth($1::float8,$2::float8)) AS lat,longitude(earth_coordinate_control_moved.ll_to_earth($1::float8,$2::float8)) AS lon,sec_to_gc($3::float8) AS arc,gc_to_sec($3::float8) AS chord"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, xy := range [][]any{{float64(0), float64(0), float64(0)}, {nil, nil, nil}, {float64(0), float64(90), float64(0)}} {
		want := []any{float64(6378168), xy[0], xy[1], xy[2], xy[2]}
		s := fixture.Select(q, []string{"r", "lat", "lon", "arc", "chord"}, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{want}, xy...)
		earthCoordinateBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
	}
	earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT proowner=(SELECT oid FROM pg_roles WHERE rolname='earth_coordinate_control_owner') AS owner FROM pg_proc WHERE oid='public.radius_now()'::regprocedure", []string{"owner"}, []string{"BOOL"}, [][]any{{true}}))
}

func earthCoordinateNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "ALTER FUNCTION") || !strings.Contains(dump.String(), "radius_now") {
		t.Fatal("dump lost current provider identities", dump.String())
	}
	old, err := c.QueryContext(p.Ctx, "SELECT id,p FROM earth_coordinate_defaults ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata := cubeMetadata(t, false, old, []string{"id", "p"}, []bool{true, true})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	earthCoordinateStoredVerify(t, p, b, c)
	earthCoordinateNewDefault(t, p, b, c)
	earthCoordinateGeneration(t, p, b, c)
	cubeConsumeRetained(t, old, [][]any{{int64(1), "(6378168, 0, 0)"}})
	cubeColumnMetadata(t, false, metadata, []string{"id", "p"}, []bool{true, true})
	target := fixture.NewFixture(t, false, true)
	for _, q := range []string{"CREATE ROLE earth_coordinate_control_owner", "CREATE TABLE earth_coordinate_allocator(v integer)"} {
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, 0))
	}
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	earthCoordinateStoredVerify(t, target, tb, target.Memory)
	earthCoordinateNewDefault(t, target, tb, target.Memory)
	earthCoordinateGeneration(t, target, tb, target.Memory)
	// Every failed late replay must leave both stored expressions and data intact.
	bad := dump.String() + "\nINSERT INTO earth_coordinate_defaults(id,p) VALUES (99,'(0)');\n"
	want := earthCheckDiagnostic("public", "on_surface", true)
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	earthCoordinateStoredVerify(t, target, tb, target.Memory)
	earthCoordinateNewDefault(t, target, tb, target.Memory)
	var after bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("late restore changed target dump", after.String())
	}
	// Reprepare against the destination generation, never rely on a pre-reset plan.
	earthCoordinateBoth(t, target, tb, target.Memory, fixture.Select("SELECT public.radius_now() AS r,latitude(earth_coordinate_control_moved.ll_to_earth(0,0)) AS lat", []string{"r", "lat"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(6378168), float64(0)}}))
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS earthdistance CASCADE; DROP EXTENSION IF EXISTS cube CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public; DROP SCHEMA IF EXISTS earth_coordinate_control_moved CASCADE"); err != nil {
			t.Fatal(err)
		}
		// Logical dumps reference cluster roles; provision the known owner.
		if _, err := p.Postgres.ExecContext(p.Ctx, "CREATE ROLE earth_coordinate_control_owner"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted coordinate SQL", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		earthCoordinateStoredVerify(t, p, pg, p.Postgres)
		earthCoordinateNewDefault(t, p, pg, p.Postgres)
		earthCoordinateGeneration(t, p, pg, p.Postgres)
		// Leave the oracle clean for the ordinary PostgreSQL backend scenario.
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP EXTENSION earthdistance CASCADE; DROP EXTENSION cube CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public; DROP SCHEMA earth_coordinate_control_moved CASCADE; DROP ROLE earth_coordinate_control_owner"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceEarthCoordinateControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"earth_coordinate_control_owner"})
	fixture.SchemaReferenceCleanup(t, "earth_coordinate_control_moved")
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		earthCoordinateStoredSetup(t, p, b, c)
		earthCoordinateStoredVerify(t, p, b, c)
		earthCoordinateNewDefault(t, p, b, c)
		for _, q := range []string{"DROP VIEW earth_coordinate_view", "DROP TABLE earth_coordinate_defaults", "DROP TABLE earth_coordinate_input", "DROP EXTENSION earthdistance", "DROP EXTENSION cube"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		earthInstall(t, p, b, c)
		earthCoordinateStoredSetup(t, p, b, c)
		earthCoordinateStoredVerify(t, p, b, c)
		for _, q := range []string{"CREATE ROLE earth_coordinate_control_owner", "CREATE SCHEMA earth_coordinate_control_moved", "ALTER FUNCTION earth() RENAME TO radius_now", "ALTER FUNCTION ll_to_earth(float8,float8) SET SCHEMA earth_coordinate_control_moved", "ALTER FUNCTION radius_now() OWNER TO earth_coordinate_control_owner"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		earthCoordinateStoredVerify(t, p, b, c)
		earthCoordinateNewDefault(t, p, b, c)
		earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT public.radius_now() AS r,latitude(earth_coordinate_control_moved.ll_to_earth(0,0)) AS lat", []string{"r", "lat"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(6378168), float64(0)}}))
		earthCoordinateGeneration(t, p, b, c)
		if !b.PG {
			earthCoordinateNativeControls(t, p, b, c)
		}
	})
}
