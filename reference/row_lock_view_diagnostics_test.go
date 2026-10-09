package reference_test

import (
	"database/sql"
	"strconv"
	"testing"
)

func TestReferenceRowLockViewDiagnostics(t *testing.T) {
	p := referenceViewFixture(t)
	for _, tc := range []struct{ name, body, suffix string }{
		{"grouped", "SELECT id FROM t GROUP BY id", "GROUP BY clause"},
		{"having_view", "SELECT count(*) AS n FROM t HAVING TRUE", "HAVING clause"},
		{"aggregate_view", "SELECT count(*) AS n FROM t", "aggregate functions"},
	} {
		p.step(t, referenceExec("CREATE VIEW "+tc.name+" AS "+tc.body, 0))
		for _, clause := range referenceRowClauses {
			for _, prepared := range []bool{false, true} {
				failure := referenceImplicitError("SELECT * FROM "+tc.name+" "+clause, referenceDiagnostic{Code: "0A000", Message: clause + " is not allowed with " + tc.suffix})
				failure.selectRows, failure.prepared, failure.errorStage = true, prepared, "execute"
				p.step(t, failure)
			}
		}
	}
	for i, clause := range referenceRowClauses {
		name := "stored_" + strconv.Itoa(i)
		p.step(t, referenceExec("CREATE VIEW "+name+" AS SELECT id,v FROM t "+clause, 0))
		for _, backend := range p.backends() {
			for _, prepared := range []bool{false, true} {
				reader, err := backend.db.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer reader.Rollback()
					failure := referenceImplicitError("SELECT v FROM "+name+" WHERE FALSE", referenceDiagnostic{Code: "25006", Message: "cannot execute SELECT in a read-only transaction"})
					failure.selectRows, failure.prepared, failure.errorStage = true, prepared, "execute"
					referenceRunStep(t, p.ctx, reader, backend.recorder, failure)
				}()
			}
		}
	}
	for index, sequence := range []string{"lock_sequence", "s"} {
		if index != 0 {
			p.step(t, referenceExec("DROP VIEW sequence_view", 0))
		}
		p.step(t, referenceExec("CREATE SEQUENCE "+sequence, 0))
		p.step(t, referenceExec("CREATE VIEW sequence_view AS SELECT last_value FROM "+sequence, 0))
		for _, clause := range referenceRowClauses {
			for _, prepared := range []bool{false, true} {
				failure := referenceImplicitError("SELECT last_value FROM sequence_view WHERE FALSE "+clause, referenceDiagnostic{Code: "42809", Message: `cannot lock rows in sequence "` + sequence + `"`})
				failure.selectRows, failure.prepared, failure.errorStage = true, prepared, "execute"
				p.step(t, failure)
				positive := referenceSelect("SELECT last_value FROM sequence_view", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}})
				positive.prepared = prepared
				p.step(t, positive)
			}
		}
	}
}
