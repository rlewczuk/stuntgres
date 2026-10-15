package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func jsonRecordControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) []fixture.Step {
	t.Helper()
	for _, q := range []string{
		`CREATE DOMAIN jr_control_positive AS integer CHECK(VALUE > 0)`,
		`CREATE DOMAIN jr_control_plain AS jr_sub`,
		`CREATE DOMAIN jr_control_array AS integer[] CHECK(array_length(VALUE,1)=2)`,
		`CREATE TYPE jr_control_object AS (a jr_control_positive,b jr_control_array,c jr_control_plain)`,
		`CREATE TABLE jr_control_source(id integer,j json)`,
		`CREATE TABLE jr_control_rows(id integer,r jr_control_object)`,
		`CREATE VIEW jr_control_typed AS SELECT s.id,r.a,r.b,r.c FROM jr_control_source s,json_to_record(s.j) AS r(a jr_control_positive,b jr_control_array,c jr_control_plain)`,
		`CREATE VIEW jr_control_scalar AS SELECT id,json_populate_record(NULL::jr_control_object,j) AS r FROM jr_control_source`,
		`CREATE VIEW jr_control_members AS SELECT * FROM ROWS FROM(json_to_record('{"a":2}') AS (a jr_control_positive),json_to_record('{"c":{"d":4,"e":"x"}}') AS (c jr_control_plain))`,
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO jr_control_source VALUES($1,$2)`, 1, 1, `{"a":2,"b":[3,4],"c":{"d":5,"e":"nested"}}`)))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jr_control_rows SELECT id,json_populate_record(NULL::jr_control_object,j) FROM jr_control_source`, 1))
	checks := []fixture.Step{
		fixture.Select(`SELECT id,a,b::text AS b,c::text AS c FROM jr_control_typed ORDER BY id`, []string{"id", "a", "b", "c"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(1), int64(2), "{3,4}", "(5,nested)"}}),
		fixture.Select(`SELECT id,(r).a AS a,(r).b::text AS b,(r).c::text AS c FROM jr_control_scalar ORDER BY id`, []string{"id", "a", "b", "c"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(1), int64(2), "{3,4}", "(5,nested)"}}),
		fixture.Select(`SELECT id,(r).a AS a,(r).b::text AS b,(r).c::text AS c FROM jr_control_rows ORDER BY id`, []string{"id", "a", "b", "c"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, [][]any{{int64(1), int64(2), "{3,4}", "(5,nested)"}}),
		fixture.Select(`SELECT a,c::text AS c FROM jr_control_members`, []string{"a", "c"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "(4,x)"}}),
	}
	jsonRecordControlState(t, p, b, c, checks)
	return checks
}

