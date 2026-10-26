package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgWaitEventsStability(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE public.pwe_wait_probe(v INT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO public.pwe_wait_probe VALUES(7)", 1))
	holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
	reader := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
	held := pgWaitEventsHeld(t, p, reader, pgWaitEventsQuery(b))
	waiterPID, holderPID := 0, 0
	var relation int64
	if b.PG {
		waiterPID = fixture.LockPID(t, p.Ctx, waiter)
		holderPID = fixture.LockPID(t, p.Ctx, holder)
		relation = fixture.LockOID(t, p.Ctx, "public.pwe_wait_probe")
	}
	tx := fixture.QueryLockBegin(t, p.Ctx, holder)
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE public.pwe_wait_probe IN ACCESS EXCLUSIVE MODE", 0))
	pgWaitEventsRead(t, p, b, reader)
	pgWaitEventsRead(t, p, b, held)

	finished := make(chan struct{})
	// LockOperation registers bounded cancellation/join before launching the
	// actual worker, after transaction/connection cleanup registration.
	join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		rows, err := waiter.QueryContext(ctx, "SELECT v FROM public.pwe_wait_probe")
		return fixture.RelationOutcome{Rows: rows, Err: err}
	})
	fixture.RelationLockPending(t, p, b, waiterPID, holderPID, relation, "AccessShareLock", finished)
	pgWaitEventsRead(t, p, b, reader)
	pgWaitEventsRead(t, p, b, held)
	select {
	case <-finished:
		t.Fatal("waiter completed while holder still held ACCESS EXCLUSIVE")
	default:
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	out := join()
	if out.Rows != nil {
		defer out.Rows.Close()
	}
	if out.Err != nil || out.Rows == nil {
		t.Fatal("released SELECT", out.Err)
	}
	columns, err := out.Rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"v"}) {
		t.Fatal("released SELECT columns", columns, err)
	}
	types, err := out.Rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal("released SELECT type", types, err)
	}
	fixture.TransactionRowValues(t, out.Rows, 7)
	if err := out.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	pgWaitEventsRead(t, p, b, reader)
	pgWaitEventsRead(t, p, b, held)
}

// Consume a previously published cursor, not a replacement query. Exact string
// triples reject NULLs, duplicates and any extra family, and require clean EOF.
func pgWaitEventsRetained(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	defer rows.Close()
	pgWaitEventsMetadata(t, rows, pg)
	var got [][]any
	for rows.Next() {
		var typ, name, description string
		if err := rows.Scan(&typ, &name, &description); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{typ, name, description})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, pgWaitEventsExpected()) {
		t.Fatal("retained definitions", got, "want", pgWaitEventsExpected())
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unclean EOF", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func pgWaitEventsControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE public.pwe_control_probe(v INT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO public.pwe_control_probe VALUES(7)", 1))
	conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
	held := pgWaitEventsHeld(t, p, conn, pgWaitEventsQuery(b))
	pgWaitEventsRead(t, p, b, conn)
	pgWaitEventsRead(t, p, b, held)
	var dump bytes.Buffer
	if !b.PG {
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 {
			t.Fatal("empty logical dump")
		}
	}
	retained, err := b.DB.QueryContext(p.Ctx, pgWaitEventsQuery(b))
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	pgWaitEventsMetadata(t, retained, b.PG)
	if b.PG {
		// pq streams: drain this real result before more physical-session work.
		// Host replacement has no PostgreSQL equivalent.
		pgWaitEventsRetained(t, retained, true)
		pgWaitEventsRead(t, p, b, conn)
		pgWaitEventsRead(t, p, b, held)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v FROM public.pwe_control_probe", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		return
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT relname::text FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname='pwe_control_probe'", []string{"relname"}, []string{"TEXT"}, nil))
	pgWaitEventsRead(t, p, b, conn)
	pgWaitEventsRead(t, p, b, held)
	pgWaitEventsRetained(t, retained, false)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v FROM public.pwe_control_probe", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	pgWaitEventsRead(t, p, b, conn)
	pgWaitEventsRead(t, p, b, held)
}

func TestReferencePGWaitEventsStability(t *testing.T) {
	pgWaitEventsScenario(t, pgWaitEventsStability)
}

func TestReferencePGWaitEventsControls(t *testing.T) {
	pgWaitEventsScenario(t, pgWaitEventsControls)
}

func TestReferencePGWaitEventsFeatureSmoke(t *testing.T) {
	// Separate serial scenarios retain the fixture's ordinary bounded lifetime.
	for _, tc := range []struct {
		name string
		body func(*testing.T, *fixture.Pair, fixture.Backend)
	}{
		{"inventory", pgWaitEventsInventory},
		{"held_prepared", pgWaitEventsPrepared},
		{"empty_metadata", pgWaitEventsEmptyMetadata},
		{"diagnostics", pgWaitEventsDiagnostics},
		{"observed_wait_stability", pgWaitEventsStability},
		{"retained_result_controls", pgWaitEventsControls},
	} {
		t.Run(tc.name, func(t *testing.T) { pgWaitEventsScenario(t, tc.body) })
	}
}
