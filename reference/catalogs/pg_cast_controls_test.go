package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgCastSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	s := fixture.Select("SELECT * FROM pgcast_control_sentinel", []string{"id", "words"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
}

func pgCastRetained(t *testing.T, rows *sql.Rows, types []*sql.ColumnType, ids map[[2]int64]int64) {
	t.Helper()
	facts := fixture.PgCastOrdered(pgCastFacts())
	index := 0
	for rows.Next() {
		var oid, source, target, function int64
		var context, method string
		if err := rows.Scan(&oid, &source, &target, &function, &context, &method); err != nil {
			t.Fatal(err)
		}
		if index >= len(facts) || !reflect.DeepEqual([]any{source, target, function, context, method}, facts[index]) || ids[[2]int64{source, target}] != oid {
			t.Fatal("retained cast changed", index, oid, source, target)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != 70 {
		t.Fatal("retained cast count", index)
	}
	// Captured public descriptors remain usable after exhaustion and controls.
	for i, typ := range types {
		if typ.Name() != fixture.PgCastColumns[i] || typ.DatabaseTypeName() != fixture.PgCastTypes[i] {
			t.Fatal("retained metadata changed", i, typ)
		}
		if nullable, known := typ.Nullable(); nullable || !known {
			t.Fatal("retained nullability", i, nullable, known)
		}
	}
}

func pgCastNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, dump string) {
	t.Helper()
	ids := pgCastIdentities(t, p, b)
	old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_cast ORDER BY castsource,casttarget")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgCastMetadata(t, old, false)
	types, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, pgCastProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgCastProbe}
	check := func() {
		pgCastSentinel(t, p, b, b.DB)
		fixture.PgCastCheckFacts(t, p, b, b.DB, pgCastFacts(), true)
		if fresh := pgCastIdentities(t, p, b); !reflect.DeepEqual(fresh, ids) {
			t.Fatal("control changed fixed cast identities", ids, fresh)
		}
		fixture.PgDescriptionExecutorRows(t, p, b, held, fixture.Select(pgCastProbe, fixture.PgCastColumns, fixture.PgCastTypes, [][]any{{ids[[2]int64{23, 20}], int64(23), int64(20), int64(481), "i", "f"}}, 23, 20))
	}
	// Valid dumped statements stage before a supported definition fails binding.
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"CREATE SEQUENCE pgcast_control_invalid CACHE 0;"))
	want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
	fixture.AssertDiagnostic(t, err, &want)
	check()
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
		t.Fatal("Restore cancellation", err)
	}
	check()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.PgCastCheckFacts(t, p, b, b.DB, pgCastFacts(), true)
	if fresh := pgCastIdentities(t, p, b); !reflect.DeepEqual(fresh, ids) {
		t.Fatal("Reset changed cast identities", ids, fresh)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	check()
	pgCastRetained(t, old, types, ids)
	pgCastCanceled(t, p, b, ids)
}

func pgCastControlsScenario(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	// Serialize actual supported user SQL once for native reconstruction and
	// PostgreSQL replay; neither backend serializes the catalog projection.
	native := p.Backends()[0]
	fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Exec("CREATE TABLE pgcast_control_sentinel(id INT,words TEXT)", 0))
	fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Exec("INSERT INTO pgcast_control_sentinel VALUES(7,'kept')", 1))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE TABLE") || !strings.Contains(dump.String(), "INSERT INTO") || strings.Contains(dump.String(), "pg_cast") {
		t.Fatal("Dump must serialize user data, not cast catalog", dump.String())
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				before := pgCastIdentities(t, p, b)
				conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := conn.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("PG18 rejected actual native Dump", err)
				}
				pgCastSentinel(t, p, b, conn)
				fixture.PgCastCheckFacts(t, p, b, conn, pgCastFacts(), false)
				if after := pgCastIdentities(t, p, b); !reflect.DeepEqual(before, after) {
					t.Fatal("user replay changed cast identities", before, after)
				}
			} else {
				pgCastNativeControls(t, p, b, dump.String())
			}
		})
	}
}

func TestReferencePGCastControls(t *testing.T) { pgCastControlsScenario(t) }
