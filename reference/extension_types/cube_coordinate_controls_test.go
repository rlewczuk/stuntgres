package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const cubeCoordinateDefaultDDL = "CREATE TABLE cube_coordinate_defaults(id integer,v float8 DEFAULT cube_ll_coord('(3,1),(1,4)'::cube,1))"
const cubeCoordinateIdentities = "SELECT 'public.cube'::regtype::oid::bigint,'public.cube_coord(public.cube,integer)'::regprocedure::oid::bigint,'public.->(public.cube,integer)'::regoperator::oid::bigint"

func cubeCoordinateStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owned bool) {
	t.Helper()
	cubeCoordinateSeed(t, p, b, c)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE VIEW cube_coordinate_view AS SELECT id,"+cubeCoordinateProjection("v", "1")+" FROM cube_coordinate_data", 0))
	run(fixture.Exec(cubeCoordinateDefaultDDL, 0))
	run(fixture.Exec("INSERT INTO cube_coordinate_defaults(id) VALUES (1)", 1))
	if owned {
		run(fixture.Exec("CREATE ROLE cube_coordinate_owner", 0))
		if b.PG {
			cubeCoordinatePGOwners(t, p, c, b.Recorder)
		} else {
			// Reuse the public named-owner Restore seam: routine ALTER is not
			// required to exercise independent installed-member ownership.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(cubeCoordinateOwnerScript(t, dump.String()))); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func cubeCoordinatePGOwners(t *testing.T, p *fixture.Pair, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	for _, name := range []string{"cube_ll_coord", "cube_ur_coord", "cube_coord", "cube_coord_llur"} {
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("ALTER FUNCTION public."+name+"(public.cube,integer) OWNER TO cube_coordinate_owner", 0))
	}
	for _, op := range []string{"->", "~>"} {
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("ALTER OPERATOR public."+op+"(public.cube,integer) OWNER TO cube_coordinate_owner", 0))
	}
}

func cubeCoordinateOwnerScript(t *testing.T, script string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v3 "
	start, end, data := fixture.CatalogExtensionOwnerDirective(t, script, "cube", true)
	var err error
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	var name, schema string
	if err := json.Unmarshal(record["name"], &name); err != nil || name != "cube" {
		t.Fatal("cube owner manifest identity", name, err)
	}
	if err := json.Unmarshal(record["schema"], &schema); err != nil || schema != "public" {
		t.Fatal("cube owner manifest namespace", schema, err)
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil {
		t.Fatal("decode cube owner members", err)
	}
	want := map[[3]string]bool{
		{"procedure", "cube_ll_coord", "public.cube, integer"}:   false,
		{"procedure", "cube_ur_coord", "public.cube, integer"}:   false,
		{"procedure", "cube_coord", "public.cube, integer"}:      false,
		{"procedure", "cube_coord_llur", "public.cube, integer"}: false,
		{"operator", "->", "public.cube, integer"}:               false,
		{"operator", "~>", "public.cube, integer"}:               false,
	}
	for _, member := range members {
		var key [3]string
		for i, field := range []string{"kind", "name", "signature"} {
			if err := json.Unmarshal(member[field], &key[i]); err != nil {
				t.Fatal("cube member identity", field, err)
			}
		}
		if seen, selected := want[key]; selected {
			if seen {
				t.Fatal("duplicate coordinate member owner", key)
			}
			member["owner"] = json.RawMessage(`"cube_coordinate_owner"`)
			want[key] = true
		}
	}
	for key, seen := range want {
		if !seen {
			t.Fatal("missing coordinate member owner", key)
		}
	}
	record["members"], err = json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return script[:start] + marker + base64.StdEncoding.EncodeToString(data) + script[end:]
}

func cubeCoordinateVerifyStored(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder, owned bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, recorder, s) }
	for _, s := range []fixture.Step{cubeCoordinateTable(b, false), cubeCoordinateTable(b, true)} {
		run(s)
		run(fixture.Prepared(s))
	}
	run(fixture.Select("SELECT id,v FROM cube_coordinate_defaults ORDER BY id", []string{"id", "v"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(1)}}))
	run(fixture.Exec("INSERT INTO cube_coordinate_defaults(id) VALUES (2)", 1))
	run(fixture.Select("SELECT v FROM cube_coordinate_defaults WHERE id=2", []string{"v"}, []string{"FLOAT8"}, [][]any{{float64(1)}}))
	run(fixture.Exec("DELETE FROM cube_coordinate_defaults WHERE id=2", 1))
	owner := ""
	if owned {
		owner = "cube_coordinate_owner"
	}
	for _, s := range cubeCoordinateCatalogChecks("public", owner) {
		run(s)
	}
}

func cubeCoordinatePending(t *testing.T, p *fixture.Pair, c fixture.Executor) (*sql.Rows, []*sql.ColumnType, []any) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, cubeCoordinateTable(fixture.Backend{}, false).Query)
	if err != nil {
		t.Fatal(err)
	}
	cols, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		t.Fatal(err)
	}
	return rows, cols, cubeSpatialMetadataSnapshot(t, cols)
}

