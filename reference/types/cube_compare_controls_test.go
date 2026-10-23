package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const cubeComparisonSchema = "cube_compare_control_ns"

func cubeComparisonControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE SCHEMA "+cubeComparisonSchema, 0))
	run(fixture.Exec("CREATE EXTENSION cube SCHEMA "+cubeComparisonSchema, 0))
	run(fixture.Exec("SET search_path="+cubeComparisonSchema+",public", 0))
	run(fixture.Exec("CREATE TABLE public.cube_compare_data(id integer NOT NULL,v cube)", 0))
	run(fixture.Exec(cubeComparisonData, 7))
	run(fixture.Exec("CREATE INDEX cube_compare_control_idx ON public.cube_compare_data(v "+cubeComparisonSchema+".cube_ops)", 0))
	run(fixture.Exec("CREATE VIEW public.cube_compare_view AS SELECT id,v<'(1)'::cube AS below,v='1'::cube AS equal,v IN ('1'::cube,'2'::cube) AS member,CASE v WHEN '1'::cube THEN true ELSE false END AS chosen,NULLIF(v,'1'::cube) AS nullified FROM public.cube_compare_data", 0))
	run(fixture.Exec("CREATE TABLE public.cube_compare_control_unique(id integer,v cube)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX cube_compare_control_key ON public.cube_compare_control_unique(v)", 0))
	run(fixture.Exec("INSERT INTO public.cube_compare_control_unique VALUES (1,'(3,1),(1,4)'),(2,'2'),(3,NULL)", 3))
}

func cubeComparisonControlChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, recorder, s) }
	run(fixture.Exec("SET search_path="+cubeComparisonSchema+",public", 0))
	for _, s := range cubeComparisonCatalogChecks(cubeComparisonSchema) {
		run(s)
		run(fixture.Prepared(s))
	}
	for _, s := range cubeComparisonConsumerChecks() {
		run(s)
	}
	// Persist each shared equality carrier, not just ordinary scalar operators.
	// These independent results run before replacement, after same-driver and
	// forced18-member imports, after failed Restore, and on actual PG replay.
	for _, s := range []fixture.Step{
		fixture.Select("SELECT id,below,equal,member,chosen FROM public.cube_compare_view ORDER BY id", []string{"id", "below", "equal", "member", "chosen"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
			{int64(1), false, false, true, false},
			{int64(2), false, true, true, true},
			{int64(3), false, true, true, true},
			{int64(4), true, false, false, false},
			{int64(5), nil, nil, nil, false},
			{int64(6), true, false, false, false},
			{int64(7), true, false, false, false},
		}),
		cubeSelect(b, "SELECT id,nullified FROM public.cube_compare_view ORDER BY id", []string{"id", "nullified"}, [][]any{
			{int64(1), "(2)"}, {int64(2), nil}, {int64(3), nil},
			{int64(4), "(-1)"}, {int64(5), nil},
			{int64(6), "(-0)"}, {int64(7), "(0)"},
		}),
	} {
		run(s)
		run(fixture.Prepared(s))
	}
	rows, err := db.QueryContext(p.Ctx, "SELECT id,nullified FROM public.cube_compare_view ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cubeMetadata(t, b.PG, rows, []string{"id", "nullified"}, []bool{false, true})
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	run(fixture.Select("SELECT '(3,1),(1,4)'::"+cubeComparisonSchema+".cube OPERATOR("+cubeComparisonSchema+".=) '(1,1),(3,4)'::"+cubeComparisonSchema+".cube AS eq,"+cubeComparisonSchema+".cube_cmp('(2)'::"+cubeComparisonSchema+".cube,'(1,100)'::"+cubeComparisonSchema+".cube) AS cmp", []string{"eq", "cmp"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(1)}}))
	run(cubeComparisonIDs("SELECT id FROM public.cube_compare_control_unique WHERE v>'(1)'::cube ORDER BY v,id", 1, 2))
	want := cubeComparisonUniqueDiagnostic("cube_compare_control_unique", "cube_compare_control_key", "(1, 1),(3, 4)")
	rejected := fixture.ErrorDiagnostic("INSERT INTO public.cube_compare_control_unique VALUES (4,'(1,1),(3,4)')", want)
	rejected.ErrorStage = "execute"
	run(rejected)
	run(fixture.Prepared(rejected))
	run(cubeComparisonIDs("SELECT id FROM public.cube_compare_control_unique ORDER BY id", 1, 2, 3))
}

