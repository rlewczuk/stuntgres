package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func earthPointStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE earth_point_input(id integer,a point,b point)",
		"CREATE VIEW earth_point_view AS SELECT id,a <@> b AS operator_miles,geo_distance(a,b) AS function_miles FROM earth_point_input",
		"CREATE TABLE earth_point_defaults(id integer,a point DEFAULT point '(0,60)',b point DEFAULT point '(1,60)',operator_miles float8 DEFAULT (point '(0,60)' <@> point '(1,60)'),function_miles float8 DEFAULT geo_distance(point '(0,60)',point '(1,60)'),CONSTRAINT earth_point_limit CHECK (a <@> b < 70))",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_point_input VALUES (1,'(0,60)','(1,60)'),(2,'(0,0)','(1,0)'),(3,NULL,'(1,0)')", 3))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_point_defaults(id) VALUES (1)", 1))
}

func earthPointStoredVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	earthCoordinateNumbers(t, p, b, c, "SELECT id,operator_miles,function_miles FROM earth_point_view ORDER BY id", []string{"id", "operator_miles", "function_miles"}, [][]any{{int64(1), "34.54626", "34.54626"}, {int64(2), "69.09318", "69.09318"}, {int64(3), nil, nil}}, []float64{0, 0, 0})
	earthCoordinateNumbers(t, p, b, c, "SELECT id,operator_miles,function_miles,a <@> b AS checked_miles FROM earth_point_defaults ORDER BY id", []string{"id", "operator_miles", "function_miles", "checked_miles"}, [][]any{{int64(1), "34.54626", "34.54626", "34.54626"}}, []float64{0, 0, 0, 0})
}

func earthPointNewDefault(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_point_defaults(id) VALUES (2)", 1))
	earthCoordinateNumbers(t, p, b, c, "SELECT operator_miles,function_miles,a <@> b AS checked_miles FROM earth_point_defaults WHERE id=2", []string{"operator_miles", "function_miles", "checked_miles"}, [][]any{{"34.54626", "34.54626", "34.54626"}}, []float64{0, 0, 0})
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM earth_point_defaults WHERE id=2", 1))
}

const earthPointInvalidWrite = "INSERT INTO earth_point_defaults VALUES (99,'(0,0)','(180,0)',NULL,NULL)"

func earthPointCheckError() fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "earth_point_defaults" violates check constraint "earth_point_limit"`)
	want.Detail = "Failing row contains (99, (0,0), (180,0), null, null)."
	want.Schema, want.Table, want.Constraint = "public", "earth_point_defaults", "earth_point_limit"
	return want
}

func earthPointCurrentVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, moved bool) {
	t.Helper()
	schema, name, owner := "public", "geo_distance", ""
	if moved {
		schema, name, owner = "earth_point_moved", "miles_now", "earth_point_owner"
	}
	for _, s := range earthPointCatalogChecks(schema, name, owner) {
		earthCoordinateBoth(t, p, b, c, s)
	}
	earthCoordinateNumbers(t, p, b, c, "SELECT "+schema+"."+name+"(point '(0,60)',point '(1,60)') AS miles", []string{"miles"}, [][]any{{"34.54626"}}, []float64{0})
	earthPointStoredVerify(t, p, b, c)
	earthPointNewDefault(t, p, b, c)
}

func earthPointNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, moved bool) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, "SELECT id,a FROM earth_point_input ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil || len(meta) != 2 || meta[1].DatabaseTypeName() != "POINT" {
		t.Fatalf("retained metadata %v %v", meta, err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	earthPointCurrentVerify(t, p, b, c, moved)
	pointConsumeRetained(t, old, [][]any{{int64(1), "(0,60)"}, {int64(2), "(0,0)"}, {int64(3), nil}})
	// A separate Driver plus a consumed allocator identity forces a real remap.
	target := fixture.NewFixture(t, false, true)
	if moved {
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE earth_point_owner", 0))
	}
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE earth_point_allocator(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	earthPointCurrentVerify(t, target, tb, target.Memory, moved)
	bad := dump.String() + "\n" + earthPointInvalidWrite + ";\n"
	want := earthPointCheckError()
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	earthPointCurrentVerify(t, target, tb, target.Memory, moved)
	var after bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("failed late restore changed deterministic dump")
	}
	if moved && p.Postgres != nil {
		for _, q := range []string{
			"DROP EXTENSION IF EXISTS earthdistance CASCADE; DROP EXTENSION IF EXISTS cube CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public; DROP SCHEMA IF EXISTS earth_point_moved CASCADE",
			"CREATE ROLE earth_point_owner",
			dump.String(),
		} {
			if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
				t.Fatal("actual emitted point SQL replay", err)
			}
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		earthPointCurrentVerify(t, p, pg, p.Postgres, true)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.ErrorDiagnostic(earthPointInvalidWrite, earthPointCheckError()))
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP EXTENSION earthdistance CASCADE; DROP EXTENSION cube CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public; DROP SCHEMA earth_point_moved CASCADE; DROP ROLE earth_point_owner"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceEarthPointControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"earth_point_owner"})
	fixture.SchemaReferenceCleanup(t, "earth_point_moved")
	earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		earthPointStoredSetup(t, p, b, c)
		earthPointCurrentVerify(t, p, b, c, false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(earthPointInvalidWrite, earthPointCheckError()))
		earthPointStoredVerify(t, p, b, c)
		var before int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'geo_distance(point,point)'::regprocedure::oid::bigint").Scan(&before); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"CREATE ROLE earth_point_owner", "CREATE SCHEMA earth_point_moved", "ALTER FUNCTION geo_distance(point,point) RENAME TO miles_now", "ALTER FUNCTION miles_now(point,point) SET SCHEMA earth_point_moved", "ALTER FUNCTION earth_point_moved.miles_now(point,point) OWNER TO earth_point_owner"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT oid::bigint AS identity FROM pg_proc WHERE oid='earth_point_moved.miles_now(point,point)'::regprocedure", []string{"identity"}, []string{"INT8"}, [][]any{{before}}))
		earthPointCurrentVerify(t, p, b, c, true)
		for _, route := range []string{"direct", "fresh", "held"} {
			earthCoordinateError(t, p, b, c, "SELECT geo_distance(point '(0,60)',point '(1,60)')", route, nil, earthCoordinateMissing("geo_distance(point, point)", "8"), true, nil)
		}
		if !b.PG {
			earthPointNativeControls(t, p, b, c, true)
		}
	})
}

func TestReferenceEarthPointFeatureSmoke(t *testing.T) {
	earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		earthPointMiles(t, p, b, c, "point '(0,60)'", "point '(1,60)'", "34.54626", 0)
		earthPointMiles(t, p, b, c, "point '(0,0)'", "point '(1,0)'", "69.09318", 0)
		earthPointMiles(t, p, b, c, "NULL::point", "point '(1,0)'", nil, 0)
		earthPointHeldRecovery(t, p, b, c)
		earthPointStoredSetup(t, p, b, c)
		earthPointCurrentVerify(t, p, b, c, false)
		if !b.PG {
			earthPointNativeControls(t, p, b, c, false)
		}
	})
}
