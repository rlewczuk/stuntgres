package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func citextControlChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor) {
	t.Helper()
	for _, tc := range []struct {
		q     string
		names []string
		nulls []bool
		rows  [][]any
	}{
		{"SELECT id,v FROM citext_values ORDER BY id", []string{"id", "v"}, []bool{false, true}, citextLiteralRows()},
		{"SELECT v FROM citext_control_empty", []string{"v"}, []bool{false}, [][]any{}},
	} {
		s := citextSelect(b, tc.q, tc.names, tc.rows)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { citextMetadata(t, b.PG, rows, tc.names, tc.nulls) }
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Prepared(s))
	}
	citextCatalog(t, p, b, executor)
}

func citextConsumeRetained(t *testing.T, rows *sql.Rows, want [][]any) {
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
				t.Fatalf("retained citext raw %T", value)
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
		t.Fatalf("retained rows %#v want %#v", got, want)
	}
}

func citextControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	citextInstall(t, p, b, c)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE citext_values(id integer NOT NULL,v citext)", 0),
		fixture.Exec("CREATE TABLE citext_control_empty(v citext NOT NULL)", 0),
		fixture.Exec(citextLiteralInsert, 6),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func citextIndependentRestore(t *testing.T, dump []byte) {
	t.Helper()
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE citext_allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	destination := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	citextControlChecks(t, target, destination, target.Memory)
	retained, err := target.Memory.QueryContext(target.Ctx, "SELECT id,v FROM citext_values ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	retainedMetadata := citextMetadata(t, false, retained, []string{"id", "v"}, []bool{false, true})
	bad := "CREATE EXTENSION citext; CREATE TABLE citext_staged(v citext); INSERT INTO citext_staged VALUES ('Valid'); INSERT INTO citext_staged VALUES (E'\\000');"
	want := citextDiagnostic("22021", `invalid byte sequence for encoding "UTF8": 0x00`)
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	citextControlChecks(t, target, destination, target.Memory)
	citextConsumeRetained(t, retained, citextLiteralRows())
	citextColumnMetadata(t, false, retainedMetadata, []string{"id", "v"}, []bool{false, true})
	var after bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after.Bytes(), dump) {
		t.Fatal("successful/failed Restore changed deterministic dump", after.String())
	}
}

// Native control phases share the SQL setup/oracle without inventing a
// PostgreSQL control API.
func citextNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO citext_values VALUES (99,'citext-private-marker')", 1))
	var dump bytes.Buffer
	err := stuntgres.Dump(p.Ctx, p.Native, &dump)
	_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
	if err != nil || rollbackErr != nil {
		t.Fatalf("Dump %v rollback %v", err, rollbackErr)
	}
	if strings.Contains(dump.String(), "citext-private-marker") {
		t.Fatal("uncommitted row escaped Dump")
	}
	ext, table := strings.Index(dump.String(), "CREATE EXTENSION"), strings.Index(dump.String(), "CREATE TABLE")
	if ext < 0 || table < ext || !strings.Contains(dump.String(), "citext") || strings.Contains(dump.String(), "CREATE TYPE") {
		t.Fatal("extension ordering/type ownership", dump.String())
	}
	old, err := c.QueryContext(p.Ctx, "SELECT id,v FROM citext_values ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata := citextMetadata(t, false, old, []string{"id", "v"}, []bool{false, true})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='citext'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	citextControlChecks(t, p, b, c)
	citextConsumeRetained(t, old, citextLiteralRows())
	citextColumnMetadata(t, false, metadata, []string{"id", "v"}, []bool{false, true})
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual native Dump SQL PostgreSQL replay", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		citextControlChecks(t, p, pg, pg.DB)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceCitextControls(t *testing.T) {
	t.Run("round_trip", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextControlSetup(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			citextControlChecks(t, p, b, c)
			if !b.PG {
				citextNativeControls(t, p, b, c)
				return
			}
			for _, q := range []string{"DROP TABLE citext_values", "DROP TABLE citext_control_empty", "DROP EXTENSION citext", "CREATE EXTENSION citext", "CREATE TABLE citext_values(id integer NOT NULL,v citext)", "CREATE TABLE citext_control_empty(v citext NOT NULL)"} {
				run(fixture.Exec(q, 0))
			}
			for _, row := range citextLiteralRows() {
				run(fixture.Exec("INSERT INTO citext_values VALUES ($1,$2)", 1, row...))
			}
			citextControlChecks(t, p, b, c)
		})
	})
	// The shared setup still runs on both backends. PostgreSQL has no native
	// control APIs; its value/catalog recreation oracle remains in round_trip.
	// Keep these independent native lifecycles out of that fixture's budget,
	// without repeating its PostgreSQL read/recreation cases.
	t.Run("control_boundaries", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextControlSetup(t, p, b, c)
			if b.PG {
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			citextControlBoundaries(t, p, b, c, dump.Bytes())
		})
	})
	t.Run("independent_restore", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextControlSetup(t, p, b, c)
			if b.PG {
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			citextIndependentRestore(t, dump.Bytes())
		})
	})
	t.Run("malformed_replay_oracle", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := "CREATE EXTENSION citext; CREATE TABLE citext_staged(v citext); INSERT INTO citext_staged VALUES ('Valid'); INSERT INTO citext_staged VALUES (E'\\000');"
			want := citextDiagnostic("22021", `invalid byte sequence for encoding "UTF8": 0x00`)
			if b.PG {
				citextError(t, p, b, c, q, false, false, false, want)
			} else {
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(q)), &want)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='citext'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		})
	})
}
