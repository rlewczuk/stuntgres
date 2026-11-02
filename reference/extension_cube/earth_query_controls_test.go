package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func earthQueryStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE earth_query_input(id integer,p earth,q earth,r float8)",
		"CREATE VIEW earth_query_view AS SELECT id,earth_distance(p,q) AS d,earth_box(p,r) AS b FROM earth_query_input",
		"CREATE TABLE earth_query_defaults(id integer,d float8 DEFAULT earth_distance('(6378168)'::earth,'(6378168)'::earth),b cube DEFAULT earth_box('(6378168)'::earth,0))",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_query_input VALUES (1,'(6378168)','(6378168,0,0)',0),(2,'(6378168)','(-6378168)','Infinity'::float8),(3,NULL,NULL,NULL)", 3))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_query_defaults(id) VALUES (1)", 1))
}

func earthQueryStoredVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	earthCoordinateNumbers(t, p, b, c, "SELECT id,d FROM earth_query_view ORDER BY id", []string{"id", "d"}, [][]any{{int64(1), float64(0)}, {int64(2), "20037605.73216"}, {int64(3), nil}}, []float64{0, 0})
	earthCoordinateBoth(t, p, b, c, cubeConstructorSelect(b, "SELECT id,b FROM earth_query_view ORDER BY id", []string{"id", "b"}, []string{"INT4", "CUBE"}, [][]any{{int64(1), "(6378168)"}, {int64(2), "(-6378168, -12756336, -12756336),(19134504, 12756336, 12756336)"}, {int64(3), nil}}))
	earthCoordinateBoth(t, p, b, c, cubeConstructorSelect(b, "SELECT id,d,b FROM earth_query_defaults ORDER BY id", []string{"id", "d", "b"}, []string{"INT4", "FLOAT8", "CUBE"}, [][]any{{int64(1), float64(0), "(6378168)"}}))
}

