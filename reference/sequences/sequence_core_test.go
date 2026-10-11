package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceDefaultsPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, c, "CREATE SEQUENCE s")
			q := "SELECT nextval('s')"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, n := range []int64{1, 2} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Step{Query: q, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{n}}})
			}
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT currval('s')", 2)
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT lastval()", 2)
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Step{Query: "SELECT nextval(NULL),currval(NULL),setval(NULL,42),setval('s',NULL),setval('s',42,NULL)", SelectRows: true, Columns: []string{"nextval", "currval", "setval", "setval", "setval"}, Types: []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, Rows: [][]any{{nil, nil, nil, nil, nil}}})
			fixture.SequenceProbeValue(t, p.Ctx, c, q, 3)
		})
	}
}

func TestReferenceSequenceSessionsSetvalRollback(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s")
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE t")
			for _, tc := range []struct{ q, msg string }{{"SELECT currval('s')", `currval of sequence "s" is not yet defined in this session`}, {"SELECT lastval()", "lastval is not yet defined in this session"}} {
				want := fixture.ControlsReferenceDiagnostic("55000", tc.msg)
				var value int64
				err := a.QueryRowContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), tc.q).Scan(&value)
				fixture.AssertDiagnostic(t, err, &want)
			}
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 1)
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 2)
			for _, tc := range []struct {
				q string
				n int64
			}{{"SELECT currval('s')", 1}, {"SELECT setval('s',42)", 42}, {"SELECT currval('s')", 42}, {"SELECT nextval('s')", 43}, {"SELECT setval('s',42,false)", 42}, {"SELECT currval('s')", 43}, {"SELECT nextval('s')", 42}, {"SELECT setval('t',80,true)", 80}, {"SELECT lastval()", 42}} {
				fixture.SequenceProbeValue(t, p.Ctx, a, tc.q, tc.n)
			}
			fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 43)
			fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 44)
			fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT setval('s',90,false)", 90)
			fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 90)
		})
	}
}

func TestReferenceSequenceCacheBoundsRow(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s CACHE 10")
			initial := fixture.Step{Query: "SELECT * FROM s", SelectRows: true, Columns: []string{"last_value", "log_cnt", "is_called"}, Types: []string{"INT8", "INT8", "BOOL"}, Rows: [][]any{{int64(1), int64(0), false}}}
			initial.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
				var last, log int64
				var called bool
				if err := rows.Scan(&last, &log, &called); err != nil || rowIndex != 0 || last != 1 || log != 0 || called {
					t.Fatal("initial cache state", rowIndex, last, log, called, err)
				}
			}
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, initial)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 1)
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 11)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 2)
			log := int64(0)
			if backend.PG {
				log = 22
			}
			populated := fixture.Step{Query: "SELECT * FROM s", SelectRows: true, Columns: []string{"last_value", "log_cnt", "is_called"}, Types: []string{"INT8", "INT8", "BOOL"}, Rows: [][]any{{int64(20), log, true}}}
			populated.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
				var last, typedLog int64
				var called bool
				if err := rows.Scan(&last, &typedLog, &called); err != nil || rowIndex != 0 || last != 20 || typedLog != log || !called {
					t.Fatal("populated cache state", rowIndex, last, typedLog, called, err)
				}
			}
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, populated)
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT setval('s',50)", 50)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 3)
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 51)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE bounded AS SMALLINT INCREMENT BY 2 MINVALUE 2 MAXVALUE 5 START WITH 2 CACHE 1 NO CYCLE")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('bounded')", 2)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('bounded')", 4)
			want := fixture.ControlsReferenceDiagnostic("2200H", `nextval: reached maximum value of sequence "bounded" (5)`)
			var value int64
			err := a.QueryRowContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), "SELECT nextval('bounded')").Scan(&value)
			fixture.AssertDiagnostic(t, err, &want)
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE bounded CYCLE")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('bounded')", 2)
			if backend.PG {
				fixture.SequenceProbeExec(t, p.Ctx, b, "ALTER SEQUENCE s INCREMENT BY 2")
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 62)
				fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE bounded_cycle AS SMALLINT INCREMENT BY 2 MINVALUE 2 MAXVALUE 5 START WITH 2 CACHE 10 CYCLE")
				for _, n := range []int64{2, 4, 2, 4, 2} {
					fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('bounded_cycle')", n)
				}
			}
			for _, tc := range []struct {
				name, ddl string
				max       int64
			}{
				{"typed", "CREATE SEQUENCE typed AS SMALLINT", 2147483647},
				{"explicit_bound", "CREATE SEQUENCE explicit_bound AS SMALLINT MAXVALUE 10", 10},
			} {
				fixture.SequenceProbeExec(t, p.Ctx, a, tc.ddl)
				fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE "+tc.name+" AS INTEGER")
				var oid int64
				if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1", tc.name).Scan(&oid); err != nil || oid <= 0 {
					t.Fatal("sequence identity", oid, err)
				}
				fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Select("SELECT seqmax FROM pg_sequence WHERE seqrelid=$1::oid", []string{"seqmax"}, []string{"INT8"}, [][]any{{tc.max}}, oid))
			}
		})
	}
}

func TestReferenceSequenceTransactionalRestartDDL(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 1)
			fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s RESTART WITH 100")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 100)
			fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT currval('s')", 100)
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 2)
			fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE rolled_back")
			fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "rolled_back" does not exist`)
			want.Position = "16"
			var value int64
			err := b.QueryRowContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), "SELECT nextval('rolled_back')").Scan(&value)
			fixture.AssertDiagnostic(t, err, &want)
			fixture.SequenceProbeExec(t, p.Ctx, a, "BEGIN")
			fixture.SequenceProbeExec(t, p.Ctx, a, "DROP SEQUENCE s")
			fixture.SequenceProbeExec(t, p.Ctx, a, "ROLLBACK")
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 3)
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s START WITH 7")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 4)
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s RESTART")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 7)
		})
	}
}

func TestReferenceSequenceHugeCacheArithmetic(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE huge CACHE 9223372036854775807")
			var oid int64
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='huge'").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal("huge sequence identity", oid, err)
			}
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Select("SELECT seqcache FROM pg_sequence WHERE seqrelid=$1::oid", []string{"seqcache"}, []string{"INT8"}, [][]any{{int64(9223372036854775807)}}, oid))
			if backend.PG {
				// PostgreSQL reserves iteratively: inspect huge metadata, never draw.
				fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE bounded_cache CACHE 1000")
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('bounded_cache')", 1)
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT last_value FROM bounded_cache", 1000)
			} else {
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('huge')", 1)
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT last_value FROM huge", 9223372036854775807)
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('huge')", 2)
			}
		})
	}
}
