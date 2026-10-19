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

func cubeControlRows(lowPrecision bool) [][]any {
	precise, subnormal := "(0.10000000000000002)", "(5e-324)"
	if lowPrecision {
		precise, subnormal = "(0.1)", "(4.94065645841247e-324)"
	}
	return [][]any{{int64(1), "(3, 1),(1, 4)"}, {int64(2), "(NaN),(NaN)"}, {int64(3), "(Infinity, -Infinity)"}, {int64(4), nil}, {int64(5), precise}, {int64(6), "(-0)"}, {int64(7), subnormal}}
}

func cubeControlChecks(b fixture.Backend) []fixture.Step {
	checks := []fixture.Step{
		cubeSelect(b, "SELECT id,v FROM cube_controls ORDER BY id", []string{"id", "v"}, cubeControlRows(false)),
		cubeSelect(b, "SELECT v FROM cube_control_vacant", []string{"v"}, [][]any{}),
	}
	for i := range checks {
		check := &checks[i]
		nulls := []bool{false, true}
		if i == 1 {
			nulls = []bool{false}
		}
		check.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, check.Columns, nulls) }
	}
	return checks
}

func cubeConsumeRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		var id int64
		var value any
		if err := rows.Scan(&id, &value); err != nil {
			t.Fatal(err)
		}
		if value != nil {
			if _, ok := value.(string); !ok {
				t.Fatalf("retained cube raw cell %T", value)
			}
		}
		got = append(got, []any{id, value})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained cube values %#v want %#v", got, want)
	}
}

const cubeControlInsert = "INSERT INTO cube_controls VALUES (1,'(3,1),(1,4)'),(2,'(NaN),(NaN)'),(3,'Infinity,-Infinity'),(4,NULL),(5,'0.10000000000000002'),(6,'-0'),(7,'5e-324')"

func cubeNativeControlCycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	verify := func(db fixture.Executor, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("SET extra_float_digits=3", 0))
		for _, check := range cubeControlChecks(b) {
			fixture.RunStep(t, p.Ctx, db, recorder, check)
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(check))
		}
	}
	verify(c, b.Recorder)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=0", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cube_controls VALUES (99,'987654321')", 1))
	var dump bytes.Buffer
	err := stuntgres.Dump(p.Ctx, p.Native, &dump)
	_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
	if err != nil || rollbackErr != nil {
		t.Fatalf("committed-only Dump %v rollback %v", err, rollbackErr)
	}
	if strings.Contains(dump.String(), "987654321") {
		t.Fatal("Dump leaked private transaction")
	}
	ext, table := strings.Index(dump.String(), "CREATE EXTENSION"), strings.Index(dump.String(), "CREATE TABLE")
	if ext < 0 || table < 0 || ext > table || !strings.Contains(dump.String(), "cube") {
		t.Fatal("extension must precede cube consumers", dump.String())
	}
	if strings.Contains(dump.String(), "CREATE DOMAIN") || strings.Contains(dump.String(), "CREATE TYPE") {
		t.Fatal("extension-owned type emitted standalone", dump.String())
	}
	// Old reader text is captured with low display precision; the dump must
	// independently preserve the full value rather than serialize this display.
	retained, err := c.QueryContext(p.Ctx, "SELECT id,v FROM cube_controls ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	metadata := cubeMetadata(t, false, retained, []string{"id", "v"}, []bool{false, true})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='cube'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	verify(c, b.Recorder)
	cubeConsumeRetained(t, retained, cubeControlRows(true))
	cubeColumnMetadata(t, false, metadata, []string{"id", "v"}, []bool{false, true})
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE cube_allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	verify(target.Memory, target.MemoryRecord)
	var first, second int64
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT ('cube'::regtype)::oid").Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT ('public.cube'::regtype)::oid").Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first <= 0 || first != second {
		t.Fatalf("restored cube identity %d/%d", first, second)
	}
	old, err := target.Memory.QueryContext(target.Ctx, "SELECT id,v FROM cube_controls ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	oldMetadata := cubeMetadata(t, false, old, []string{"id", "v"}, []bool{false, true})
	badPrefix := "CREATE EXTENSION cube; CREATE TABLE cube_staged(v cube); INSERT INTO cube_staged VALUES ('1'); INSERT INTO cube_staged VALUES ("
	bad := badPrefix + "'(1,)');"
	want := cubeSyntaxDiagnostic(`syntax error at or near ")"`)
	want.Position = strconv.Itoa(utf8.RuneCountInString(badPrefix) + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	verify(target.Memory, target.MemoryRecord)
	cubeConsumeRetained(t, old, cubeControlRows(false))
	cubeColumnMetadata(t, false, oldMetadata, []string{"id", "v"}, []bool{false, true})
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=0", 0))
	var after bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after.Bytes(), dump.Bytes()) {
		t.Fatal("restored/failed Restore changed deterministic dump", after.String())
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual cube Dump SQL oracle replay", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, fixture.Exec("SET extra_float_digits=3", 0))
		for _, check := range cubeControlChecks(pg) {
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, check)
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, fixture.Prepared(check))
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func cubeControlScenario(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	cubeInstall(t, p, b, c)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE cube_controls(id integer NOT NULL,v cube)", 0))
	run(fixture.Exec("CREATE TABLE cube_control_vacant(v cube NOT NULL)", 0))
	run(fixture.Exec(cubeControlInsert, 7))
	for _, check := range cubeControlChecks(b) {
		run(check)
		run(fixture.Prepared(check))
	}
	if !b.PG {
		cubeNativeControlCycle(t, p, b, c)
		return
	}
	for _, q := range []string{"DROP TABLE cube_controls", "DROP TABLE cube_control_vacant", "DROP EXTENSION cube", "CREATE EXTENSION cube", "CREATE TABLE cube_controls(id integer NOT NULL,v cube)", "CREATE TABLE cube_control_vacant(v cube NOT NULL)"} {
		run(fixture.Exec(q, 0))
	}
	for _, row := range cubeControlRows(false) {
		run(fixture.Exec("INSERT INTO cube_controls VALUES ($1,$2)", 1, row...))
	}
	for _, check := range cubeControlChecks(b) {
		run(check)
		run(fixture.Prepared(check))
	}
}

func TestReferenceCubeControls(t *testing.T) {
	t.Run("round_trip", func(t *testing.T) { cubeScenario(t, cubeControlScenario) })
	t.Run("staged_error_location", func(t *testing.T) {
		cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			prefix := "CREATE EXTENSION cube; CREATE TABLE cube_staged(v cube); INSERT INTO cube_staged VALUES ('1'); INSERT INTO cube_staged VALUES ("
			q := prefix + "'(1,)');"
			want := cubeSyntaxDiagnostic(`syntax error at or near ")"`)
			want.Position = strconv.Itoa(utf8.RuneCountInString(prefix) + 1)
			if b.PG {
				cubeError(t, p, b, c, q, false, false, false, want)
			} else {
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(q)), &want)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='cube'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		})
	})
}

func TestReferenceCubeFeatureSmoke(t *testing.T) {
	cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE cube_smoke(id integer NOT NULL,v cube)", 0))
		run(fixture.Exec("INSERT INTO cube_smoke VALUES (1,'(1,2),(3,4)')", 1))
		q := "INSERT INTO cube_smoke VALUES ($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 2, "(3,1),(1,4)"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 3, nil))
		s := cubeSelect(b, "SELECT id,v FROM cube_smoke ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(1, 2),(3, 4)"}, {int64(2), "(3, 1),(1, 4)"}, {int64(3), nil}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, s.Columns, []bool{false, true}) }
		run(s)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			run(s)
		} else {
			run(fixture.Exec("DROP TABLE cube_smoke", 0))
			run(fixture.Exec("DROP EXTENSION cube", 0))
			cubeInstall(t, p, b, c)
			run(fixture.Exec("CREATE TABLE cube_smoke(id integer NOT NULL,v cube)", 0))
			run(fixture.Exec("INSERT INTO cube_smoke VALUES (1,'(1, 2),(3, 4)'),(2,'(3, 1),(1, 4)'),(3,NULL)", 3))
			run(s)
		}
	})
}