func cubeComparisonRetainedRows() [][]any {
	return [][]any{{int64(4), "(-1)"}, {int64(6), "(-0)"}, {int64(7), "(0)"}, {int64(2), "(1)"}, {int64(3), "(1)"}, {int64(1), "(2)"}, {int64(5), nil}}
}

func cubeComparisonSessionSettings(t *testing.T, p *fixture.Pair, c *sql.Conn) [2]string {
	t.Helper()
	var settings [2]string
	for i, name := range []string{"search_path", "extra_float_digits"} {
		if err := c.QueryRowContext(p.Ctx, "SHOW "+name).Scan(&settings[i]); err != nil {
			t.Fatalf("comparison session setting %s capture: %#v", name, err)
		}
	}
	return settings
}

func cubeComparisonNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatalf("comparison native Dump: %#v", err)
	}
	sourceIDs := cubeComparisonMemberIDs(t, p, c)
	retained, err := c.QueryContext(p.Ctx, "SELECT id,v FROM public.cube_compare_data ORDER BY v,id")
	if err != nil {
		t.Fatalf("comparison source retained query: %#v", err)
	}
	defer retained.Close()
	metadata := cubeMetadata(t, false, retained, []string{"id", "v"}, []bool{false, true})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatalf("comparison same-driver Reset: %#v", err)
	}
	settings := cubeComparisonSessionSettings(t, p, c)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatalf("comparison same-driver Restore: %#v", err)
	}
	if got := cubeComparisonSessionSettings(t, p, c); got != settings {
		t.Fatal("comparison restore changed caller settings", got, settings)
	}
	cubeComparisonControlChecks(t, p, b, c, b.Recorder)
	cubeComparisonHeldConsumer(t, p, b, c)
	cubeConsumeRetained(t, retained, cubeComparisonRetainedRows())
	cubeColumnMetadata(t, false, metadata, []string{"id", "v"}, []bool{false, true})
	target := fixture.NewFixture(t, false, true)
	destination, err := target.Memory.Conn(target.Ctx)
	if err != nil {
		t.Fatalf("comparison destination connection: %#v", err)
	}
	defer destination.Close()
	retainedIDs := cubeComparisonForceCollisions(t, target, destination, sourceIDs)
	targetSettings := cubeComparisonSessionSettings(t, target, destination)
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatalf("comparison retained-TEMP collision Restore: %#v", err)
	}
	if got := cubeComparisonSessionSettings(t, target, destination); got != targetSettings {
		t.Fatal("comparison collision restore changed caller settings", got, targetSettings)
	}
	cubeComparisonControlChecks(t, target, fixture.Backend{}, destination, target.MemoryRecord)
	cubeComparisonHeldConsumer(t, target, fixture.Backend{Recorder: target.MemoryRecord}, destination)
	cubeComparisonCheckRemap(t, target, destination, sourceIDs, retainedIDs)
	old, err := destination.QueryContext(target.Ctx, "SELECT id,v FROM public.cube_compare_data ORDER BY v,id")
	if err != nil {
		t.Fatalf("comparison destination retained query: %#v", err)
	}
	defer old.Close()
	oldMetadata := cubeMetadata(t, false, old, []string{"id", "v"}, []bool{false, true})
	prefix := "CREATE EXTENSION cube; CREATE TABLE cube_compare_staged(v cube); INSERT INTO cube_compare_staged VALUES ('2'),("
	bad := prefix + "'(1,)');"
	want := cubeSyntaxDiagnostic(`syntax error at or near ")"`)
	want.Position = strconv.Itoa(utf8.RuneCountInString(prefix) + 1)
	targetSettings = cubeComparisonSessionSettings(t, target, destination)
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	if got := cubeComparisonSessionSettings(t, target, destination); got != targetSettings {
		t.Fatal("failed comparison restore changed caller settings", got, targetSettings)
	}
	cubeComparisonControlChecks(t, target, fixture.Backend{}, destination, target.MemoryRecord)
	cubeComparisonCheckRemap(t, target, destination, sourceIDs, retainedIDs)
	cubeConsumeRetained(t, old, cubeComparisonRetainedRows())
	cubeColumnMetadata(t, false, oldMetadata, []string{"id", "v"}, []bool{false, true})
	duplicate := cubeComparisonUniqueDiagnostic("cube_compare_control_unique", "cube_compare_control_key", "(1, 1),(3, 4)")
	s := fixture.ErrorDiagnostic("INSERT INTO public.cube_compare_control_unique VALUES (4,'(1,1),(3,4)')", duplicate)
	s.ErrorStage = "execute"
	fixture.RunStep(t, target.Ctx, destination, target.MemoryRecord, s)
	fixture.RunStep(t, target.Ctx, destination, target.MemoryRecord, cubeComparisonIDs("SELECT id FROM public.cube_compare_control_unique ORDER BY id", 1, 2, 3))
	if p.Postgres != nil {
		pgc, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatalf("comparison PostgreSQL replay connection: %#v", err)
		}
		defer pgc.Close()
		// Replay the actual native output, not a hand-written equivalent dump.
		if _, err := pgc.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+cubeComparisonSchema+" CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatalf("comparison PostgreSQL replay cleanup: %#v", err)
		}
		if _, err := pgc.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("actual comparison dump PostgreSQL replay: %#v", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		defer func() {
			if _, err := pgc.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+cubeComparisonSchema+" CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Error("comparison replay cleanup", err)
			}
		}()
		cubeComparisonControlChecks(t, p, pg, pgc, pg.Recorder)
		cubeComparisonHeldConsumer(t, p, pg, pgc)
		cubeError(t, p, pg, pgc, "INSERT INTO public.cube_compare_control_unique VALUES (4,'(1,1),(3,4)')", false, false, false, duplicate)
	}
}

func TestReferenceCubeComparisonControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, cubeComparisonSchema)
	cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeComparisonControlSetup(t, p, b, c)
		cubeComparisonControlChecks(t, p, b, c, b.Recorder)
		cubeComparisonHeldConsumer(t, p, b, c)
		if !b.PG {
			cubeComparisonNativeControls(t, p, b, c)
			return
		}
		// Initial PG discovery never invokes native controls; exercise actual SQL
		// teardown/recreation of the same qualified provider and consumers.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP SCHEMA "+cubeComparisonSchema+" CASCADE", 0))
		for _, q := range []string{"DROP VIEW public.cube_compare_view", "DROP TABLE public.cube_compare_data", "DROP TABLE public.cube_compare_control_unique"} {
			// DROP SCHEMA CASCADE has already removed dependent cube columns/view,
			// but leaves the owning tables; IF EXISTS handles the removed view.
			q = strings.Replace(q, "DROP VIEW ", "DROP VIEW IF EXISTS ", 1)
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		cubeComparisonControlSetup(t, p, b, c)
		cubeComparisonControlChecks(t, p, b, c, b.Recorder)
		cubeComparisonHeldConsumer(t, p, b, c)
		rows, err := c.QueryContext(p.Ctx, "SELECT id,v FROM public.cube_compare_data ORDER BY v,id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cubeMetadata(t, true, rows, []string{"id", "v"}, nil)
		var got [][]any
		for rows.Next() {
			var id int64
			var value any
			if err := rows.Scan(&id, &value); err != nil {
				t.Fatal(err)
			}
			if value != nil {
				raw, ok := value.([]byte)
				if !ok {
					t.Fatalf("PG cube raw %T", value)
				}
				value = string(raw)
			}
			got = append(got, []any{id, value})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, cubeComparisonRetainedRows()) {
			t.Fatalf("recreated rows %#v", got)
		}
	})
}
