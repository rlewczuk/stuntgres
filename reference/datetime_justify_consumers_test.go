package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
)

func datetimeJustifyAtomicity(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE j_atomic(id integer,span interval)", 0))
	run(referenceExec("INSERT INTO j_atomic VALUES(1,INTERVAL '1 hour')", 1))
	run(referenceExec("CREATE TABLE j_source(id integer,span interval)", 0))
	run(referenceExec("INSERT INTO j_source VALUES(2,INTERVAL '24 hours'),(3,INTERVAL '2147483647 days 24 hours')", 2))
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{
			"INSERT INTO j_atomic VALUES(2,justify_hours(INTERVAL '24 hours')),(3,justify_hours(INTERVAL '2147483647 days 24 hours'))",
			"INSERT INTO j_atomic SELECT id,justify_hours(span) FROM j_source ORDER BY id",
		} {
			s := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("22008", "interval out of range"))
			s.prepared, s.errorStage = prepared, "execute"
			run(s)
			run(referenceSelect("SELECT id,span FROM j_atomic ORDER BY id", []string{"id", "span"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "01:00:00"}}))
		}
		s := referenceErrorDiagnostic("UPDATE j_source SET span=justify_hours(span)", controlsReferenceDiagnostic("22008", "interval out of range"))
		s.prepared, s.errorStage = prepared, "execute"
		run(s)
		run(referenceSelect("SELECT id,span FROM j_source ORDER BY id", []string{"id", "span"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(2), "24:00:00"}, {int64(3), "2147483647 days 24:00:00"}}))
	}
	// Native precancellation is an ordinary API boundary. A fast pq request
	// can complete before cancellation is observed; SQL atomicity above remains
	// shared and does not rely on that race.
	if !b.pg {
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		_, err := c.ExecContext(ctx, "INSERT INTO j_atomic VALUES(4,justify_interval(INTERVAL '1 mon -1 hour'))")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("precanceled insert: %v", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("context cancellation fabricated SQLSTATE")
		}
	}
	run(referenceSelect("SELECT id FROM j_atomic ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
}

func TestReferenceDatetimeJustifyConsumersControls(t *testing.T) {
	datetimeJustifyScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE j_stored(id integer,span interval,normalized interval GENERATED ALWAYS AS (justify_interval(span)) STORED)", 0))
		run(referenceExec("CREATE INDEX j_expression ON j_stored ((justify_interval(span)))", 0))
		run(referenceExec("CREATE TABLE j_defaults(id integer,span interval DEFAULT justify_days(INTERVAL '65 days'))", 0))
		run(referenceExec("CREATE VIEW j_view AS SELECT id,justify_hours(span) AS span FROM j_stored WHERE justify_interval(span)=INTERVAL '1 mon'", 0))
		run(referenceExec("INSERT INTO j_stored(id,span) VALUES(1,INTERVAL '30 days'),(2,INTERVAL '1 mon')", 2))
		run(referenceExec("INSERT INTO j_defaults(id) VALUES(1)", 1))
		view := referenceSelect("SELECT id,span FROM j_view ORDER BY id", []string{"id", "span"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "30 days"}, {int64(2), "1 mon"}})
		run(view)
		run(referenceSelect("SELECT span,normalized FROM j_stored ORDER BY id", []string{"span", "normalized"}, []string{"INTERVAL", "INTERVAL"}, [][]any{{"30 days", "1 mon"}, {"1 mon", "1 mon"}}))
		run(referenceSelect("SELECT justify_interval(span) AS v,count(*) AS n FROM j_stored GROUP BY justify_interval(span)", []string{"v", "n"}, []string{"INTERVAL", "INT8"}, [][]any{{"1 mon", int64(2)}}))
		run(referenceSelect("SELECT a.id,b.id AS peer FROM j_stored a JOIN j_stored b ON justify_interval(a.span)=justify_interval(b.span) ORDER BY a.id,b.id", []string{"id", "peer"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(1)}, {int64(2), int64(2)}}))
		datetimeJustifyValue(t, p, b, c, "CASE WHEN false THEN justify_hours(INTERVAL '2147483647 days 24 hours') ELSE justify_days(INTERVAL '30 days') END", "1 mon")
		datetimeJustifyValue(t, p, b, c, "justify_interval(justify_interval(INTERVAL '1 mon -1 hour'))", "29 days 23:00:00")
		run(referenceSelect("SELECT justify_interval(span)=span AS equal FROM j_stored ORDER BY id", []string{"equal"}, []string{"BOOL"}, [][]any{{true}, {true}}))
		run(referenceExec("UPDATE j_defaults SET span=justify_interval(INTERVAL '1 mon -1 hour') WHERE id=1", 1))
		run(referenceSelect("SELECT span FROM j_defaults ORDER BY id", []string{"span"}, []string{"INTERVAL"}, [][]any{{"29 days 23:00:00"}}))
		if !b.pg {
			datetimeJustifyControls(t, p, b, c, view)
		}
		datetimeJustifyAtomicity(t, p, b, c)
	})
}

