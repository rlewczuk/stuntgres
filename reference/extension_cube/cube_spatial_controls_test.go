package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const cubeSpatialDefaultDDL = "CREATE TABLE cube_spatial_defaults(id integer,overlap boolean DEFAULT ('(3,4)'::cube && '()'::cube),contains boolean DEFAULT ('(3,4)'::cube @> '()'::cube),contained boolean DEFAULT ('(3,4)'::cube <@ '()'::cube),distance float8 DEFAULT cube_distance('(3,4)'::cube,'()'::cube),euclidean float8 DEFAULT ('(3,4)'::cube <-> '()'::cube),taxicab float8 DEFAULT ('(3,4)'::cube <#> '()'::cube),chebyshev float8 DEFAULT ('(3,4)'::cube <=> '()'::cube))"

func cubeSpatialStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	cubeSpatialSeed(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW cube_spatial_view AS "+cubeSpatialTableQuery(), 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(cubeSpatialDefaultDDL, 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cube_spatial_defaults(id) VALUES (1)", 1))
}

func cubeSpatialStoredChecks(b fixture.Backend) []fixture.Step {
	return []fixture.Step{
		cubeSpatialTableCheck(b),
		cubeSpatialSelectNullable(b, "SELECT id,"+strings.Join(cubeSpatialNames, ",")+" FROM cube_spatial_view ORDER BY id", append([]string{"id"}, cubeSpatialNames...), append([]string{"INT4"}, cubeSpatialTypes...), cubeSpatialFixtureRows(), true),
		fixture.Select("SELECT "+strings.Join(cubeSpatialNames, ",")+" FROM cube_spatial_defaults ORDER BY id", cubeSpatialNames, cubeSpatialTypes, [][]any{cubeSpatialResult(false, true, false, 5, 7, 4)}),
	}
}

func cubeSpatialVerifyStored(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	for _, step := range cubeSpatialStoredChecks(b) {
		fixture.RunStep(t, p.Ctx, db, recorder, step)
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(step))
	}
	for _, step := range cubeSpatialCatalogChecks("public") {
		fixture.RunStep(t, p.Ctx, db, recorder, step)
	}
	// The restored defaults must execute, not merely survive as printed text.
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("INSERT INTO cube_spatial_defaults(id) VALUES (2)", 1))
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT "+strings.Join(cubeSpatialNames, ",")+" FROM cube_spatial_defaults WHERE id=2", cubeSpatialNames, cubeSpatialTypes, [][]any{cubeSpatialResult(false, true, false, 5, 7, 4)}))
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("DELETE FROM cube_spatial_defaults WHERE id=2", 1))
}

func cubeSpatialRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		values := make([]any, 8)
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained spatial rows %#v want %#v", got, want)
	}
}

func cubeSpatialMetadataSnapshot(t *testing.T, cols []*sql.ColumnType) []any {
	t.Helper()
	var snapshot []any
	for _, col := range cols {
		nullable, knownNull := col.Nullable()
		length, knownLength := col.Length()
		precision, scale, knownDecimal := col.DecimalSize()
		snapshot = append(snapshot, []any{col.Name(), col.DatabaseTypeName(), col.ScanType(), nullable, knownNull, length, knownLength, precision, scale, knownDecimal})
	}
	return snapshot
}

// The caller closes the retained cursor before releasing its physical owner.
func cubeSpatialPending(t *testing.T, p *fixture.Pair, c fixture.Executor) (*sql.Rows, []*sql.ColumnType, []any) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, cubeSpatialTableQuery())
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

