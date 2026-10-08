package reference_test

import (
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceInsertSelectFailedRestoreTransactionEligibility(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			run := func(s referenceStep) { insertSelectRun(t, ctx, backend.db, backend.recorder, s) }
			for _, s := range []referenceStep{referenceExec("CREATE TABLE t(i INT)", 0), referenceExec("INSERT INTO t VALUES(9)", 1), referenceExec("CREATE TABLE s(i INT)", 0), referenceExec("INSERT INTO s VALUES(1)", 1)} {
				run(s)
			}
			tx, err := backend.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			insertSelectRun(t, ctx, tx, backend.recorder, referenceExec("INSERT INTO t VALUES(7)", 1))
			query := "INSERT INTO t SELECT i FROM s"
			producer, err := tx.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer producer.Close()
			if !backend.pg {
				err := stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE t(i INT); INSERT INTO t SELECT $1"))
				referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore INSERT query sources are unsupported"})
			}
			insertSelectRun(t, ctx, referenceHeldStatement{tx, producer, query}, backend.recorder, referenceExec(query, 1))
			run(referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(7)}, {int64(9)}}))
		})
	}
}
