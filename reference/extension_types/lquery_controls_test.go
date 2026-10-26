package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func lqueryControlChecks(b fixture.Backend) []fixture.Step {
	checks := []fixture.Step{
		lquerySelect(b, "SELECT id,v FROM lq_controls ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "Top.*{,2}.sport@*.!football|tennis{1,}.Russ*|Spain"}, {int64(2), "foo{1}"}, {int64(3), "foo%@*"}, {int64(4), nil}}),
		lquerySelect(b, "SELECT v FROM lq_control_vacant", []string{"v"}, [][]any{}),
	}
	for i := range checks {
		names, nulls := checks[i].Columns, []bool{false, true}
		if i == 1 { nulls = []bool{false} }
		checks[i].CheckMetadata = func(t *testing.T, rows *sql.Rows) { lqueryMetadata(t, b.PG, rows, names, nulls) }
	}
	return checks
}

func lqueryConsumeRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		var id int64
		var value any
		if err := rows.Scan(&id, &value); err != nil { t.Fatal(err) }
		if value != nil { if _, ok := value.(string); !ok { t.Fatalf("retained raw value %T", value) } }
		got = append(got, []any{id, value})
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	if err := rows.Close(); err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(got, want) { t.Fatalf("retained rows %#v want %#v", got, want) }
}

func lqueryBadRestore() (string, fixture.Diagnostic) {
	q := "CREATE EXTENSION ltree; CREATE TABLE lq_staged(v lquery); INSERT INTO lq_staged VALUES ('a'); INSERT INTO lq_staged VALUES ('a..b');"
	want := fixture.ControlsReferenceDiagnostic("42601", "lquery syntax error at character 3")
	want.Position = lquerySQLPosition(q, "'a..b'")
	return q, want
}

func lqueryVerifyControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, s := range lqueryControlChecks(b) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
	lqueryIdentityChecks(t, p, b, c)
}

func lqueryNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO lq_controls VALUES (99,'PrivateSentinel')", 1))
	var dump bytes.Buffer
	err := stuntgres.Dump(p.Ctx, p.Native, &dump)
	_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
	if err != nil || rollbackErr != nil { t.Fatalf("Dump %v rollback %v", err, rollbackErr) }
	if strings.Contains(dump.String(), "PrivateSentinel") { t.Fatal("Dump leaked private row") }
	ext, table := strings.Index(dump.String(), "CREATE EXTENSION"), strings.Index(dump.String(), "CREATE TABLE")
	if ext < 0 || table < ext || !strings.Contains(dump.String(), "ltree") { t.Fatal("extension must precede consumers", dump.String()) }
	if strings.Contains(dump.String(), "CREATE DOMAIN") || strings.Contains(dump.String(), "CREATE TYPE") { t.Fatal("extension-owned scalar emitted standalone", dump.String()) }
	check := lqueryControlChecks(b)[0]
	retained, err := b.DB.QueryContext(p.Ctx, check.Query)
	if err != nil { t.Fatal(err) }
	defer retained.Close()
	metadata := lqueryMetadata(t, false, retained, check.Columns, []bool{false, true})
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='ltree'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='lquery'", []string{"typname"}, []string{"NAME"}, [][]any{}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	lqueryVerifyControls(t, p, b, c)
	lqueryConsumeRetained(t, retained, check.Rows)
	lqueryColumnMetadata(t, false, metadata, check.Columns, []bool{false, true})

	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE lq_allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	destination := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	lqueryVerifyControls(t, target, destination, target.Memory)
	old, err := target.Memory.QueryContext(target.Ctx, check.Query)
	if err != nil { t.Fatal(err) }
	defer old.Close()
	oldMetadata := lqueryMetadata(t, false, old, check.Columns, []bool{false, true})
	var beforeOID, afterOID int64
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT ('lquery'::regtype)::oid").Scan(&beforeOID); err != nil { t.Fatal(err) }
	bad, want := lqueryBadRestore()
	fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
	canceled, cancel := context.WithCancel(target.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) { t.Fatalf("canceled Restore %v", err) } else {
		var state interface{ SQLState() string }
		if errors.As(err, &state) { t.Fatalf("context error acquired SQLSTATE %q", state.SQLState()) }
	}
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT ('lquery'::regtype)::oid").Scan(&afterOID); err != nil { t.Fatal(err) }
	if beforeOID != afterOID { t.Fatalf("failed Restore replaced identity %d/%d", beforeOID, afterOID) }
	lqueryVerifyControls(t, target, destination, target.Memory)
	lqueryConsumeRetained(t, old, check.Rows)
	lqueryColumnMetadata(t, false, oldMetadata, check.Columns, []bool{false, true})
	var after bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil { t.Fatal(err) }
	if !bytes.Equal(after.Bytes(), dump.Bytes()) { t.Fatal("failed Restore changed deterministic Dump", after.String()) }
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil { t.Fatal("actual emitted SQL replay", err) }
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		lqueryVerifyControls(t, p, pg, pg.DB)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
	}
}

func lqueryRecreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	literalSQL := "INSERT INTO lq_controls VALUES (5,'a*@%')"
	boundSQL := "INSERT INTO lq_controls VALUES (6,$1::text::lquery)"
	literal, err := c.PrepareContext(p.Ctx, literalSQL)
	if err != nil { t.Fatal(err) }
	defer literal.Close()
	bound, err := c.PrepareContext(p.Ctx, boundSQL)
	if err != nil { t.Fatal(err) }
	defer bound.Close()
	var before, after int64
	if err := c.QueryRowContext(p.Ctx, "SELECT ('lquery'::regtype)::oid").Scan(&before); err != nil { t.Fatal(err) }
	for _, q := range []string{"DROP TABLE lq_controls", "DROP TABLE lq_control_vacant", "DROP EXTENSION ltree", "CREATE EXTENSION ltree", "CREATE TABLE lq_controls(id integer NOT NULL,v lquery)", "CREATE TABLE lq_control_vacant(v lquery NOT NULL)"} { run(fixture.Exec(q, 0)) }
	run(fixture.Exec("INSERT INTO lq_controls VALUES (1,'Top.*{,2}.sport@*.!football|tennis{1,}.Russ*|Spain'),(2,'foo{1}'),(3,'foo%@*'),(4,NULL)", 4))
	lqueryVerifyControls(t, p, b, c)
	if err := c.QueryRowContext(p.Ctx, "SELECT ('lquery'::regtype)::oid").Scan(&after); err != nil { t.Fatal(err) }
	if before <= 0 || after <= 0 || before == after { t.Fatalf("recreation identity %d/%d", before, after) }
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: literal, SQL: literalSQL}, b.Recorder, fixture.Exec(literalSQL, 1))
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: bound, SQL: boundSQL}, b.Recorder, fixture.Exec(boundSQL, 1, "*{0,2}"))
	run(lquerySelect(b, "SELECT id,v FROM lq_controls WHERE id>4 ORDER BY id", []string{"id", "v"}, [][]any{{int64(5), "a%@*"}, {int64(6), "*{,2}"}}))
}

func TestReferenceLqueryControls(t *testing.T) {
	t.Run("round_trip", func(t *testing.T) {
		lqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			lqueryInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE lq_controls(id integer NOT NULL,v lquery)", 0))
			run(fixture.Exec("CREATE TABLE lq_control_vacant(v lquery NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO lq_controls VALUES (1,'Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain'),(2,'foo{1}'),(3,'foo*@%@@**'),(4,NULL)", 4))
			lqueryVerifyControls(t, p, b, c)
			if !b.PG { lqueryNativeControls(t, p, b, c) }
			lqueryRecreate(t, p, b, c)
		})
	})
	t.Run("staged_error_location", func(t *testing.T) {
		lqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q, want := lqueryBadRestore()
			if b.PG { lqueryError(t, p, b, c, q, false, false, false, want) } else { fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(q)), &want) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='ltree'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		})
	})
}
