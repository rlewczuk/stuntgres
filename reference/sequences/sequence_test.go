package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceIndependentMatrix(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			native, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			step := func(s fixture.Step) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, native, backend.Recorder, s)
			}
			scalar := func(q, label string, want int64, prepared bool) {
				step(fixture.Step{Query: q, Prepared: prepared, SelectRows: true, Columns: []string{label}, Types: []string{"INT8"}, Rows: [][]any{{want}}})
			}
			step(fixture.Step{Query: "CREATE SEQUENCE s"})
			before := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "s" is not yet defined in this session`)
			step(fixture.Step{Query: "SELECT currval('s')", State: "55000", Diagnostic: &before, ErrorStage: "execute"})
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
			step(fixture.Step{Query: "SELECT nextval(NULL)", SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{nil}}})
			scalar("SELECT currval('s')", "currval", 7, false)
			step(fixture.Step{Query: "CREATE TABLE t(v BIGINT)"})
			step(fixture.Step{Query: "INSERT INTO t VALUES(nextval('s'))", Affected: 1})
			step(fixture.Step{Query: "SELECT nextval('s') FROM t", SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(9)}}})
			step(fixture.Step{Query: "CREATE SEQUENCE bounded AS SMALLINT INCREMENT BY 2 MINVALUE 2 MAXVALUE 5 START WITH 2 CACHE 1 CYCLE"})
			for _, v := range []int64{2, 4, 2} {
				scalar("SELECT nextval('bounded')", "nextval", v, true)
			}
			step(fixture.Step{Query: "ALTER SEQUENCE bounded NO CYCLE"})
			scalar("SELECT nextval('bounded')", "nextval", 4, false)
			limit := fixture.ControlsReferenceDiagnostic("2200H", `nextval: reached maximum value of sequence "bounded" (5)`)
			step(fixture.Step{Query: "SELECT nextval('bounded')", State: "2200H", Diagnostic: &limit, ErrorStage: "execute"})
			step(fixture.Step{Query: "CREATE SEQUENCE descending AS SMALLINT INCREMENT -1"})
			scalar("SELECT nextval('descending')", "nextval", -1, false)
			step(fixture.Step{Query: "CREATE SCHEMA moved"})
			defer fixture.SequenceProbeExec(t, p.Ctx, native, "DROP SCHEMA moved CASCADE")
			step(fixture.Step{Query: "ALTER SEQUENCE descending SET SCHEMA moved"})
			step(fixture.Step{Query: "ALTER SEQUENCE moved.descending RENAME TO renamed"})
			scalar("SELECT nextval('moved.renamed')", "nextval", -2, false)
			step(fixture.Step{Query: "CREATE TABLE linked(a INT,b INT,c INT)"})
			step(fixture.Step{Query: "CREATE SEQUENCE owned OWNED BY linked.c"})
			step(fixture.Step{Query: "ALTER TABLE linked DROP COLUMN b"})
			scalar("SELECT nextval('owned')", "nextval", 1, false)
			step(fixture.Step{Query: "ALTER TABLE linked DROP COLUMN c"})
			missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "owned" does not exist`)
			missing.Position = "16"
			step(fixture.Step{Query: "SELECT nextval('owned')", State: "42P01", Diagnostic: &missing, ErrorStage: "execute"})
			// Continue the same independently specified stream on a physical peer.
			a := native
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 10)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT currval('s')", 9)
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var v int64
			if err = tx.QueryRowContext(p.Ctx, "SELECT nextval('s')").Scan(&v); err != nil || v != 11 {
				t.Fatal(v, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 12)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE TEMP SEQUENCE s START 100")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 100)
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 13)
			held, err := b.PrepareContext(p.Ctx, "SELECT nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			fixture.SequenceProbeExec(t, p.Ctx, b, "ALTER SEQUENCE public.s RENAME TO identity")
			err = held.QueryRowContext(p.Ctx).Scan(&v)
			heldMissing := fixture.ControlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
			heldMissing.Position = "16"
			fixture.AssertDiagnostic(t, err, &heldMissing)
			parameter, err := b.PrepareContext(p.Ctx, "SELECT nextval($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer parameter.Close()
			if err = parameter.QueryRowContext(p.Ctx, "identity").Scan(&v); err != nil || v != 14 {
				t.Fatal("parameter identity must not observe allocation from failed held literal", v, err)
			}
		})
	}
}
