package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedIdentityStrictnessAndEffects(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s")
			q := "SELECT nextval('s')"
			literal, err := a.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer literal.Close()
			parameter, err := a.PrepareContext(p.Ctx, "SELECT nextval($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer parameter.Close()
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SCHEMA moved")
			defer fixture.SequenceProbeExec(t, p.Ctx, a, "DROP SCHEMA moved CASCADE")
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s SET SCHEMA moved")
			fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE moved.s RENAME TO renamed")
			held := fixture.HeldStatement{Executor: a, Statement: literal, SQL: q}
			missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
			missing.Position = "16"
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Step{Query: q, SelectRows: true, State: "42P01", Diagnostic: &missing, ErrorStage: "execute"})
			fixture.SequenceProbeExec(t, p.Ctx, a, "DROP SEQUENCE moved.renamed")
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s START 20")
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.SequenceInt64Step(fixture.Step{Query: q, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(20)}}}))
			paramHeld := fixture.HeldStatement{Executor: a, Statement: parameter, SQL: "SELECT nextval($1)"}
			fixture.RunStep(t, p.Ctx, paramHeld, backend.Recorder, fixture.SequenceInt64Step(fixture.Step{Query: "SELECT nextval($1)", Args: []any{"s"}, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(21)}}}))
			query := "SELECT nextval(NULL),setval('s',NULL),setval('s',1,NULL)"
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Step{Query: query, SelectRows: true, Columns: []string{"nextval", "setval", "setval"}, Types: []string{"INT8", "INT8", "INT8"}, Rows: [][]any{{nil, nil, nil}}})
			// Retain the original typed NULL scan as well as the raw recorder contract.
			rows, err := a.QueryContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			var nulls [3]sql.NullInt64
			if !rows.Next() {
				rows.Close()
				t.Fatal(rows.Err())
			}
			if err = rows.Scan(&nulls[0], &nulls[1], &nulls[2]); err != nil || nulls != ([3]sql.NullInt64{}) {
				rows.Close()
				t.Fatal(nulls, err)
			}
			if rows.Next() || rows.Err() != nil {
				rows.Close()
				t.Fatal("extra/error NULL row", rows.Err())
			}
			if err = rows.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT currval('s')", 21)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE TABLE t(v BIGINT NOT NULL,w BIGINT)")
			if _, err = a.ExecContext(p.Ctx, "INSERT INTO t VALUES(nextval('s'),currval('unused'))"); err == nil {
				t.Fatal("missing literal accepted")
			}
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT currval('s')", 21)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE unused")
			undefined := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "unused" is not yet defined in this session`)
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Step{Query: "INSERT INTO t VALUES(nextval('s'),currval('unused'))", State: "55000", Diagnostic: &undefined, ErrorStage: "execute"})
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT currval('s')", 22)
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Step{Query: "SELECT * FROM t", SelectRows: true, Columns: []string{"v", "w"}, Types: []string{"INT8", "INT8"}, Rows: [][]any{}})
			fixture.SequenceProbeValue(t, p.Ctx, a, q, 23)
		})
	}
}
