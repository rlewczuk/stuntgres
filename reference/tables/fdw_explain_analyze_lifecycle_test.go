package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWExplainAnalyzeLifecycle(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		want := fdwAdvancedLive(t, p, b, r, run)
		run(fixture.Exec("ANALYZE fdw_data", 0))
		old := fdwExplainText(t, p, c, "EXPLAIN (VERBOSE) SELECT * FROM fdw_data")
		saved := strings.Join(old, "\n")
		// Shared ordinary failure keeps published distributions and remote rows.
		run(fixture.ErrorDiagnostic("ANALYZE fdw_data(missing)", fdwDiagnostic("42703", `column "missing" of relation "fdw_data" does not exist`)))
		run(fixture.Prepared(fixture.ErrorDiagnostic("ANALYZE fdw_data(label,label)", fdwDiagnostic("42701", `column "label" of relation "fdw_data" appears more than once`))))
		fdwPopulation(run, 20)
		fdwWantEstimate(t, p, c, "label='common'", 14)
		fdwAssertSource(t, p, b, r, want)
		if !b.PG {
			for _, phase := range []string{"GetForeignRelSize", "ExplainForeignScan", "ExplainForeignModify", "AnalyzeForeignTable", "AcquireSampleRows"} {
				for _, action := range []string{"mapping", "cancel", "provider"} {
					fdwGatedInformation(t, p, b, c, r, phase, action, nil)
					fdwPopulation(run, 20)
					fdwWantEstimate(t, p, c, "label='common'", 14)
					fdwAssertSource(t, p, b, r, want)
				}
			}
			for _, phase := range []string{"GetForeignPaths", "GetForeignPlan", "ExplainForeignScan", "AnalyzeForeignTable", "AcquireSampleRows"} {
				failure := errors.New("provider information failed")
				r.setHook(func(ctx context.Context, name string) error {
					if name == phase {
						return failure
					}
					return ctx.Err()
				})
				query := "EXPLAIN (VERBOSE) SELECT * FROM fdw_data"
				if strings.Contains(phase, "Analyze") || phase == "AcquireSampleRows" {
					query = "ANALYZE fdw_data"
				}
				_, err := c.ExecContext(p.Ctx, query)
				r.setHook(nil)
				if !errors.Is(err, failure) {
					t.Fatal(phase, err)
				}
				fdwPopulation(run, 20)
			}
			// Transport corruption of a genuine acquired row must fail import,
			// not replace prior facts with a fake partial sample.
			r.mu.Lock()
			r.rowTransform = func(_ context.Context, row stuntgres.ForeignRow) (stuntgres.ForeignRow, error) {
				row[0] = "not an integer"
				return row, nil
			}
			r.mu.Unlock()
			_, err := c.ExecContext(p.Ctx, "ANALYZE fdw_data")
			r.mu.Lock()
			r.rowTransform = nil
			r.mu.Unlock()
			fixture.AssertDiagnostic(t, err, new(fdwDiagnostic("22P02", `invalid input syntax for type integer: "not an integer"`)))
			fdwPopulation(run, 20)
			fdwWantEstimate(t, p, c, "label='common'", 14)
			fdwInformationControls(t, p, b, c, r, run)
			fdwAssertSource(t, p, b, r, want)
		}
		if strings.Join(old, "\n") != saved {
			t.Fatal("saved TEXT rows mutated")
		}
	})
}

func fdwGatedInformation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, phase, action string, replacement []byte) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	failure := errors.New("gated provider information failed")
	gate := func(ctx context.Context, name string) error {
		if name != phase {
			return ctx.Err()
		}
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
			if action == "provider" {
				return failure
			}
			return ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.setHook(gate)
	var previousWriteHook func(context.Context, string) error
	var transactions, modifications int
	if r.writer != nil {
		previousWriteHook = r.writer.writeHook
		transactions, modifications = r.writer.transactions, r.writer.modifies
		r.writer.writeHook = gate
	}
	query := "EXPLAIN (VERBOSE) SELECT * FROM fdw_data"
	if strings.Contains(phase, "Analyze") || phase == "AcquireSampleRows" {
		query = "ANALYZE fdw_data"
	}
	if phase == "ExplainForeignModify" {
		query = "EXPLAIN (VERBOSE) UPDATE fdw_data SET label='never' WHERE id=1"
	}
	done := make(chan error, 1)
	go func() {
		rows, err := c.QueryContext(ctx, query)
		if rows != nil {
			rows.Close()
			if err == nil {
				err = errors.New("stale callback published public rows")
			}
		}
		done <- err
	}()
	joined := false
	defer func() {
		cancel()
		unblock()
		if !joined {
			<-done
		}
		r.setHook(nil)
		if r.writer != nil {
			r.writer.writeHook = previousWriteHook
		}
	}()
	select {
	case <-entered:
	case <-p.Ctx.Done():
		t.Fatal("callback not admitted", phase)
	}
	// This real peer statement must complete while the callback remains gated.
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT 42 AS peer", []string{"peer"}, []string{"INT4"}, [][]any{{int64(42)}}))
	switch action {
	case "cancel":
		cancel()
	case "provider":
		unblock()
	case "restore":
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(replacement)); err != nil {
			t.Fatal(err)
		}
		unblock()
	case "revoke":
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("REVOKE MAINTAIN ON fdw_data FROM fdw_other", 0))
		unblock()
	default:
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(ADD user 'postgres')", 0))
		unblock()
	}
	var err error
	select {
	case err = <-done:
		joined = true
	case <-p.Ctx.Done():
		t.Fatal("callback worker not joined", phase)
	}
	if action == "cancel" {
		if !errors.Is(err, context.Canceled) {
			t.Fatal(phase, err)
		}
	} else if action == "provider" {
		if !errors.Is(err, failure) {
			t.Fatal(phase, err)
		}
	} else {
		fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
		if action == "mapping" {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(DROP user)", 0))
		}
	}
	r.setHook(nil)
	state := r.snapshot()
	if state.openPools != 0 || state.openRows != 0 {
		t.Fatal("callback resource leak", phase, state)
	}
	if r.writer != nil && (r.writer.transactions != transactions || r.writer.modifies != modifications) {
		t.Fatal("gated plain modification EXPLAIN entered writing startup")
	}
}

func fdwInformationControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	query := "EXPLAIN (VERBOSE,COSTS false) SELECT * FROM fdw_data"
	held, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	run(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER id SET STATISTICS 1", 0))
	run(fixture.Prepared(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER id SET (n_distinct=5)", 0)))
	configuration := fixture.Select("SELECT attstattarget,attoptions::text AS options FROM pg_attribute WHERE attrelid='fdw_data'::regclass AND attname='id'", []string{"attstattarget", "options"}, []string{"INT2", "TEXT"}, [][]any{{int64(1), "{n_distinct=5}"}})
	run(configuration)
	var dump bytes.Buffer
	before := r.snapshot()
	r.mu.Lock()
	validationStart := len(r.validations)
	r.mu.Unlock()
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if r.snapshot() != before {
		t.Fatal("Dump invoked information callbacks")
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE invalid(")); err == nil {
		t.Fatal("invalid Restore succeeded")
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fdwPopulation(run, 20)
	if r.snapshot() != before {
		t.Fatal("failed Restore invoked callbacks")
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	// Declaration replay legitimately validates wrapper/server/mapping/table
	// options. It must not plan, explain, sample, open or iterate remote data.
	r.mu.Lock()
	validations := append([]fdwValidation(nil), r.validations[validationStart:]...)
	r.mu.Unlock()
	wantKinds := map[stuntgres.ForeignObjectKind]int{
		stuntgres.ForeignWrapper: 1, stuntgres.ForeignServer: 1,
		stuntgres.ForeignUserMapping: 1, stuntgres.ForeignTable: 1,
	}
	for _, validation := range validations {
		wantKinds[validation.kind]--
		if validation.kind == stuntgres.ForeignTable &&
			(fdwOption(validation.options, "schema_name") != "fdw_source" || fdwOption(validation.options, "table_name") != "data") {
			t.Fatal("replay validated a different remote table", validation)
		}
	}
	for kind, remaining := range wantKinds {
		if remaining != 0 {
			t.Fatal("unexpected declaration validation schedule", kind, remaining, validations)
		}
	}
	afterReplay := r.snapshot()
	afterReplay.calls -= len(validations)
	if afterReplay != before {
		t.Fatal("declaration replay invoked non-validator callbacks", before, afterReplay)
	}
	fdwPopulation(run, -1)
	run(configuration)
	fdwExplainText(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, query)
	run(fixture.Exec("ANALYZE fdw_data", 0))
	fdwPopulation(run, 20)
	fdwWantEstimate(t, p, c, "id=7", 4)
	fdwGatedInformation(t, p, b, c, r, "AcquireSampleRows", "restore", dump.Bytes())
	fdwPopulation(run, -1)
	run(fixture.Exec("ANALYZE fdw_data", 0))
	fdwPopulation(run, 20)
	fdwGatedInformation(t, p, b, c, r, "ExplainForeignModify", "restore", dump.Bytes())
	fdwPopulation(run, -1)
	run(fixture.Exec("ANALYZE fdw_data", 0))
	fdwPopulation(run, 20)
	// A different registered destination implementation, not serialized callback
	// state, must subsequently plan/sample the same genuine external source.
	destinationReader := &fdwSQLReader{driver: r.driver, source: r.source, dsn: r.dsn, advanced: &fdwAdvancedState{}}
	driver := stuntgres.NewDriver()
	if err := destinationReader.configure(driver); err != nil {
		t.Fatal(err)
	}
	connector, err := driver.OpenConnector("fdw-information-destination")
	if err != nil {
		t.Fatal(err)
	}
	destination := sql.OpenDB(connector)
	defer destination.Close()
	if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	before = r.snapshot()
	fdwExplainText(t, p, destination, query)
	if _, err := destination.ExecContext(p.Ctx, "ANALYZE fdw_data"); err != nil {
		t.Fatal(err)
	}
	if r.snapshot() != before || destinationReader.snapshot().calls == 0 {
		t.Fatal("Restore retained source callback authority")
	}
	unconfigured := stuntgres.NewDriver()
	missingConnector, err := unconfigured.OpenConnector("fdw-information-missing")
	if err != nil {
		t.Fatal(err)
	}
	missing := sql.OpenDB(missingConnector)
	defer missing.Close()
	err = stuntgres.Restore(p.Ctx, missing, bytes.NewReader(dump.Bytes()))
	fixture.AssertControlDiagnostic(t, err, fdwDiagnostic("42883", "function public.fdw_sql_handler() does not exist"))
}