// Restore replays into an isolated engine: a permanent spacer is replaced,
// not an allocator perturbation. Retain actual TEMP identities on a pinned
// owner and prove that all selected source IDs collide before importing.
func cubeSpatialRetainCollisions(t *testing.T, p *fixture.Pair, c *sql.Conn, targets [3]int64) {
	t.Helper()
	remaining := map[int64]bool{}
	var maximum int64
	for _, id := range targets {
		if id <= 0 {
			t.Fatalf("invalid collision target %d", id)
		}
		remaining[id] = true
		maximum = max(maximum, id)
	}
	for i := 0; ; i++ {
		name := "cube_spatial_allocator_" + strconv.Itoa(i)
		fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec("CREATE TEMP TABLE "+name+"(v integer)", 0))
		var relation, rowType, array int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,reltype::bigint FROM pg_class WHERE oid="+cubeLiteral("pg_temp."+name)+"::regclass::oid").Scan(&relation, &rowType); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT typarray::bigint FROM pg_type WHERE oid=$1::oid", rowType).Scan(&array); err != nil {
			t.Fatal(err)
		}
		for _, id := range []int64{relation, rowType, array} {
			if id <= 0 {
				t.Fatalf("invalid retained TEMP identity %d", id)
			}
			delete(remaining, id)
		}
		if i == 0 {
			// TEMP allocation starts with a retained ordinary/toast namespace
			// pair. These IDs can collide with the first imported cube identity.
			var namespace string
			var namespaceOwner int64
			if err := c.QueryRowContext(p.Ctx, "SELECT nspname,nspowner::bigint FROM pg_namespace WHERE oid=(SELECT relnamespace FROM pg_class WHERE oid="+cubeLiteral("pg_temp."+name)+"::regclass::oid)").Scan(&namespace, &namespaceOwner); err != nil {
				t.Fatal(err)
			}
			suffix, ok := strings.CutPrefix(namespace, "pg_temp_")
			if !ok || suffix == "" || namespaceOwner <= 0 {
				t.Fatalf("unexpected owned TEMP namespace %q/%d", namespace, namespaceOwner)
			}
			namespaces, err := c.QueryContext(p.Ctx, "SELECT oid::bigint,nspowner::bigint FROM pg_namespace WHERE nspname IN ($1,$2)", namespace, "pg_toast_temp_"+suffix)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for namespaces.Next() {
				var id, owner int64
				if err := namespaces.Scan(&id, &owner); err != nil {
					namespaces.Close()
					t.Fatal(err)
				}
				if id <= 0 || owner != namespaceOwner {
					namespaces.Close()
					t.Fatalf("invalid retained TEMP namespace identity/owner %d/%d", id, owner)
				}
				delete(remaining, id)
				count++
			}
			readErr, closeErr := namespaces.Err(), namespaces.Close()
			if readErr != nil || closeErr != nil || count != 2 {
				t.Fatalf("retained TEMP namespace pair %d: %v/%v", count, readErr, closeErr)
			}
			fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec("INSERT INTO pg_temp.cube_spatial_allocator_0 VALUES (42)", 1))
		}
		if len(remaining) == 0 {
			return
		}
		if max(relation, rowType, array) >= maximum {
			t.Fatalf("retained TEMP allocator passed uncovered source identities %v", remaining)
		}
	}
}

func cubeSpatialNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=0", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	ext, consumer := strings.Index(dump.String(), "CREATE EXTENSION"), strings.Index(dump.String(), "CREATE TABLE")
	if ext < 0 || consumer < 0 || ext >= consumer {
		t.Fatal("extension must precede spatial consumers", dump.String())
	}
	if strings.Contains(dump.String(), "CREATE FUNCTION") || strings.Contains(dump.String(), "CREATE OPERATOR") {
		t.Fatal("extension spatial members leaked as standalone DDL", dump.String())
	}
	rows, cols, metadata := cubeSpatialPending(t, p, c)
	defer rows.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	cubeSpatialVerifyStored(t, p, b, c, b.Recorder)
	cubeSpatialRetained(t, rows, cubeSpatialFixtureRows())
	if !reflect.DeepEqual(cubeSpatialMetadataSnapshot(t, cols), metadata) {
		t.Fatal("same-driver replacement mutated captured metadata")
	}
	fresh := fixture.NewFixture(t, false, true)
	freshOwner, err := fresh.Memory.Conn(fresh.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer freshOwner.Close()
	const identities = "SELECT 'public.cube'::regtype::oid::bigint,'public.cube_distance(public.cube,public.cube)'::regprocedure::oid::bigint,'public.<->(public.cube,public.cube)'::regoperator::oid::bigint"
	var original [3]int64
	if err := c.QueryRowContext(p.Ctx, identities).Scan(&original[0], &original[1], &original[2]); err != nil {
		t.Fatal(err)
	}
	cubeSpatialRetainCollisions(t, fresh, freshOwner, original)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var remapped [3]int64
	if err := fresh.Memory.QueryRowContext(fresh.Ctx, identities).Scan(&remapped[0], &remapped[1], &remapped[2]); err != nil {
		t.Fatal(err)
	}
	for i, id := range remapped {
		if id <= 0 || id == original[i] {
			t.Fatalf("allocator perturbation did not remint type/procedure/operator: %v/%v", original, remapped)
		}
	}
	cubeSpatialVerifyStored(t, fresh, b, fresh.Memory, fresh.MemoryRecord)
	fixture.RunStep(t, fresh.Ctx, freshOwner, fresh.MemoryRecord, fixture.Select("SELECT v FROM pg_temp.cube_spatial_allocator_0", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	old, oldCols, oldMetadata := cubeSpatialPending(t, fresh, fresh.Memory)
	defer old.Close()
	// Stored defaults use normal callable binding, while Restore INSERT
	// scalars retain their narrower traversal whitelist. Stage a valid
	// default and row before the later excluded producer; this control API
	// rejection is not an SQL codec diagnostic.
	prefix := "CREATE EXTENSION cube; CREATE TABLE cube_spatial_staged(d float8 DEFAULT cube_distance('(3,4)'::cube,'()'::cube)); INSERT INTO cube_spatial_staged VALUES (5::float8); INSERT INTO cube_spatial_staged VALUES (cube_distance("
	bad := prefix + "'(1,)'::cube,'()'::cube));"
	restoreErr := stuntgres.Restore(fresh.Ctx, fresh.Native, strings.NewReader(bad))
	if restoreErr == nil || restoreErr.Error() != "stuntgres: invalid restore scalar" {
		t.Fatal("Restore changed its excluded scalar boundary", restoreErr)
	}
	var state interface{ SQLState() string }
	if errors.As(restoreErr, &state) {
		t.Fatal("ordinary Restore traversal error became SQLSTATE", restoreErr)
	}
	cubeSpatialVerifyStored(t, fresh, b, fresh.Memory, fresh.MemoryRecord)
	fixture.RunStep(t, fresh.Ctx, freshOwner, fresh.MemoryRecord, fixture.Select("SELECT v FROM pg_temp.cube_spatial_allocator_0", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	cubeSpatialRetained(t, old, cubeSpatialFixtureRows())
	if !reflect.DeepEqual(cubeSpatialMetadataSnapshot(t, oldCols), oldMetadata) {
		t.Fatal("failed replacement mutated captured metadata")
	}
	fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Exec("SET extra_float_digits=0", 0))
	var again bytes.Buffer
	if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("spatial ownership/default/view restore changed deterministic dump", again.String())
	}
	if p.Postgres != nil {
		// Execute the actual emitted SQL, including native owner metadata, in PG.
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("native spatial dump PostgreSQL replay", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		cubeSpatialVerifyStored(t, p, pg, p.Postgres, pg.Recorder)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceCubeSpatialControls(t *testing.T) {
	cubeSpatialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeSpatialStoredSetup(t, p, b, c)
		cubeSpatialVerifyStored(t, p, b, c, b.Recorder)
		if !b.PG {
			cubeSpatialNativeControls(t, p, b, c)
			return
		}
		// PG exposes SQL recreation, not a pretend Reset/Restore API.
		for _, q := range []string{"DROP VIEW cube_spatial_view", "DROP TABLE cube_spatial_defaults", "DROP TABLE cube_spatial_data", "DROP EXTENSION cube"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		cubeSpatialStoredSetup(t, p, b, c)
		cubeSpatialVerifyStored(t, p, b, c, b.Recorder)
	})
}

func TestReferenceCubeSpatialFeatureSmoke(t *testing.T) {
	cubeSpatialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeSpatialStoredSetup(t, p, b, c)
		cubeSpatialVerifyStored(t, p, b, c, b.Recorder)
		// Both installed families share the same namespace and cube identity.
		cubeSpatialBoth(t, p, b, c, cubeSpatialSelect(b, "SELECT cube_distance(cube(ARRAY[3,4]),cube(0)) AS distance,cube_dim(cube(1)) AS dimension,cube_is_point(cube(1)) AS point", []string{"distance", "dimension", "point"}, []string{"FLOAT8", "INT4", "BOOL"}, [][]any{{float64(5), int64(1), true}}))
		q := "SELECT " + cubeSpatialProjection("$1::cube", "$2::cube")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cubeSpatialNamedSelect(b, q, [][]any{cubeSpatialResult(false, true, false, 5, 7, 4)}, "(3,4)", "()"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, cubeSpatialNamedSelect(b, q, [][]any{{nil, nil, nil, nil, nil, nil, nil}}, nil, "()"))
		cubeSpatialBoth(t, p, b, c, fixture.Select("SELECT id FROM cube_spatial_data WHERE a && b ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
	})
}
