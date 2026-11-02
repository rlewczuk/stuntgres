package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func dependencyMissing() fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("42704", `required extension "cube" is not installed`)
	want.Hint = "Use CREATE EXTENSION ... CASCADE to install required extensions too."
	return want
}

func dependencyDenied() fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("42501", `permission denied to create extension "earthdistance"`)
	want.Hint = "Must be superuser to create this extension."
	return want
}

func dependencyCollision(cubeSchema string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("42710", `type "earth" already exists`)
	want.Context = strings.ReplaceAll("SQL statement \"CREATE DOMAIN earth AS public.cube\n  CONSTRAINT not_point CHECK(public.cube_is_point(VALUE))\n  CONSTRAINT not_3d CHECK(public.cube_dim(VALUE) <= 3)\n  CONSTRAINT on_surface CHECK(abs(public.cube_distance(VALUE, '(0)'::public.cube) /\n  earth() - '1'::float8) < '10e-7'::float8)\"\nextension script file \"earthdistance--1.1.sql\", near line 30", "public.cube", cubeSchema+".cube")
	return want
}

func dependencyError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, prepared bool, want fixture.Diagnostic) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want)
	s.Prepared = prepared
	// Utility analysis may be deferred until execution. Assert the full record
	// without guessing whether a new error is raised by Prepare or Execute.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func dependencyVersions(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT extname,extversion FROM pg_extension WHERE extname IN ('cube','earthdistance') ORDER BY extname", []string{"extname", "extversion"}, []string{"NAME", "TEXT"}, [][]any{{"cube", "1.5"}, {"earthdistance", "1.2"}}))
}

func dependencyGraph(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	dependencyVersions(t, p, b, db, recorder)
	earthIdentityChecks(t, p, b, db, recorder)
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT e.oid>0 AND c.oid>0 AND e.oid<>c.oid AS identities,d.deptype FROM pg_depend d JOIN pg_extension e ON d.objid=e.oid JOIN pg_extension c ON d.refobjid=c.oid WHERE d.classid='pg_extension'::regclass AND d.refclassid='pg_extension'::regclass AND e.extname='earthdistance' AND c.extname='cube'", []string{"identities", "deptype"}, []string{"BOOL", "CHAR"}, [][]any{{true, "n"}}))
}

func dependencyAbsent(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Select("SELECT extname FROM pg_extension WHERE extname IN ('cube','earthdistance') ORDER BY extname", []string{"extname"}, []string{"NAME"}, [][]any{}))
	run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('cube','_cube') ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{}))
	run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('cube_in','cube_out','cube_dim','cube_distance','cube_is_point','earth','ll_to_earth','latitude','longitude','gc_to_sec','sec_to_gc','geo_distance','earth_distance','earth_box') ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{}))
	run(fixture.Select("SELECT conname FROM pg_constraint WHERE connamespace='public'::regnamespace AND conname IN ('not_3d','not_point','on_surface') ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{}))
}

func dependencyQualifiedGraph(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder, cubeSchema, earthSchema string) {
	t.Helper()
	dependencyVersions(t, p, b, db, recorder)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, recorder, s) }
	run(fixture.Select("SELECT e.oid>0 AND c.oid>0 AND e.oid<>c.oid AS identities,e.typtype,e.typbasetype=c.oid AS base,e.typoutput=c.typoutput AS output FROM pg_type e JOIN pg_type c ON e.typbasetype=c.oid WHERE e.oid='"+earthSchema+".earth'::regtype AND c.oid='"+cubeSchema+".cube'::regtype", []string{"identities", "typtype", "base", "output"}, []string{"BOOL", "CHAR", "BOOL", "BOOL"}, [][]any{{true, "d", true, true}}))
	run(fixture.Select("SELECT conname,convalidated FROM pg_constraint WHERE contypid='"+earthSchema+".earth'::regtype ORDER BY conname", []string{"conname", "convalidated"}, []string{"NAME", "BOOL"}, [][]any{{"not_3d", true}, {"not_point", true}, {"on_surface", true}}))
	run(fixture.Select("SELECT d.deptype FROM pg_depend d JOIN pg_extension e ON d.objid=e.oid JOIN pg_extension c ON d.refobjid=c.oid WHERE d.classid='pg_extension'::regclass AND d.refclassid='pg_extension'::regclass AND e.extname='earthdistance' AND c.extname='cube'", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}))
}

func dependencyNoEarthExtension(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='earthdistance'", []string{"extname"}, []string{"NAME"}, [][]any{}))
}

// Snapshot only within one backend: PostgreSQL's full cube manifest is larger
// than the native logical provider's, and allocator numbers are not goldens.
var dependencyCubeSnapshots = []string{
	"SELECT oid,extnamespace,extowner,extversion,extrelocatable FROM pg_extension WHERE extname='cube'",
	"SELECT oid,typname,typnamespace,typowner,typacl::text,typinput,typoutput,typarray,typelem FROM pg_type WHERE oid IN ('cube'::regtype,'cube[]'::regtype) ORDER BY oid",
	"SELECT d.classid,d.objid,d.objsubid,d.deptype FROM pg_depend d JOIN pg_extension e ON d.refobjid=e.oid WHERE d.refclassid='pg_extension'::regclass AND e.extname='cube' AND d.deptype='e' ORDER BY d.classid,d.objid,d.objsubid,d.deptype",
	"SELECT p.oid,p.proname,p.pronamespace,p.proowner,p.proacl::text,p.prosrc,p.probin FROM pg_proc p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='cube') AND d.deptype='e' ORDER BY p.oid",
	"SELECT p.oid,p.oprname,p.oprnamespace,p.oprowner,p.oprleft,p.oprright,p.oprcode FROM pg_operator p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_operator'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='cube') AND d.deptype='e' ORDER BY p.oid",
	"SELECT p.oid,p.opcname,p.opcnamespace,p.opcowner,p.opcintype,p.opcfamily FROM pg_opclass p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_opclass'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='cube') AND d.deptype='e' ORDER BY p.oid",
	"SELECT p.oid,p.opfname,p.opfnamespace,p.opfowner FROM pg_opfamily p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_opfamily'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='cube') AND d.deptype='e' ORDER BY p.oid",
}

func dependencyCaptureCube(t *testing.T, p *fixture.Pair, c *sql.Conn) [][][]any {
	t.Helper()
	out := make([][][]any, len(dependencyCubeSnapshots))
	for i, q := range dependencyCubeSnapshots {
		out[i] = fixture.OwnershipSnapshot(t, p, c, q)
		if len(out[i]) == 0 {
			t.Fatal("missing genuine cube snapshot", q)
		}
	}
	return out
}

func dependencyUnchangedCube(t *testing.T, p *fixture.Pair, c *sql.Conn, before [][][]any) {
	t.Helper()
	for i, q := range dependencyCubeSnapshots {
		fixture.OwnershipUnchanged(t, p, c, q, before[i])
	}
}
