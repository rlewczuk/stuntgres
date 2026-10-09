package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

// SQL is shared; only the public native catalog controls have no PG analogue.
func stringAggViewLifecycle(t *testing.T, p *referencePair, pg bool, db *sql.DB, recorder *referenceRecorder, run func(referenceStep)) {
	t.Helper()
	run(referenceExec(`CREATE VIEW sa_view AS SELECT string_agg(v,':' ORDER BY id DESC) AS t,string_agg(b,'\x80' ORDER BY id) AS b,string_agg('x',d ORDER BY id) AS literal_t,string_agg('\xff'::bytea,s ORDER BY id) AS literal_b FROM sa`, 0))
	run(referenceExec("ALTER TABLE sa RENAME TO renamed_sa", 0))
	const q = "SELECT t,b,literal_t,literal_b FROM sa_view"
	want := [][]any{{"c:b::a", string([]byte{0, 128, 128, 255, 128, 128}), "xSKIPx!xx/xSKIP2x", string([]byte{255, 222, 173, 255, 0, 255, 255, 92, 255, 255})}}
	s := referenceSelect(q, []string{"t", "b", "literal_t", "literal_b"}, []string{"TEXT", "BYTEA", "TEXT", "BYTEA"}, want)
	s.checkMetadata = stringAggMetadata(pg)
	run(s)
	run(referencePrepared(s))
	held, err := db.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: db, statement: held, query: q}, recorder, s)
	if pg {
		return
	}
	old, err := held.QueryContext(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	stringAggMetadata(false)(t, old)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if !old.Next() {
		t.Fatal(old.Err())
	}
	var a, b, c, d any
	if err := old.Scan(&a, &b, &c, &d); err != nil {
		t.Fatal(err)
	}
	got := []any{referenceNormalize(a), referenceNormalize(b), referenceNormalize(c), referenceNormalize(d)}
	if !reflect.DeepEqual(got, want[0]) {
		t.Fatalf("retained view=%#v want=%#v", got, want[0])
	}
	if old.Next() || old.Err() != nil {
		t.Fatal(old.Err())
	}
	old.Close()
	run(s)
	referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: db, statement: held, query: q}, recorder, s)
	run(referenceExec("CREATE TABLE restore_sentinel(v TEXT)", 0))
	run(referenceExec("INSERT INTO restore_sentinel VALUES('kept')", 1))
	session, err := db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	referenceRunStep(t, p.ctx, session, recorder, referenceExec("SET application_name='string-agg-kept'", 0))
	// Failure after a valid private-stage aggregate definition cannot publish it.
	bad := append(append([]byte(nil), dump.Bytes()...), []byte("\nSELECT missing FROM missing;\n")...)
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(bad)); err == nil {
		t.Fatal("failed Restore succeeded")
	}
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Restore=%v", err)
	}
	run(referenceSelect("SELECT v FROM restore_sentinel", []string{"v"}, []string{"TEXT"}, [][]any{{"kept"}}))
	referenceRunStep(t, p.ctx, session, recorder, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"string-agg-kept"}}))
	run(s)
	run(referenceExec("UPDATE renamed_sa SET v='z' WHERE id=4", 1))
	changed := s
	changed.rows = [][]any{{"c:z::a", want[0][1], want[0][2], want[0][3]}}
	referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: db, statement: held, query: q}, recorder, changed)
}

func TestReferenceStringAggLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			stringAggSetup(run)
			stringAggViewLifecycle(t, p, backend.pg, backend.db, backend.recorder, run)
			if backend.pg {
				return
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := backend.db.QueryContext(ctx, "SELECT string_agg(v,d ORDER BY id) FROM renamed_sa")
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled query published cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled query=%v", err)
			}
			result, err := backend.db.ExecContext(ctx, "INSERT INTO restore_sentinel SELECT string_agg(v,d ORDER BY id) FROM renamed_sa")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled insert=%v,%v", result, err)
			}
			run(referenceSelect("SELECT v FROM restore_sentinel", []string{"v"}, []string{"TEXT"}, [][]any{{"kept"}}))
			// Restore's reader failure also preserves catalog, independently of SQL errors.
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE staged(v TEXT); INSERT INTO staged VALUES('x'); SELECT string_agg(v,',' ORDER BY v) FROM staged;")); err == nil {
				t.Fatal("unsupported restore query succeeded")
			}
			run(referenceSelect("SELECT v FROM restore_sentinel", []string{"v"}, []string{"TEXT"}, [][]any{{"kept"}}))
		})
	}
}
