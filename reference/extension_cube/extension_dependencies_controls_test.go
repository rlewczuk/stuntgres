package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var dependencyControlState = []struct {
	query          string
	columns, types []string
}{
	{"SELECT extname,pg_get_userbyid(extowner) AS owner,extversion FROM pg_extension WHERE extname IN ('cube','earthdistance') ORDER BY extname", []string{"extname", "owner", "extversion"}, []string{"NAME", "NAME", "TEXT"}},
	{"SELECT typname,pg_get_userbyid(typowner) AS owner,typacl::text AS acl FROM pg_type WHERE oid IN ('cube'::regtype,'earth'::regtype) ORDER BY typname", []string{"typname", "owner", "acl"}, []string{"NAME", "NAME", "TEXT"}},
	{"SELECT proname,pg_get_userbyid(proowner) AS owner,proacl::text AS acl FROM pg_proc WHERE oid IN ('cube_dim(cube)'::regprocedure,'earth()'::regprocedure) ORDER BY proname", []string{"proname", "owner", "acl"}, []string{"NAME", "NAME", "TEXT"}},
	{"SELECT e.extname,d.classid::bigint AS class,pg_describe_object(d.classid,d.objid,d.objsubid) AS identity FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname IN ('cube','earthdistance') AND d.deptype='e' ORDER BY e.extname,d.classid,identity", []string{"extname", "class", "identity"}, []string{"NAME", "INT8", "TEXT"}},
	{"SELECT pg_describe_object(d.classid,d.objid,0) AS identity,pg_get_userbyid(t.typowner) AS owner,t.typacl::text AS acl FROM pg_depend d JOIN pg_type t ON t.oid=d.objid WHERE d.classid='pg_type'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid IN (SELECT oid FROM pg_extension WHERE extname IN ('cube','earthdistance')) AND d.deptype='e' ORDER BY identity", []string{"identity", "owner", "acl"}, []string{"TEXT", "NAME", "TEXT"}},
	{"SELECT pg_describe_object(d.classid,d.objid,0) AS identity,pg_get_userbyid(p.proowner) AS owner,p.proacl::text AS acl FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid IN (SELECT oid FROM pg_extension WHERE extname IN ('cube','earthdistance')) AND d.deptype='e' ORDER BY identity", []string{"identity", "owner", "acl"}, []string{"TEXT", "NAME", "TEXT"}},
	{"SELECT pg_describe_object(d.classid,d.objid,0) AS identity,pg_get_userbyid(p.oprowner) AS owner FROM pg_depend d JOIN pg_operator p ON p.oid=d.objid WHERE d.classid='pg_operator'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid IN (SELECT oid FROM pg_extension WHERE extname IN ('cube','earthdistance')) AND d.deptype='e' ORDER BY identity", []string{"identity", "owner"}, []string{"TEXT", "NAME"}},
	{"SELECT pg_describe_object(d.classid,d.objid,0) AS identity,pg_get_userbyid(p.opcowner) AS owner FROM pg_depend d JOIN pg_opclass p ON p.oid=d.objid WHERE d.classid='pg_opclass'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid IN (SELECT oid FROM pg_extension WHERE extname IN ('cube','earthdistance')) AND d.deptype='e' ORDER BY identity", []string{"identity", "owner"}, []string{"TEXT", "NAME"}},
	{"SELECT pg_describe_object(d.classid,d.objid,0) AS identity,pg_get_userbyid(p.opfowner) AS owner FROM pg_depend d JOIN pg_opfamily p ON p.oid=d.objid WHERE d.classid='pg_opfamily'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid IN (SELECT oid FROM pg_extension WHERE extname IN ('cube','earthdistance')) AND d.deptype='e' ORDER BY identity", []string{"identity", "owner"}, []string{"TEXT", "NAME"}},
}

func dependencyStoredVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	dependencyGraph(t, p, b, db, recorder)
	for _, s := range []fixture.Step{
		fixture.Select("SELECT dimensions,radius,latitude FROM deps_control_calls", []string{"dimensions", "radius", "latitude"}, []string{"INT4", "FLOAT8", "FLOAT8"}, [][]any{{int64(2), float64(6378168), float64(0)}}),
		fixture.Select("SELECT p.proname,d.deptype FROM pg_depend d JOIN pg_proc p ON d.refobjid=p.oid WHERE d.classid='pg_constraint'::regclass AND d.refclassid='pg_proc'::regclass AND d.objid=(SELECT oid FROM pg_constraint WHERE contypid='earth'::regtype AND conname='not_point') ORDER BY p.proname", []string{"proname", "deptype"}, []string{"NAME", "CHAR"}, [][]any{{"cube_is_point", "n"}}),
		fixture.Select("SELECT t.typowner=e.extowner AS owner,t.typacl IS NOT NULL AS explicit_acl FROM pg_type t JOIN pg_extension e ON e.extname='earthdistance' WHERE t.oid='earth'::regtype", []string{"owner", "explicit_acl"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
	} {
		fixture.RunStep(t, p.Ctx, db, recorder, s)
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
	}
}

func TestReferenceExtensionDependenciesControls(t *testing.T) {
	earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// The only setup installation is the dependent extension's CASCADE.
		run(fixture.Exec("CREATE EXTENSION earthdistance VERSION '1.2' CASCADE", 0))
		for _, q := range []string{
			"CREATE TABLE earth_controls(id integer NOT NULL,v earth)", "CREATE TABLE earth_vacant(v earth NOT NULL)",
			"CREATE VIEW deps_control_calls AS SELECT cube_dim('(1,2)'::cube) AS dimensions,earth() AS radius,latitude(ll_to_earth(0,0)) AS latitude",
			"REVOKE USAGE ON TYPE earth FROM PUBLIC", "REVOKE EXECUTE ON FUNCTION cube_dim(cube) FROM PUBLIC",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(earthControlInsert, 7))
		earthControlVerify(t, p, b, c, b.Recorder)
		dependencyStoredVerify(t, p, b, c, b.Recorder)
		held := earthHeld(t, p, c, "SELECT id,v FROM earth_controls ORDER BY id")
		defer held.Statement.Close()
		s := fixture.EarthSelect(b, held.SQL, []string{"id", "v"}, earthControlRows(false))
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, s.Columns, []bool{false, true}) }
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		nullOnly := fixture.EarthSelect(b, "SELECT v FROM earth_controls WHERE id=6", []string{"v"}, [][]any{{nil}})
		nullOnly.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, []string{"v"}, []bool{true}) }
		run(nullOnly)
		run(fixture.Prepared(nullOnly))
		if !b.PG {
			// Logical owner/ACL snapshots have independent shape/capability checks
			// above. OIDs are compared only within one restored destination.
			logical := make([][][]any, len(dependencyControlState))
			for i, tc := range dependencyControlState {
				logical[i] = fixture.OwnershipSnapshot(t, p, c, tc.query)
			}
			identities := make(map[*fixture.Pair][][][]any)
			queries := []string{
				"SELECT oid,extname,extnamespace,extowner,extversion FROM pg_extension WHERE extname IN ('cube','earthdistance') ORDER BY extname",
				"SELECT oid,typname,typbasetype,typarray,typowner,typacl::text FROM pg_type WHERE oid IN ('cube'::regtype,'earth'::regtype) ORDER BY typname",
				"SELECT oid,conname,contypid,conbin FROM pg_constraint WHERE contypid='earth'::regtype ORDER BY conname",
				"SELECT d.classid,d.objid,d.objsubid,d.refobjid,d.deptype FROM pg_depend d JOIN pg_extension e ON d.refobjid=e.oid WHERE d.refclassid='pg_extension'::regclass AND e.extname IN ('cube','earthdistance') ORDER BY d.classid,d.objid,d.objsubid,d.refobjid,d.deptype",
				"SELECT current_setting('search_path'),current_setting('extra_float_digits')",
			}
			verify := func(t *testing.T, target *fixture.Pair, backend fixture.Backend, db fixture.Executor, recorder *fixture.Recorder) {
				dependencyStoredVerify(t, target, backend, db, recorder)
				if backend.PG {
					return
				}
				for i, tc := range dependencyControlState {
					fixture.RunStep(t, target.Ctx, db, recorder, fixture.Select(tc.query, tc.columns, tc.types, logical[i]))
				}
				conn, ok := db.(*sql.Conn)
				if !ok {
					t.Fatalf("native controls require a pinned session, got %T", db)
				}
				if before, ok := identities[target]; ok {
					for i, q := range queries {
						fixture.OwnershipUnchanged(t, target, conn, q, before[i])
					}
				} else {
					before := make([][][]any, len(queries))
					for i, q := range queries {
						before[i] = fixture.OwnershipSnapshot(t, target, conn, q)
					}
					identities[target] = before
				}
			}
			// Prepared handles belong to one physical generation, unlike the
			// detached Rows and copied metadata retained by earthNativeControls.
			if err := held.Statement.Close(); err != nil {
				t.Fatal(err)
			}
			// Reuse all existing exact-data, detached-reader, same/fresh-driver,
			// failed-restore, deterministic-redump and actual PG SQL replay checks.
			earthNativeControls(t, p, b, c, verify)
			restored := earthHeld(t, p, c, held.SQL)
			defer restored.Statement.Close()
			fixture.RunStep(t, p.Ctx, restored, b.Recorder, s)
		}
	})
}
