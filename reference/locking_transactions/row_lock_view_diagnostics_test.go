package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockViewDiagnostics(t *testing.T) {
	p := referenceViewFixture(t)
	for _, tc := range []struct{ name, body, suffix string }{
		{"grouped", "SELECT id FROM t GROUP BY id", "GROUP BY clause"},
		{"having_view", "SELECT count(*) AS n FROM t HAVING TRUE", "HAVING clause"},
		{"aggregate_view", "SELECT count(*) AS n FROM t", "aggregate functions"},
	} {
		p.Step(t, fixture.Exec("CREATE VIEW "+tc.name+" AS "+tc.body, 0))
		for _, clause := range fixture.RowClauses {
			for _, prepared := range []bool{false, true} {
				failure := fixture.ImplicitError("SELECT * FROM "+tc.name+" "+clause, fixture.Diagnostic{Code: "0A000", Message: clause + " is not allowed with " + tc.suffix})
				failure.SelectRows, failure.Prepared, failure.ErrorStage = true, prepared, "execute"
				p.Step(t, failure)
			}
		}
	}
	for i, clause := range fixture.RowClauses {
		name := "stored_" + strconv.Itoa(i)
		p.Step(t, fixture.Exec("CREATE VIEW "+name+" AS SELECT id,v FROM t "+clause, 0))
		for _, backend := range p.Backends() {
			for _, prepared := range []bool{false, true} {
				reader, err := backend.DB.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer reader.Rollback()
					failure := fixture.ImplicitError("SELECT v FROM "+name+" WHERE FALSE", fixture.Diagnostic{Code: "25006", Message: "cannot execute SELECT in a read-only transaction"})
					failure.SelectRows, failure.Prepared, failure.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, reader, backend.Recorder, failure)
				}()
			}
		}
	}
	for index, sequence := range []string{"lock_sequence", "s"} {
		if index != 0 {
			p.Step(t, fixture.Exec("DROP VIEW sequence_view", 0))
		}
		p.Step(t, fixture.Exec("CREATE SEQUENCE "+sequence, 0))
		p.Step(t, fixture.Exec("CREATE VIEW sequence_view AS SELECT last_value FROM "+sequence, 0))
		for _, clause := range fixture.RowClauses {
			for _, prepared := range []bool{false, true} {
				failure := fixture.ImplicitError("SELECT last_value FROM sequence_view WHERE FALSE "+clause, fixture.Diagnostic{Code: "42809", Message: `cannot lock rows in sequence "` + sequence + `"`})
				failure.SelectRows, failure.Prepared, failure.ErrorStage = true, prepared, "execute"
				p.Step(t, failure)
				positive := fixture.Select("SELECT last_value FROM sequence_view", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}})
				positive.Prepared = prepared
				p.Step(t, positive)
			}
		}
	}
}
