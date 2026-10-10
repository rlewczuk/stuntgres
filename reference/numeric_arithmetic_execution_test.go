package reference_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
)

func numericArithmeticAtomicity(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
	run(referenceExec("CREATE TABLE dest(id INT,n NUMERIC)", 0))
	run(referenceExec("INSERT INTO dest VALUES(99,99)", 1))
	sentinel := referenceSelect("SELECT id,n FROM dest ORDER BY id", []string{"id", "n"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(99), "99"}})
	for _, tc := range []struct{ expr, code, message string }{
		{"1::numeric/0", "22012", "division by zero"}, {"(-2)::numeric^0.5", "2201F", "a negative number raised to a non-integer power yields a complex result"}, {"1e131071::numeric*10", "22003", "value overflows numeric format"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared%t", tc.code, prepared), func(t *testing.T) {
				s := referenceImplicitError("INSERT INTO dest VALUES(1,0.1+0.2),(2,"+tc.expr+")", referenceDiagnostic{Code: tc.code, Message: tc.message})
				s.prepared = prepared
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, referenceArithmeticExecutor{b.db, t}, b.recorder, s)
				run(sentinel)
			})
		}
	}
	run(referenceExec("CREATE TABLE source(id INT,n NUMERIC)", 0))
	run(referenceExec("INSERT INTO source VALUES(1,2),(2,0)", 2))
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError("INSERT INTO dest SELECT id,1::numeric/n FROM source ORDER BY id", referenceDiagnostic{Code: "22012", Message: "division by zero"})
		s.prepared = prepared
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, referenceArithmeticExecutor{b.db, t}, b.recorder, s)
		run(sentinel)
	}
	run(referenceExec("CREATE TABLE sized(id INT,n NUMERIC(3,1))", 0))
	run(referenceExec("INSERT INTO sized VALUES(99,9.9)", 1))
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("assignment overflow/prepared%t", prepared), func(t *testing.T) {
			s := referenceImplicitError("INSERT INTO sized VALUES(1,0.1+0.2),(2,99.9+0.1)", referenceDiagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."})
			s.prepared = prepared
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, referenceArithmeticExecutor{b.db, t}, b.recorder, s)
		})
	}
	run(referenceSelect("SELECT id,n FROM sized ORDER BY id", []string{"id", "n"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(99), "9.9"}}))
	numericArithmeticDiagnosticRoutes(t, p, b, "SELECT 1::numeric/n FROM source ORDER BY id", referenceDiagnostic{Code: "22012", Message: "division by zero"}, false)
	writer, err := b.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := b.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	referenceRunStep(t, p.ctx, writer, b.recorder, referenceExec("BEGIN", 0))
	bad := referenceImplicitError("INSERT INTO dest VALUES(1,0.1+0.2),(2,1::numeric/0)", referenceDiagnostic{Code: "22012", Message: "division by zero"})
	bad.errorStage = "execute"
	referenceRunStep(t, p.ctx, referenceArithmeticExecutor{writer, t}, b.recorder, bad)
	referenceRunStep(t, p.ctx, reader, b.recorder, sentinel)
	aborted := referenceImplicitError("SELECT n FROM dest", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	aborted.selectRows = true
	aborted.errorStage = "execute"
	referenceRunStep(t, p.ctx, writer, b.recorder, aborted)
	referenceRunStep(t, p.ctx, writer, b.recorder, referenceExec("ROLLBACK", 0))
	referenceRunStep(t, p.ctx, reader, b.recorder, sentinel)
}

func TestReferenceNumericArithmeticAtomicityAndCancellation(t *testing.T) {
	numericArithmeticBackends(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		numericArithmeticAtomicity(t, p, b)
		canceled, cancel := context.WithCancel(p.ctx)
		cancel()
		for _, q := range []string{"SELECT n+0.1 FROM dest", "INSERT INTO dest VALUES(1,0.1+0.2)"} {
			stmt, err := b.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, prepared := range []bool{false, true} {
				var exec referenceExecutor = b.db
				if prepared {
					exec = referenceHeldStatement{b.db, stmt, q}
				}
				if strings.HasPrefix(q, "SELECT") {
					rows, err := exec.QueryContext(canceled, q)
					if rows != nil {
						rows.Close()
						t.Fatal("precanceled query exposed rows")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else {
					result, err := exec.ExecContext(canceled, q)
					if result != nil || !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled insert result=%v error=%v", result, err)
					}
				}
			}
			stmt.Close()
		}
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT n+0.1 FROM dest", []string{"?column?"}, []string{"NUMERIC"}, [][]any{{"99.1"}}))
		if !b.pg { // Native-only API, with the same arithmetic SQL and state contract.
			err := stuntgres.Restore(canceled, p.native, strings.NewReader("CREATE TABLE replacement(n NUMERIC); INSERT INTO replacement VALUES(0.1+0.2)"))
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT id,n FROM dest", []string{"id", "n"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(99), "99"}}))
		}
	})
}