func cubeCoordinateDrain(t *testing.T, rows *sql.Rows) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		v := make([]any, 5)
		dest := make([]any, 5)
		for i := range v {
			dest[i] = &v[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(1), float64(1), float64(3), float64(3), float64(1)}, {int64(2), float64(42), float64(42), float64(42), float64(42)}, {int64(3), nil, nil, nil, nil}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained coordinates %#v want %#v", got, want)
	}
}

func cubeCoordinateControlCycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owned bool) {
	t.Helper()
	cubeCoordinateVerifyStored(t, p, b, c, b.Recorder, owned)
	if b.PG {
		// SQL recreation is the shared PG counterpart, not a pretend control API.
		for _, q := range []string{"DROP VIEW cube_coordinate_view", "DROP TABLE cube_coordinate_defaults", "DROP TABLE cube_coordinate_data", "DROP EXTENSION cube"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		if owned {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP ROLE cube_coordinate_owner", 0))
		}
		cubeCoordinateStoredSetup(t, p, b, c, owned)
		cubeCoordinateVerifyStored(t, p, b, c, b.Recorder, owned)
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	ext, consumer := strings.Index(text, "CREATE EXTENSION"), strings.Index(text, "CREATE TABLE")
	if ext < 0 || consumer < 0 || ext >= consumer || strings.Contains(text, "CREATE FUNCTION") || strings.Contains(text, "CREATE OPERATOR") {
		t.Fatal("invalid extension/consumer dump ordering", text)
	}
	rows, cols, metadata := cubeCoordinatePending(t, p, c)
	defer rows.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	cubeCoordinateVerifyStored(t, p, b, c, b.Recorder, owned)
	cubeCoordinateDrain(t, rows)
	if !reflect.DeepEqual(cubeSpatialMetadataSnapshot(t, cols), metadata) {
		t.Fatal("replacement mutated captured metadata")
	}
	var source [3]int64
	if err := c.QueryRowContext(p.Ctx, cubeCoordinateIdentities).Scan(&source[0], &source[1], &source[2]); err != nil {
		t.Fatal(err)
	}
	fresh := fixture.NewFixture(t, false, true)
	owner, err := fresh.Memory.Conn(fresh.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	// Reuse the established actual retained-TEMP collision campaign rather than
	// a disposable permanent table that Restore would simply replace.
	cubeSpatialRetainCollisions(t, fresh, owner, source)
	if owned {
		fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Exec("CREATE ROLE cube_coordinate_owner", 0))
	}
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var destination [3]int64
	if err := fresh.Memory.QueryRowContext(fresh.Ctx, cubeCoordinateIdentities).Scan(&destination[0], &destination[1], &destination[2]); err != nil {
		t.Fatal(err)
	}
	for i, id := range destination {
		if id <= 0 || id == source[i] {
			t.Fatalf("coordinate identity not reminted: %v -> %v", source, destination)
		}
	}
	cubeCoordinateVerifyStored(t, fresh, b, fresh.Memory, fresh.MemoryRecord, owned)
	pending, oldCols, oldMetadata := cubeCoordinatePending(t, fresh, fresh.Memory)
	defer pending.Close()
	bad := "CREATE EXTENSION cube; CREATE TABLE staged(v float8 DEFAULT cube_ll_coord('(1)'::cube,1)); INSERT INTO staged VALUES (1::float8); INSERT INTO staged VALUES (cube_ll_coord('(1)'::cube,1));"
	err = stuntgres.Restore(fresh.Ctx, fresh.Native, strings.NewReader(bad))
	if err == nil || err.Error() != "stuntgres: invalid restore scalar" {
		t.Fatal("Restore producer boundary", err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("ordinary Restore rejection acquired SQLSTATE", err)
	}
	cubeCoordinateVerifyStored(t, fresh, b, fresh.Memory, fresh.MemoryRecord, owned)
	cubeCoordinateDrain(t, pending)
	if !reflect.DeepEqual(cubeSpatialMetadataSnapshot(t, oldCols), oldMetadata) {
		t.Fatal("failed Restore mutated metadata")
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("coordinate dump changed after remint", again.String())
	}
	if p.Postgres != nil {
		replay, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		ownerCreated := false
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			queries := []string{"RESET SESSION AUTHORIZATION", "RESET ROLE", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"}
			if ownerCreated {
				queries = append(queries, "DROP ROLE cube_coordinate_owner")
			}
			for _, query := range queries {
				if _, err := replay.ExecContext(ctx, query); err != nil {
					t.Error("coordinate replay cleanup", query, err)
				}
			}
			if err := replay.Close(); err != nil {
				t.Error(err)
			}
		}()
		if _, err := replay.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if owned {
			fixture.RunStep(t, p.Ctx, replay, p.PostgresRecord, fixture.Exec("CREATE ROLE cube_coordinate_owner", 0))
			ownerCreated = true
		}
		if _, err := replay.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual native coordinate dump PG replay", err)
		}
		if owned {
			// PostgreSQL ignores native ownership comments. Transfer the same
			// independently selected members through its ordinary SQL seam.
			cubeCoordinatePGOwners(t, p, replay, p.PostgresRecord)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		cubeCoordinateVerifyStored(t, p, pg, replay, pg.Recorder, owned)
	}
}

func TestReferenceCubeCoordinateControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cube_coordinate_owner"})
	cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeCoordinateStoredSetup(t, p, b, c, true)
		cubeCoordinateControlCycle(t, p, b, c, true)
	})
}
