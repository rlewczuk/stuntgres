package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceSequenceDefaultsPrepared(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c := sequenceCoreConn(t, p.ctx, backend.db)
			sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s")
			q := "SELECT nextval('s')"
			stmt, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
			for _, n := range []int64{1, 2} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceStep{query: q, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{n}}})
			}
			sequenceProbeValue(t, p.ctx, c, "SELECT currval('s')", 2)
			sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 2)
			referenceRunStep(t, p.ctx, c, backend.recorder, referenceStep{query: "SELECT nextval(NULL),currval(NULL),setval(NULL,42),setval('s',NULL),setval('s',42,NULL)", selectRows: true, columns: []string{"nextval", "currval", "setval", "setval", "setval"}, types: []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, rows: [][]any{{nil, nil, nil, nil, nil}}})
			sequenceProbeValue(t, p.ctx, c, q, 3)
		})
	}
}

func TestReferenceSequenceSessionsSetvalRollback(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s")
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE t")
			for _, tc := range []struct{ q, msg string }{{"SELECT currval('s')", `currval of sequence "s" is not yet defined in this session`}, {"SELECT lastval()", "lastval is not yet defined in this session"}} {
				want := controlsReferenceDiagnostic("55000", tc.msg)
				var value int64
				err := a.QueryRowContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), tc.q).Scan(&value)
				referenceAssertDiagnostic(t, err, &want)
			}
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 1)
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 2)
			for _, tc := range []struct {
				q string
				n int64
			}{{"SELECT currval('s')", 1}, {"SELECT setval('s',42)", 42}, {"SELECT currval('s')", 42}, {"SELECT nextval('s')", 43}, {"SELECT setval('s',42,false)", 42}, {"SELECT currval('s')", 43}, {"SELECT nextval('s')", 42}, {"SELECT setval('t',80,true)", 80}, {"SELECT lastval()", 42}} {
				sequenceProbeValue(t, p.ctx, a, tc.q, tc.n)
			}
			sequenceProbeExec(t, p.ctx, a, "BEGIN")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 43)
			sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 44)
			sequenceProbeExec(t, p.ctx, a, "BEGIN")
			sequenceProbeValue(t, p.ctx, a, "SELECT setval('s',90,false)", 90)
			sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 90)
		})
	}
}

