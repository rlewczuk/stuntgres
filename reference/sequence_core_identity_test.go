package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSequencePreparedIdentityStrictnessAndEffects(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a := sequenceCoreConn(t, p.ctx, backend.db)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s")
			q := "SELECT nextval('s')"
			literal, err := a.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer literal.Close()
			parameter, err := a.PrepareContext(p.ctx, "SELECT nextval($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer parameter.Close()
			sequenceProbeExec(t, p.ctx, a, "CREATE SCHEMA moved")
			defer sequenceProbeExec(t, p.ctx, a, "DROP SCHEMA moved CASCADE")
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s SET SCHEMA moved")
			sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE moved.s RENAME TO renamed")
			held := referenceHeldStatement{referenceExecutor: a, statement: literal, query: q}
			missing := controlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
			missing.Position = "16"
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceStep{query: q, selectRows: true, state: "42P01", diagnostic: &missing, errorStage: "execute"})
			sequenceProbeExec(t, p.ctx, a, "DROP SEQUENCE moved.renamed")
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s START 20")
			referenceRunStep(t, p.ctx, held, backend.recorder, sequenceInt64Step(referenceStep{query: q, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(20)}}}))
			paramHeld := referenceHeldStatement{referenceExecutor: a, statement: parameter, query: "SELECT nextval($1)"}
			referenceRunStep(t, p.ctx, paramHeld, backend.recorder, sequenceInt64Step(referenceStep{query: "SELECT nextval($1)", args: []any{"s"}, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(21)}}}))
			query := "SELECT nextval(NULL),setval('s',NULL),setval('s',1,NULL)"
			referenceRunStep(t, p.ctx, a, backend.recorder, referenceStep{query: query, selectRows: true, columns: []string{"nextval", "setval", "setval"}, types: []string{"INT8", "INT8", "INT8"}, rows: [][]any{{nil, nil, nil}}})
			// Retain the original typed NULL scan as well as the raw recorder contract.
			rows, err := a.QueryContext(p.ctx, query)
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
			sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 21)
			sequenceProbeExec(t, p.ctx, a, "CREATE TABLE t(v BIGINT NOT NULL,w BIGINT)")
			if _, err = a.ExecContext(p.ctx, "INSERT INTO t VALUES(nextval('s'),currval('unused'))"); err == nil {
				t.Fatal("missing literal accepted")
			}
			sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 21)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE unused")
			undefined := controlsReferenceDiagnostic("55000", `currval of sequence "unused" is not yet defined in this session`)
			referenceRunStep(t, p.ctx, a, backend.recorder, referenceStep{query: "INSERT INTO t VALUES(nextval('s'),currval('unused'))", state: "55000", diagnostic: &undefined, errorStage: "execute"})
			sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 22)
			referenceRunStep(t, p.ctx, a, backend.recorder, referenceStep{query: "SELECT * FROM t", selectRows: true, columns: []string{"v", "w"}, types: []string{"INT8", "INT8"}, rows: [][]any{}})
			sequenceProbeValue(t, p.ctx, a, q, 23)
		})
	}
}