func datetimeJustifyControls(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, view referenceStep) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"justify_days", "justify_hours", "justify_interval"} {
		if !bytes.Contains(dump.Bytes(), []byte(name)) {
			t.Fatalf("dump omits %s", name)
		}
	}
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
	old, err := c.QueryContext(ctx, view.query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: c, rows: old}, b.recorder, view)
	referenceRunStep(t, p.ctx, c, b.recorder, view)
	intervalDumpMatches(t, p.ctx, p.native, dump.Bytes())
	peer, recorded, recorder := intervalReferenceDestination(t, p, "justify-peer")
	if err := stuntgres.Restore(p.ctx, peer, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, recorded, recorder, view)
	referenceRunStep(t, p.ctx, recorded, recorder, referenceExec("INSERT INTO j_defaults(id) VALUES(2)", 1))
	referenceRunStep(t, p.ctx, recorded, recorder, referenceSelect("SELECT span FROM j_defaults WHERE id=2", []string{"span"}, []string{"INTERVAL"}, [][]any{{"2 mons 5 days"}}))
	intervalDumpMatches(t, p.ctx, p.native, dump.Bytes())
	for _, q := range []string{"CREATE TABLE j_failed(span interval); INSERT INTO j_failed VALUES(justify_hours(INTERVAL '2147483647 days 24 hours'));", "CREATE TABLE j_failed(span interval); INSERT INTO j_failed VALUES(justify_days(INTERVAL 'invalid'));"} {
		err := stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString(q))
		code, message := "22008", "interval out of range"
		if bytes.Contains([]byte(q), []byte("invalid")) {
			code, message = "22007", `invalid input syntax for type interval: "invalid"`
		}
		if err == nil {
			t.Fatal("failed Restore succeeded")
		}
		want := controlsReferenceDiagnostic(code, message)
		if code == "22007" {
			want.Position = referencePosition(strings.Index(q, "'") + 1)
		}
		referenceAssertDiagnostic(t, err, &want)
		intervalDumpMatches(t, p.ctx, p.native, dump.Bytes())
	}
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	intervalDumpMatches(t, p.ctx, p.native, dump.Bytes())
	pure := "CREATE TABLE j_pure(span interval); INSERT INTO j_pure VALUES(justify_interval(INTERVAL '1 mon -1 hour'));"
	if err := stuntgres.Restore(p.ctx, peer, bytes.NewBufferString(pure)); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, recorded, recorder, referenceSelect("SELECT span FROM j_pure", []string{"span"}, []string{"INTERVAL"}, [][]any{{"29 days 23:00:00"}}))
	if p.postgres != nil {
		// Replay the actual emitted native SQL, not an independently handwritten dump.
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatalf("actual emitted SQL replay: %v\n%s", err, dump.String())
		}
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, view)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO j_defaults(id) VALUES(2)", 1))
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT span FROM j_defaults WHERE id=2", []string{"span"}, []string{"INTERVAL"}, [][]any{{"2 mons 5 days"}}))
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}