func TestReferenceSequenceCacheBoundsRow(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s CACHE 10")
			initial := referenceStep{query: "SELECT * FROM s", selectRows: true, columns: []string{"last_value", "log_cnt", "is_called"}, types: []string{"INT8", "INT8", "BOOL"}, rows: [][]any{{int64(1), int64(0), false}}}
			initial.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
				var last, log int64
				var called bool
				if err := rows.Scan(&last, &log, &called); err != nil || rowIndex != 0 || last != 1 || log != 0 || called {
					t.Fatal("initial cache state", rowIndex, last, log, called, err)
				}
			}
			referenceRunStep(t, p.ctx, a, backend.recorder, initial)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 1)
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 11)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 2)
			log := int64(0)
			if backend.pg {
				log = 22
			}
			populated := referenceStep{query: "SELECT * FROM s", selectRows: true, columns: []string{"last_value", "log_cnt", "is_called"}, types: []string{"INT8", "INT8", "BOOL"}, rows: [][]any{{int64(20), log, true}}}
			populated.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
				var last, typedLog int64
				var called bool
				if err := rows.Scan(&last, &typedLog, &called); err != nil || rowIndex != 0 || last != 20 || typedLog != log || !called {
					t.Fatal("populated cache state", rowIndex, last, typedLog, called, err)
				}
			}
			referenceRunStep(t, p.ctx, a, backend.recorder, populated)
			sequenceProbeValue(t, p.ctx, b, "SELECT setval('s',50)", 50)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 3)
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 51)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE bounded AS SMALLINT INCREMENT BY 2 MINVALUE 2 MAXVALUE 5 START WITH 2 CACHE 1 NO CYCLE")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('bounded')", 2)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('bounded')", 4)
			want := controlsReferenceDiagnostic("2200H", `nextval: reached maximum value of sequence "bounded" (5)`)
			var value int64
			err := a.QueryRowContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), "SELECT nextval('bounded')").Scan(&value)
			referenceAssertDiagnostic(t, err, &want)
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE bounded CYCLE")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('bounded')", 2)
			if backend.pg {
				sequenceProbeExec(t, p.ctx, b, "ALTER SEQUENCE s INCREMENT BY 2")
				sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 62)
				sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE bounded_cycle AS SMALLINT INCREMENT BY 2 MINVALUE 2 MAXVALUE 5 START WITH 2 CACHE 10 CYCLE")
				for _, n := range []int64{2, 4, 2, 4, 2} {
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('bounded_cycle')", n)
				}
			}
			for _, tc := range []struct {
				name, ddl string
				max       int64
			}{
				{"typed", "CREATE SEQUENCE typed AS SMALLINT", 2147483647},
				{"explicit_bound", "CREATE SEQUENCE explicit_bound AS SMALLINT MAXVALUE 10", 10},
			} {
				sequenceProbeExec(t, p.ctx, a, tc.ddl)
				sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE "+tc.name+" AS INTEGER")
				var oid int64
				if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname=$1", tc.name).Scan(&oid); err != nil || oid <= 0 {
					t.Fatal("sequence identity", oid, err)
				}
				referenceRunStep(t, p.ctx, a, backend.recorder, referenceSelect("SELECT seqmax FROM pg_sequence WHERE seqrelid=$1::oid", []string{"seqmax"}, []string{"INT8"}, [][]any{{tc.max}}, oid))
			}
		})
	}
}

func TestReferenceSequenceTransactionalRestartDDL(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 1)
			sequenceProbeExec(t, p.ctx, a, "BEGIN")
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s RESTART WITH 100")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 100)
			sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
			sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 100)
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 2)
			sequenceProbeExec(t, p.ctx, a, "BEGIN")
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE rolled_back")
			sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
			want := controlsReferenceDiagnostic("42P01", `relation "rolled_back" does not exist`)
			want.Position = "16"
			var value int64
			err := b.QueryRowContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), "SELECT nextval('rolled_back')").Scan(&value)
			referenceAssertDiagnostic(t, err, &want)
			sequenceProbeExec(t, p.ctx, a, "BEGIN")
			sequenceProbeExec(t, p.ctx, a, "DROP SEQUENCE s")
			sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 3)
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s START WITH 7")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 4)
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s RESTART")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 7)
		})
	}
}

func TestReferenceSequenceHugeCacheArithmetic(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a := sequenceCoreConn(t, p.ctx, backend.db)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE huge CACHE 9223372036854775807")
			var oid int64
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='huge'").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal("huge sequence identity", oid, err)
			}
			referenceRunStep(t, p.ctx, a, backend.recorder, referenceSelect("SELECT seqcache FROM pg_sequence WHERE seqrelid=$1::oid", []string{"seqcache"}, []string{"INT8"}, [][]any{{int64(9223372036854775807)}}, oid))
			if backend.pg {
				// PostgreSQL reserves iteratively: inspect huge metadata, never draw.
				sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE bounded_cache CACHE 1000")
				sequenceProbeValue(t, p.ctx, a, "SELECT nextval('bounded_cache')", 1)
				sequenceProbeValue(t, p.ctx, a, "SELECT last_value FROM bounded_cache", 1000)
			} else {
				sequenceProbeValue(t, p.ctx, a, "SELECT nextval('huge')", 1)
				sequenceProbeValue(t, p.ctx, a, "SELECT last_value FROM huge", 9223372036854775807)
				sequenceProbeValue(t, p.ctx, a, "SELECT nextval('huge')", 2)
			}
		})
	}
}