func earthQueryNewDefault(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_query_defaults(id) VALUES (2)", 1))
	earthCoordinateBoth(t, p, b, c, cubeConstructorSelect(b, "SELECT d,b FROM earth_query_defaults WHERE id=2", []string{"d", "b"}, []string{"FLOAT8", "CUBE"}, [][]any{{float64(0), "(6378168)"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM earth_query_defaults WHERE id=2", 1))
}

func earthQueryCorruptOwners(t *testing.T, script string) string {
	t.Helper()
	start, end, data := fixture.CatalogExtensionOwnerDirective(t, script, "earthdistance", true)
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil {
		t.Fatal(err)
	}
	if len(members) != 12 {
		t.Fatal("emitted owner manifest lost current roles", len(members))
	}
	found := false
	for _, member := range members {
		var name string
		if err := json.Unmarshal(member["name"], &name); err != nil {
			t.Fatal(err)
		}
		if name == "earth_box" {
			member["signature"] = json.RawMessage(`"public.earth, integer"`)
			found = true
		}
	}
	if !found {
		t.Fatal("actual emitted query owner identity missing")
	}
	var err error
	record["members"], err = json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return script[:start] + "-- stuntgres-extension-owners-v3 " + base64.StdEncoding.EncodeToString(data) + script[end:]
}

func earthQueryNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, verify func(*testing.T, *fixture.Pair, fixture.Backend, fixture.Executor)) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, "SELECT id,b FROM earth_query_defaults ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata := cubeMetadata(t, false, old, []string{"id", "b"}, []bool{true, true})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("same-driver query Restore", err)
	}
	earthQueryStoredVerify(t, p, b, c)
	earthQueryNewDefault(t, p, b, c)
	if verify != nil {
		verify(t, p, b, c)
	}
	cubeConsumeRetained(t, old, [][]any{{int64(1), "(6378168)"}})
	cubeColumnMetadata(t, false, metadata, []string{"id", "b"}, []bool{true, true})

	target := fixture.NewFixture(t, false, true)
	if verify != nil {
		for _, q := range []string{"CREATE ROLE earth_query_control_distance_owner", "CREATE ROLE earth_query_control_box_owner"} {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, 0))
		}
	}
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE earth_query_allocator(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("independent-driver query Restore", err)
	}
	tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	earthQueryStoredVerify(t, target, tb, target.Memory)
	earthQueryNewDefault(t, target, tb, target.Memory)
	if verify != nil {
		verify(t, target, tb, target.Memory)
	}
	retained, err := target.Memory.QueryContext(target.Ctx, "SELECT id,b FROM earth_query_defaults ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	badOwner := earthQueryCorruptOwners(t, dump.String())
	failure := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(badOwner))
	var state interface{ SQLState() string }
	if failure == nil || errors.As(failure, &state) {
		t.Fatal("corrupt owner identity must fail ordinarily", failure)
	}
	if verify != nil {
		// Capture a genuinely changed stored default through ordinary DDL and
		// Dump. Restore must replay its emitted CREATE/default definition, not
		// admit an unrelated appended ALTER TABLE SET DEFAULT.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE earth_query_defaults ALTER COLUMN b SET DEFAULT earth_query_control_moved.earth_box('(6378168)'::earth,'5e-324'::float8)", 0))
		var badDump bytes.Buffer
		dumpErr := stuntgres.Dump(p.Ctx, p.Native, &badDump)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE earth_query_defaults ALTER COLUMN b SET DEFAULT earth_query_control_moved.earth_box('(6378168)'::earth,0)", 0))
		if dumpErr != nil {
			t.Fatal("capturing failing query default", dumpErr)
		}
		if bytes.Equal(dump.Bytes(), badDump.Bytes()) {
			t.Fatal("captured query radius did not change emitted SQL")
		}
		badCall := badDump.String() + "\nINSERT INTO earth_query_defaults(id) VALUES (99);\n"
		want := earthQueryUnderflow("gc_to_sec", "earth_box", "during inlining")
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(badCall)), &want)
	}
	bad := dump.String() + "\nINSERT INTO earth_query_defaults(id,b) VALUES (99,'(0)'::earth);\n"
	want := earthCheckDiagnostic("public", "on_surface", true)
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	earthQueryStoredVerify(t, target, tb, target.Memory)
	earthQueryNewDefault(t, target, tb, target.Memory)
	cubeConsumeRetained(t, retained, [][]any{{int64(1), "(6378168)"}})
	var after bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("failed query replay changed target catalog/data/defaults", after.String())
	}
	if p.Postgres != nil {
		reset := "DROP EXTENSION IF EXISTS earthdistance CASCADE; DROP EXTENSION IF EXISTS cube CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public; DROP SCHEMA IF EXISTS earth_query_control_moved CASCADE"
		if _, err := p.Postgres.ExecContext(p.Ctx, reset); err != nil {
			t.Fatal(err)
		}
		if verify != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "CREATE ROLE earth_query_control_distance_owner; CREATE ROLE earth_query_control_box_owner"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted query SQL replay", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		earthQueryStoredVerify(t, p, pg, p.Postgres)
		earthQueryNewDefault(t, p, pg, p.Postgres)
		if verify != nil {
			verify(t, p, pg, p.Postgres)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, reset); err != nil {
			t.Fatal(err)
		}
		if verify != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP ROLE earth_query_control_distance_owner; DROP ROLE earth_query_control_box_owner"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func earthQueryControlIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, tc := range []struct{ signature, owner string }{
		{"public.distance_now(earth,earth)", "earth_query_control_distance_owner"},
		{"earth_query_control_moved.earth_box(earth,float8)", "earth_query_control_box_owner"},
	} {
		earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT proowner=(SELECT oid FROM pg_roles WHERE rolname="+cubeLiteral(tc.owner)+") AS owner FROM pg_proc WHERE oid="+cubeLiteral(tc.signature)+"::regprocedure", []string{"owner"}, []string{"BOOL"}, [][]any{{true}}))
	}
	earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT proacl IS NOT NULL AS explicit_acl,has_function_privilege('earth_query_control_distance_owner','earth_query_control_moved.earth_box(earth,float8)','EXECUTE') AS delegated FROM pg_proc WHERE oid='earth_query_control_moved.earth_box(earth,float8)'::regprocedure", []string{"explicit_acl", "delegated"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	earthCoordinateBoth(t, p, b, c, cubeConstructorSelect(b, "SELECT public.distance_now('(6378168)'::earth,'(6378168)'::earth) AS d,earth_query_control_moved.earth_box('(6378168)'::earth,0) AS b", []string{"d", "b"}, []string{"FLOAT8", "CUBE"}, [][]any{{float64(0), "(6378168)"}}))
}

func TestReferenceEarthQueryControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"earth_query_control_distance_owner", "earth_query_control_box_owner"})
	fixture.SchemaReferenceCleanup(t, "earth_query_control_moved")
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		earthQueryStoredSetup(t, p, b, c)
		earthQueryStoredVerify(t, p, b, c)
		for _, q := range []string{
			"CREATE ROLE earth_query_control_distance_owner", "CREATE ROLE earth_query_control_box_owner", "CREATE SCHEMA earth_query_control_moved",
			"GRANT USAGE,CREATE ON SCHEMA public TO earth_query_control_distance_owner",
			"GRANT USAGE,CREATE ON SCHEMA earth_query_control_moved TO earth_query_control_box_owner",
			"ALTER FUNCTION earth_distance(earth,earth) RENAME TO distance_now",
			"ALTER FUNCTION earth_box(earth,float8) SET SCHEMA earth_query_control_moved",
			"ALTER FUNCTION distance_now(earth,earth) OWNER TO earth_query_control_distance_owner",
			"ALTER FUNCTION earth_query_control_moved.earth_box(earth,float8) OWNER TO earth_query_control_box_owner",
			"REVOKE EXECUTE ON FUNCTION earth_query_control_moved.earth_box(earth,float8) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION earth_query_control_moved.earth_box(earth,float8) TO earth_query_control_distance_owner",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		earthQueryStoredVerify(t, p, b, c)
		earthQueryNewDefault(t, p, b, c)
		earthQueryControlIdentity(t, p, b, c)
		if !b.PG {
			earthQueryNativeControls(t, p, b, c, earthQueryControlIdentity)
		}
	})
}