func jsonRecordControlState(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, checks []fixture.Step) {
	t.Helper()
	for _, s := range checks {
		jsonRecordBoth(t, p, b, c, s)
	}
	metadata := fixture.Select(`SELECT r.* FROM jr_control_source s,json_to_record(s.j) AS r(a jr_control_positive,b jr_control_array,c jr_control_plain)`,
		[]string{"a", "b", "c"}, []string{"INT4", "_INT4", ""}, [][]any{{int64(2), "{3,4}", "(5,nested)"}})
	jsonRecordBoth(t, p, b, c, jsonRecordMetadata(metadata, b.PG))
	for _, tc := range []struct{ input, domain, constraint string }{
		{`{"a":-1}`, "jr_control_positive", "jr_control_positive_check"},
		{`{"b":[1]}`, "jr_control_array", "jr_control_array_check"},
	} {
		jsonRecordErrorBoth(t, p, b, c, `SELECT * FROM json_populate_record(NULL::jr_control_object,'`+tc.input+`')`,
			jsonRecordDomain("23514", tc.domain, tc.constraint), false)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jr_control_rows SELECT 42,json_populate_record(NULL::jr_control_object,'{"a":6,"b":[7,8],"c":{"d":9,"e":"fresh"}}')`, 1))
	jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT (r).a AS a,(r).b::text AS b,(r).c::text AS c FROM jr_control_rows WHERE id=42`, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(6), "{7,8}", "(9,fresh)"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DELETE FROM jr_control_rows WHERE id=42`, 1))
}

func jsonRecordControlJoin(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("JSON record control worker did not join")
		return nil
	}
}

func jsonRecordControlRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, checks []fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // Only these actual public controls are native-specific.
	writer := &fixture.CatalogControlGatedWriter{Started: make(chan struct{}), Release: make(chan struct{})}
	done := make(chan error, 1)
	released, joined := false, false
	defer func() {
		if !released {
			close(writer.Release)
		}
		if !joined {
			jsonRecordControlJoin(t, done)
		}
	}()
	go func() { done <- stuntgres.Dump(p.Ctx, p.Native, writer) }()
	select {
	case <-writer.Started:
	case <-p.Ctx.Done():
		t.Fatal("Dump did not reach its writer", p.Ctx.Err())
	}
	// This actual query must finish while writer I/O is held: capture releases
	// engine locks before invoking an external writer.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, checks[0])
	close(writer.Release)
	released = true
	err := jsonRecordControlJoin(t, done)
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	dump := append([]byte(nil), writer.Output.Bytes()...)
	retained := jsonRecordMetadata(jsonRecordPair(`SELECT * FROM json_populate_record(NULL::jr_pair,'{"a":2}')`, [][]any{{int64(2), nil}}), false)
	old, err := c.QueryContext(p.Ctx, retained.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	retained.CheckMetadata(t, old)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	retained.CheckMetadata(t, old)
	fixture.StringCoreRetainedRows(t, old, nil, retained.Columns, retained.Types, retained.Rows, false)
	jsonRecordControlState(t, p, b, c, checks)

	// A genuinely different target owner with independently advanced OIDs must
	// rebind every named/domain/member usage rather than retaining source tags.
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(`CREATE TYPE jr_target_spacer AS (x int)`, 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	jsonRecordControlState(t, target, fixture.Backend{Recorder: target.MemoryRecord}, target.Memory, checks)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(`INSERT INTO jr_control_rows SELECT 2,json_populate_record(NULL::jr_control_object,'{"a":6,"b":[7,8],"c":{"d":9,"e":"target"}}')`, 1))
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select(`SELECT (r).a AS a,(r).b::text AS b,(r).c::text AS c FROM jr_control_rows WHERE id=2`, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(6), "{7,8}", "(9,target)"}}))
	jsonRecordControlState(t, p, b, c, checks)

	failure := errors.New("JSON record restore reader failure")
	if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	// The existing replay whitelist does not admit INSERT query sources.
	// Assert that boundary independently, never mistake it for a typed failure.
	rejected := append(append([]byte(nil), dump...), []byte(`INSERT INTO jr_control_rows SELECT 2,json_populate_record(NULL::jr_control_object,'{"a":"bad"}');`)...)
	err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(rejected))
	want := jsonRecordDiagnostic("0A000", "restore INSERT query sources are unsupported", 0)
	fixture.AssertDiagnostic(t, err, &want)
	jsonRecordControlState(t, p, b, c, checks)

	// Actual emitted schema/data followed by allowed literal VALUES: an earlier
	// valid tuple must not publish when a later typed composite field fails.
	suffix := `INSERT INTO jr_control_rows VALUES(2,'(2,"{3,4}","(5,early)")'),(3,'(bad,,)');`
	bad := append(append([]byte(nil), dump...), []byte(suffix)...)
	err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad))
	want = jsonRecordDiagnostic("22P02", `invalid input syntax for type integer: "bad"`,
		utf8.RuneCount(dump)+utf8.RuneCountInString(suffix[:strings.Index(suffix, "'(bad,,)'")])+1)
	fixture.AssertDiagnostic(t, err, &want)
	jsonRecordControlState(t, p, b, c, checks)
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("SELECT )"))
	want = jsonRecordDiagnostic("42601", `syntax error at or near ")"`, 8)
	fixture.AssertDiagnostic(t, err, &want)
	jsonRecordControlState(t, p, b, c, checks)

	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	reader := &fixture.SequenceGatedReader{Reader: bytes.NewReader(dump), Started: make(chan struct{}), Release: make(chan struct{})}
	restoreDone := make(chan error, 1)
	restoreReleased, restoreJoined := false, false
	defer func() {
		cancel()
		if !restoreReleased {
			close(reader.Release)
		}
		if !restoreJoined {
			jsonRecordControlJoin(t, restoreDone)
		}
	}()
	go func() { restoreDone <- stuntgres.Restore(ctx, p.Native, reader) }()
	select {
	case <-reader.Started:
	case <-p.Ctx.Done():
		t.Fatal("Restore did not reach its reader", p.Ctx.Err())
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, checks[0])
	cancel()
	close(reader.Release)
	restoreReleased = true
	err = jsonRecordControlJoin(t, restoreDone)
	restoreJoined = true
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled Restore", err)
	}
	jsonRecordControlState(t, p, b, c, checks)

	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
			t.Fatal("actual unmodified native dump replay", err)
		}
		jsonRecordControlState(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, p.Postgres, checks)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceJSONRecordControls(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		checks := jsonRecordControlSetup(t, p, b, c)
		jsonRecordControlRoundtrip(t, p, b, c, checks)
	})
}
