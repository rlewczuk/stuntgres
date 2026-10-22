package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type storedSleepControlOutcome struct {
	Err           error
	SleepObserved bool
}

// Keep a live, identifiable frame below the public operation, even in optimized
// test binaries. Creation metadata alone is never admission evidence.
//
//go:noinline
func storedSleepControlWorker(ctx context.Context, operation func(context.Context) error, done chan<- error) {
	done <- operation(ctx)
}

func observeStoredSleepControl(t *testing.T, parent context.Context, name string, operation func(context.Context) error) storedSleepControlOutcome {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan error, 1)
	go storedSleepControlWorker(ctx, operation, done)
	out := storedSleepControlOutcome{}
	join := func() storedSleepControlOutcome {
		cancel()
		// Cleanup is independent of an expired scenario, not a success budget.
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case out.Err = <-done:
			return out
		case <-timer.C:
			t.Fatalf("%s: canceled worker did not join", name)
			return out
		}
	}
	stacks := make([]byte, 64<<10)
	poll := time.NewTimer(time.Hour)
	poll.Stop()
	defer poll.Stop()
	for {
		select {
		case out.Err = <-done:
			return out
		default:
		}
		n := runtime.Stack(stacks, true)
		for n == len(stacks) {
			stacks = make([]byte, len(stacks)*2)
			n = runtime.Stack(stacks, true)
		}
		for _, frames := range bytes.Split(stacks[:n], []byte("\n\n")) {
			if created := bytes.Index(frames, []byte("\ncreated by ")); created >= 0 {
				frames = frames[:created]
			}
			if bytes.Contains(frames, []byte("stuntgres/reference/datetime_test.storedSleepControlWorker(")) &&
				bytes.Contains(frames, []byte("stuntgres/internal/engine/functions.datetimeSleepWaitRemaining(")) {
				out.SleepObserved = true
				return join()
			}
		}
		// Delay after sampling: accumulated ticker ticks can starve the worker.
		poll.Reset(time.Millisecond)
		select {
		case out.Err = <-done:
			return out
		case <-ctx.Done():
			return join()
		case <-poll.C:
		}
	}
}

func datetimeSleepStoredControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, assertState func()) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	healthy := func() {
		run(fixture.Select("SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
	t.Run("native-observer-calibration", func(t *testing.T) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET statement_timeout=0", 0))
		published := false
		out := observeStoredSleepControl(t, p.Ctx, "calibration", func(ctx context.Context) error {
			var v bool
			err := c.QueryRowContext(ctx, "SELECT pg_sleep(600) IS NULL").Scan(&v)
			published = err == nil
			return err
		})
		if !out.SleepObserved || !errors.Is(out.Err, context.Canceled) || published {
			t.Fatalf("calibration: admission=%t published=%t err=%v", out.SleepObserved, published, out.Err)
		}
		run(fixture.Exec("SET statement_timeout='2s'", 0))
		run(fixture.Select("SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
	if t.Failed() {
		t.FailNow()
	}

	// Preservation vector, not a newly inferred PostgreSQL canonical golden.
	defaults := func() [][3]string {
		var values [][3]string
		for _, name := range []string{"sleep_assignment_default", "sleep_default_bool", "sleep_default_null", "sleep_default_text"} {
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regclass::oid::bigint", "public."+name).Scan(&oid); err != nil {
				t.Fatal(err)
			}
			rows, err := c.QueryContext(p.Ctx, "SELECT r.relname,a.attname,pg_get_expr(d.adbin,d.adrelid) FROM (SELECT * FROM pg_catalog.pg_attrdef WHERE adrelid=$1::oid) d JOIN (SELECT * FROM pg_catalog.pg_class WHERE oid=$1::oid) r ON d.adrelid=r.oid JOIN (SELECT * FROM pg_catalog.pg_attribute WHERE attrelid=$1::oid) a ON a.attrelid=r.oid AND a.attnum=d.adnum ORDER BY a.attname", oid)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var v [3]string
				if err := rows.Scan(&v[0], &v[1], &v[2]); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				values = append(values, v)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		return values
	}
	originalDefaults := defaults()
	if len(originalDefaults) != 5 {
		t.Fatalf("stored default declarations=%v, want five", originalDefaults)
	}
	preserved := func() {
		healthy()
		assertState()
		if got := defaults(); !reflect.DeepEqual(got, originalDefaults) {
			t.Fatalf("stored defaults changed: got=%v want=%v", got, originalDefaults)
		}
	}
	observe := func(name string, operation func(context.Context) error) error {
		out := observeStoredSleepControl(t, p.Ctx, name, operation)
		if out.SleepObserved {
			t.Fatalf("%s: observed stored sleep evaluation; joined error=%v", name, out.Err)
		}
		return out.Err
	}
	expectOrdinary := func(name string, want error, operation func(context.Context) error) {
		err := observe(name, operation)
		if !errors.Is(err, want) {
			t.Fatalf("%s: operation completion/context failure; no sleep admission observed: got=%v want=%v", name, err, want)
		}
		preserved()
	}
	failure := errors.New("stored controls I/O failure")
	expectOrdinary("Dump writer failure", failure, func(ctx context.Context) error {
		return stuntgres.Dump(ctx, p.Native, fixture.CatalogControlFailingWriter{Err: failure})
	})
	expectOrdinary("Reset pre-canceled", context.Canceled, func(ctx context.Context) error {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		return stuntgres.Reset(canceled, p.Native)
	})
	expectOrdinary("Restore reader failure", failure, func(ctx context.Context) error {
		return stuntgres.Restore(ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure})
	})
	expectOrdinary("Restore canceled during read", context.Canceled, func(ctx context.Context) error {
		canceled, cancel := context.WithCancel(ctx)
		defer cancel()
		return stuntgres.Restore(canceled, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	})
	run(fixture.Error("SELECT * FROM replacement", "42P01"))
	err := observe("Restore late replay failure", func(ctx context.Context) error {
		return stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE sleep_control_staged(v integer NOT NULL); INSERT INTO sleep_control_staged VALUES(1),(NULL);"))
	})
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502",
		Message: `null value in column "v" of relation "sleep_control_staged" violates not-null constraint`,
		Detail:  "Failing row contains (null).", Schema: "public", Table: "sleep_control_staged", Column: "v",
	})
	preserved()
	run(fixture.Error("SELECT * FROM sleep_control_staged", "42P01"))

	success := func(name string, operation func(context.Context) error) {
		if err := observe(name, operation); err != nil {
			t.Fatalf("%s: operation completion/context failure; no sleep admission observed: %v", name, err)
		}
	}
	var dump bytes.Buffer
	success("Dump", func(ctx context.Context) error { return stuntgres.Dump(ctx, p.Native, &dump) })
	success("Reset", func(ctx context.Context) error { return stuntgres.Reset(ctx, p.Native) })
	run(fixture.Error("SELECT * FROM sleep_assignment", "42P01"))
	run(fixture.Error("SELECT * FROM sleep_serialization", "42P01"))
	success("Restore", func(ctx context.Context) error {
		return stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes()))
	})
	preserved()
}
