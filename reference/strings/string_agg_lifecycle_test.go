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

// SQL is shared; only the public native catalog controls have no PG analogue.
func stringAggViewLifecycle(t *testing.T, p *fixture.Pair, pg bool, db *sql.DB, recorder *fixture.Recorder, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec(`CREATE VIEW sa_view AS SELECT string_agg(v,':' ORDER BY id DESC) AS t,string_agg(b,'\x80' ORDER BY id) AS b,string_agg('x',d ORDER BY id) AS literal_t,string_agg('\xff'::bytea,s ORDER BY id) AS literal_b FROM sa`, 0))
	run(fixture.Exec("ALTER TABLE sa RENAME TO renamed_sa", 0))
	const q = "SELECT t,b,literal_t,literal_b FROM sa_view"
	want := [][]any{{"c:b::a", string([]byte{0, 128, 128, 255, 128, 128}), "xSKIPx!xx/xSKIP2x", string([]byte{255, 222, 173, 255, 0, 255, 255, 92, 255, 255})}}
	s := fixture.Select(q, []string{"t", "b", "literal_t", "literal_b"}, []string{"TEXT", "BYTEA", "TEXT", "BYTEA"}, want)
	s.CheckMetadata = stringAggMetadata(pg)
	run(s)
	run(fixture.Prepared(s))
	held, err := db.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: db, Statement: held, SQL: q}, recorder, s)
	if pg {
		return
	}
	old, err := held.QueryContext(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	stringAggMetadata(false)(t, old)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if !old.Next() {
		t.Fatal(old.Err())
	}
	var a, b, c, d any
	if err := old.Scan(&a, &b, &c, &d); err != nil {
		t.Fatal(err)
	}
	got := []any{fixture.Normalize(a), fixture.Normalize(b), fixture.Normalize(c), fixture.Normalize(d)}
	if !reflect.DeepEqual(got, want[0]) {
		t.Fatalf("retained view=%#v want=%#v", got, want[0])
	}
	if old.Next() || old.Err() != nil {
		t.Fatal(old.Err())
	}
	old.Close()
	run(s)
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: db, Statement: held, SQL: q}, recorder, s)
	run(fixture.Exec("CREATE TABLE restore_sentinel(v TEXT)", 0))
	run(fixture.Exec("INSERT INTO restore_sentinel VALUES('kept')", 1))
	session, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	fixture.RunStep(t, p.Ctx, session, recorder, fixture.Exec("SET application_name='string-agg-kept'", 0))
	// Failure after a valid private-stage aggregate definition cannot publish it.
	bad := append(append([]byte(nil), dump.Bytes()...), []byte("\nSELECT missing FROM missing;\n")...)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
		t.Fatal("failed Restore succeeded")
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Restore=%v", err)
	}
	run(fixture.Select("SELECT v FROM restore_sentinel", []string{"v"}, []string{"TEXT"}, [][]any{{"kept"}}))
	fixture.RunStep(t, p.Ctx, session, recorder, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"string-agg-kept"}}))
	run(s)
	run(fixture.Exec("UPDATE renamed_sa SET v='z' WHERE id=4", 1))
	changed := s
	changed.Rows = [][]any{{"c:z::a", want[0][1], want[0][2], want[0][3]}}
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: db, Statement: held, SQL: q}, recorder, changed)
}

func TestReferenceStringAggLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			stringAggSetup(run)
			stringAggViewLifecycle(t, p, backend.PG, backend.DB, backend.Recorder, run)
			if backend.PG {
				return
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := backend.DB.QueryContext(ctx, "SELECT string_agg(v,d ORDER BY id) FROM renamed_sa")
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled query published cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled query=%v", err)
			}
			result, err := backend.DB.ExecContext(ctx, "INSERT INTO restore_sentinel SELECT string_agg(v,d ORDER BY id) FROM renamed_sa")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled insert=%v,%v", result, err)
			}
			run(fixture.Select("SELECT v FROM restore_sentinel", []string{"v"}, []string{"TEXT"}, [][]any{{"kept"}}))
			// Restore's reader failure also preserves catalog, independently of SQL errors.
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE staged(v TEXT); INSERT INTO staged VALUES('x'); SELECT string_agg(v,',' ORDER BY v) FROM staged;")); err == nil {
				t.Fatal("unsupported restore query succeeded")
			}
			run(fixture.Select("SELECT v FROM restore_sentinel", []string{"v"}, []string{"TEXT"}, [][]any{{"kept"}}))
		})
	}
}
