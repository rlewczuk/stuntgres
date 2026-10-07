//go:build reference

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
				// REL18 plans expanded views at Bind, not Describe Statement. Native common
				// Describe already performs structural legality. Preserve both phase records:
				// this is an explicitly specified protocol distinction, not blanket parity.
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, failure)
				if prepared {
					failure.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
			}
		}
	}
	for i, clause := range referenceRowClauses {
		name := "stored_" + strconv.Itoa(i)
		p.step(t, referenceExec("CREATE VIEW "+name+" AS SELECT id,v FROM t "+clause, 0))
		for _, backend := range []struct {
			db     *sql.DB
			record *referenceRecorder
		}{{p.postgres, p.postgresRecord}, {p.memory, p.memoryRecord}} {
			reader, err := backend.db.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer reader.Rollback()
				failure := referenceImplicitError("SELECT v FROM "+name+" WHERE FALSE", referenceDiagnostic{Code: "25006", Message: "cannot execute SELECT " + clause + " in a read-only transaction"})
				failure.selectRows, failure.prepared, failure.errorStage = true, true, "execute"
				referenceRunStep(t, p.ctx, reader, backend.record, failure)
			}()
		}
	}
	p.step(t, referenceExec("CREATE SEQUENCE lock_sequence", 0))
	p.step(t, referenceExec("CREATE VIEW sequence_view AS SELECT last_value FROM lock_sequence", 0))
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			failure := referenceImplicitError("SELECT last_value FROM sequence_view WHERE FALSE "+clause, referenceDiagnostic{Code: "42809", Message: `cannot lock rows in sequence "lock_sequence"`})
			failure.selectRows, failure.prepared, failure.errorStage = true, prepared, "execute"
			p.step(t, failure)
		}
	}
}

func TestReferenceRowLockCatalogControl(t *testing.T) {
	p := referenceViewFixture(t)
	// PostgreSQL's pg_class is physically lockable. Native catalog relations are
	// synthesized and deliberately have a separate 0A000 boundary, never fake IDs.
	a, b := referenceLockConnections(t, p.ctx, p.postgres)
	var oid int64
	if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='t' AND relnamespace='public'::regnamespace").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 {
		t.Fatal(oid)
	}
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			holder, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer holder.Rollback()
				query := "SELECT oid FROM pg_catalog.pg_class WHERE oid=" + strconv.FormatInt(oid, 10) + " " + clause
				step := referenceSelect(query, []string{"oid"}, []string{"OID"}, [][]any{{oid}})
				step.prepared = prepared
				referenceRunStep(t, p.ctx, holder, p.postgresRecord, step)
				referenceRunStep(t, p.ctx, b, p.postgresRecord, referenceSelect("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			}()
		}
	}
}
