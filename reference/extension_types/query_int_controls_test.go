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

func queryIntControlChecks(b fixture.Backend) []fixture.Step {
	checks := []fixture.Step{
		fixture.QueryIntSelect(b, "SELECT id,v FROM qi_controls ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "1 & ( 2 | 3 )"}, {int64(2), "!( !1 )"}, {int64(3), "8"}, {int64(4), nil}}),
		fixture.QueryIntSelect(b, "SELECT v FROM qi_control_vacant", []string{"v"}, [][]any{}),
	}
	for i := range checks {
		check := &checks[i]
		nulls := []bool{false, true}
		if i == 1 {
			nulls = []bool{false}
		}
		check.CheckMetadata = func(t *testing.T, rows *sql.Rows) { queryIntMetadata(t, b.PG, rows, check.Columns, nulls) }
	}
	return checks
}

func queryIntConsumeRetained(t *testing.T, rows *sql.Rows, check fixture.Step) {
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
				t.Fatalf("retained raw cell=%T", value)
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
	if !reflect.DeepEqual(got, check.Rows) {
		t.Fatalf("retained values %#v want %#v", got, check.Rows)
	}
}

// Native controls have no PostgreSQL API equivalent; the shared SQL portions
// and actual emitted SQL replay carry the independent oracle contract.
func queryIntNativeControlCycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	checks := queryIntControlChecks(b)
	verify := func(db fixture.Executor, recorder *fixture.Recorder) {
		for _, check := range checks {
			fixture.RunStep(t, p.Ctx, db, recorder, check)
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(check))
		}
	}
	verify(c, b.Recorder)
	if _, err := c.ExecContext(p.Ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO qi_controls VALUES (99,'987654321')", 1))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.QueryIntSelect(b, checks[0].Query, checks[0].Columns, [][]any{{int64(1), "1 & ( 2 | 3 )"}, {int64(2), "!( !1 )"}, {int64(3), "8"}, {int64(4), nil}, {int64(99), "987654321"}}))
	var dump bytes.Buffer
	err := stuntgres.Dump(p.Ctx, p.Native, &dump)
	_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
	if err != nil || rollbackErr != nil {
		t.Fatalf("committed-only Dump %v rollback %v", err, rollbackErr)
	}
	if strings.Contains(dump.String(), "987654321") {
		t.Fatal("Dump leaked private transaction")
	}
	verify(c, b.Recorder)
	ext := strings.Index(dump.String(), "CREATE EXTENSION")
	table := strings.Index(dump.String(), "CREATE TABLE")
	if ext < 0 || table < 0 || ext > table || !strings.Contains(dump.String(), "intarray") {
		t.Fatal("extension must precede query_int consumers", dump.String())
	}
	if strings.Contains(dump.String(), "CREATE DOMAIN") || strings.Contains(dump.String(), "CREATE TYPE") {
		t.Fatal("extension-owned type emitted standalone", dump.String())
	}
	retained, err := b.DB.QueryContext(p.Ctx, checks[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	metadata, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	queryIntColumnMetadata(t, false, metadata, checks[0].Columns, []bool{false, true})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	verify(c, b.Recorder)
	queryIntConsumeRetained(t, retained, checks[0])
	queryIntColumnMetadata(t, false, metadata, checks[0].Columns, []bool{false, true})
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE qi_allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	verify(target.Memory, target.MemoryRecord)
	var first, second int64
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT ('query_int'::regtype)::oid").Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT ('public.query_int'::regtype)::oid").Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first <= 0 || first != second {
		t.Fatalf("restored logical identity %d/%d", first, second)
	}
	old, err := target.Memory.QueryContext(target.Ctx, checks[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	oldMetadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	bad := "CREATE EXTENSION intarray; CREATE TABLE qi_staged(v query_int); INSERT INTO qi_staged VALUES ('1'); INSERT INTO qi_staged VALUES ('1&&2');"
	want := fixture.ControlsReferenceDiagnostic("42601", "syntax error")
	want.Position = "131"
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	verify(target.Memory, target.MemoryRecord)
	queryIntConsumeRetained(t, old, checks[0])
	queryIntColumnMetadata(t, false, oldMetadata, checks[0].Columns, []bool{false, true})
	var after bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after.Bytes(), dump.Bytes()) {
		t.Fatal("failed Restore changed committed catalog/data", after.String())
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual Dump SQL oracle replay", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		for _, check := range queryIntControlChecks(pg) {
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, check)
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, fixture.Prepared(check))
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func queryIntControlScenario(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	queryIntInstall(t, p, b, c)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE qi_controls(id integer NOT NULL,v query_int)", 0))
	run(fixture.Exec("CREATE TABLE qi_control_vacant(v query_int NOT NULL)", 0))
	run(fixture.Exec("INSERT INTO qi_controls VALUES (1,'1&(2|3)'),(2,'!!1'),(3,'010'),(4,NULL)", 4))
	for _, check := range queryIntControlChecks(b) {
		run(check)
		run(fixture.Prepared(check))
	}
	if !b.PG {
		queryIntNativeControlCycle(t, p, b, c)
		return
	}
	// PostgreSQL has SQL extension recreation, not native Reset/Dump/Restore.
	for _, q := range []string{"DROP TABLE qi_controls", "DROP TABLE qi_control_vacant", "DROP EXTENSION intarray", "CREATE EXTENSION intarray", "CREATE TABLE qi_controls(id integer NOT NULL,v query_int)", "CREATE TABLE qi_control_vacant(v query_int NOT NULL)"} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO qi_controls VALUES (1,'1 & ( 2 | 3 )'),(2,'!( !1 )'),(3,'8'),(4,NULL)", 4))
	for _, check := range queryIntControlChecks(b) {
		run(check)
		run(fixture.Prepared(check))
	}
}

func TestReferenceQueryIntControls(t *testing.T) {
	t.Run("round_trip", func(t *testing.T) { queryIntScenario(t, queryIntControlScenario) })
	t.Run("staged_error_location", func(t *testing.T) {
		queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := "CREATE EXTENSION intarray; CREATE TABLE qi_staged(v query_int); INSERT INTO qi_staged VALUES ('1'); INSERT INTO qi_staged VALUES ('1&&2');"
			want := fixture.ControlsReferenceDiagnostic("42601", "syntax error")
			want.Position = "131"
			if b.PG {
				queryIntError(t, p, b, c, q, false, false, false, want)
			} else {
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(q)), &want)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		})
	})
}

func TestReferenceQueryIntFeatureSmoke(t *testing.T) {
	queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		queryIntControlScenario(t, p, b, c)
		// Exercise the existing provider beside the new selected type provider.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION unaccent", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT unaccent('Hôtel') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
	})
}
