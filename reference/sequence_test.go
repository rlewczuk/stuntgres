//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceSequenceIndependentMatrix(t *testing.T) {
	p := newReferencePair(t)
	conn := func(db *sql.DB) *sql.Conn {
		t.Helper()
		c, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	native := conn(p.memory)
	pg := conn(p.postgres)
	step := func(s referenceStep) {
		t.Helper()
		n := referenceRunStep(t, p.ctx, native, p.memoryRecord, s)
		r := referenceRunStep(t, p.ctx, pg, p.postgresRecord, s)
		if !reflect.DeepEqual(n, r) {
			t.Fatalf("%s parity native=%#v pg=%#v", s.query, n, r)
		}
	}
	scalar := func(q, label string, want int64, prepared bool) {
		step(referenceStep{query: q, prepared: prepared, selectRows: true, columns: []string{label}, types: []string{"INT8"}, rows: [][]any{{want}}})
	}
	step(referenceStep{query: "CREATE SEQUENCE s"})
	before := controlsReferenceDiagnostic("55000", `currval of sequence "s" is not yet defined in this session`)
	step(referenceStep{query: "SELECT currval('s')", state: "55000", diagnostic: &before, errorStage: "execute"})
	scalar("SELECT nextval('s')", "nextval", 1, true)
	scalar("SELECT nextval('s'::regclass)", "nextval", 2, false)
	scalar("SELECT currval('s')", "currval", 2, true)
	scalar("SELECT lastval()", "lastval", 2, false)
	scalar("SELECT setval('s',42)", "setval", 42, true)
	scalar("SELECT currval('s')", "currval", 42, false)
	scalar("SELECT nextval('s')", "nextval", 43, false)
	scalar("SELECT setval('s',7,false)", "setval", 7, false)
	scalar("SELECT currval('s')", "currval", 43, false)
	scalar("SELECT nextval('s')", "nextval", 7, false)
	step(referenceStep{query: "SELECT nextval(NULL)", selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{nil}}})
	scalar("SELECT currval('s')", "currval", 7, false)
	step(referenceStep{query: "CREATE TABLE t(v BIGINT)"})
	step(referenceStep{query: "INSERT INTO t VALUES(nextval('s'))", affected: 1})
	step(referenceStep{query: "SELECT nextval('s') FROM t", selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(9)}}})
	step(referenceStep{query: "CREATE SEQUENCE bounded AS SMALLINT INCREMENT BY 2 MINVALUE 2 MAXVALUE 5 START WITH 2 CACHE 1 CYCLE"})
	for _, v := range []int64{2, 4, 2} {
		scalar("SELECT nextval('bounded')", "nextval", v, true)
	}
	step(referenceStep{query: "ALTER SEQUENCE bounded NO CYCLE"})
	scalar("SELECT nextval('bounded')", "nextval", 4, false)
	limit := controlsReferenceDiagnostic("2200H", `nextval: reached maximum value of sequence "bounded" (5)`)
	step(referenceStep{query: "SELECT nextval('bounded')", state: "2200H", diagnostic: &limit, errorStage: "execute"})
	step(referenceStep{query: "CREATE SEQUENCE descending AS SMALLINT INCREMENT -1"})
	scalar("SELECT nextval('descending')", "nextval", -1, false)
	step(referenceStep{query: "CREATE SCHEMA moved"})
	defer sequenceProbeExec(t, p.ctx, pg, "DROP SCHEMA moved CASCADE")
	step(referenceStep{query: "ALTER SEQUENCE descending SET SCHEMA moved"})
	step(referenceStep{query: "ALTER SEQUENCE moved.descending RENAME TO renamed"})
	scalar("SELECT nextval('moved.renamed')", "nextval", -2, false)
	step(referenceStep{query: "CREATE TABLE linked(a INT,b INT,c INT)"})
	step(referenceStep{query: "CREATE SEQUENCE owned OWNED BY linked.c"})
	step(referenceStep{query: "ALTER TABLE linked DROP COLUMN b"})
	scalar("SELECT nextval('owned')", "nextval", 1, false)
	step(referenceStep{query: "ALTER TABLE linked DROP COLUMN c"})
	missing := controlsReferenceDiagnostic("42P01", `relation "owned" does not exist`)
	missing.Position = "16"
	step(referenceStep{query: "SELECT nextval('owned')", state: "42P01", diagnostic: &missing, errorStage: "execute"})
	// Execute each session-state scenario against separate real physical connections.
	for _, backend := range []struct {
		name string
		db   *sql.DB
		a    *sql.Conn
	}{{"native", p.memory, native}, {"postgres", p.postgres, pg}} {
		t.Run(backend.name, func(t *testing.T) {
			b := conn(backend.db)
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 10)
			sequenceProbeValue(t, p.ctx, backend.a, "SELECT currval('s')", 9)
			tx, err := backend.a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var v int64
			if err = tx.QueryRowContext(p.ctx, "SELECT nextval('s')").Scan(&v); err != nil || v != 11 {
				t.Fatal(v, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 12)
			sequenceProbeExec(t, p.ctx, backend.a, "CREATE TEMP SEQUENCE s START 100")
			sequenceProbeValue(t, p.ctx, backend.a, "SELECT nextval('s')", 100)
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 13)
			held, err := b.PrepareContext(p.ctx, "SELECT nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			sequenceProbeExec(t, p.ctx, b, "ALTER SEQUENCE public.s RENAME TO identity")
			err = held.QueryRowContext(p.ctx).Scan(&v)
			heldMissing := controlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
			heldMissing.Position = "16"
			referenceAssertDiagnostic(t, err, &heldMissing)
			parameter, err := b.PrepareContext(p.ctx, "SELECT nextval($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer parameter.Close()
			if err = parameter.QueryRowContext(p.ctx, "identity").Scan(&v); err != nil || v != 14 {
				t.Fatal("parameter identity must not observe allocation from failed held literal", v, err)
			}
		})
	}
}
