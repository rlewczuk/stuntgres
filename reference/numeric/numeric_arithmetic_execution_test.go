package reference_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func numericArithmeticAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE dest(id INT,n NUMERIC)", 0))
	run(fixture.Exec("INSERT INTO dest VALUES(99,99)", 1))
	sentinel := fixture.Select("SELECT id,n FROM dest ORDER BY id", []string{"id", "n"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(99), "99"}})
	for _, tc := range []struct{ expr, code, message string }{
		{"1::numeric/0", "22012", "division by zero"}, {"(-2)::numeric^0.5", "2201F", "a negative number raised to a non-integer power yields a complex result"}, {"1e131071::numeric*10", "22003", "value overflows numeric format"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared%t", tc.code, prepared), func(t *testing.T) {
				s := fixture.ImplicitError("INSERT INTO dest VALUES(1,0.1+0.2),(2,"+tc.expr+")", fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{b.DB, t}, b.Recorder, s)
				run(sentinel)
			})
		}
	}
	run(fixture.Exec("CREATE TABLE source(id INT,n NUMERIC)", 0))
	run(fixture.Exec("INSERT INTO source VALUES(1,2),(2,0)", 2))
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("INSERT INTO dest SELECT id,1::numeric/n FROM source ORDER BY id", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{b.DB, t}, b.Recorder, s)
		run(sentinel)
	}
	run(fixture.Exec("CREATE TABLE sized(id INT,n NUMERIC(3,1))", 0))
	run(fixture.Exec("INSERT INTO sized VALUES(99,9.9)", 1))
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("assignment overflow/prepared%t", prepared), func(t *testing.T) {
			s := fixture.ImplicitError("INSERT INTO sized VALUES(1,0.1+0.2),(2,99.9+0.1)", fixture.Diagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{b.DB, t}, b.Recorder, s)
		})
	}
	run(fixture.Select("SELECT id,n FROM sized ORDER BY id", []string{"id", "n"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(99), "9.9"}}))
	numericArithmeticDiagnosticRoutes(t, p, b, "SELECT 1::numeric/n FROM source ORDER BY id", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, false)
	writer, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec("BEGIN", 0))
	bad := fixture.ImplicitError("INSERT INTO dest VALUES(1,0.1+0.2),(2,1::numeric/0)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
	bad.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{writer, t}, b.Recorder, bad)
	fixture.RunStep(t, p.Ctx, reader, b.Recorder, sentinel)
	aborted := fixture.ImplicitError("SELECT n FROM dest", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
	aborted.SelectRows = true
	aborted.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, writer, b.Recorder, aborted)
	fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec("ROLLBACK", 0))
	fixture.RunStep(t, p.Ctx, reader, b.Recorder, sentinel)
}

func TestReferenceNumericArithmeticAtomicityAndCancellation(t *testing.T) {
	numericArithmeticBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		numericArithmeticAtomicity(t, p, b)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, q := range []string{"SELECT n+0.1 FROM dest", "INSERT INTO dest VALUES(1,0.1+0.2)"} {
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, prepared := range []bool{false, true} {
				var exec fixture.Executor = b.DB
				if prepared {
					exec = fixture.HeldStatement{b.DB, stmt, q}
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
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT n+0.1 FROM dest", []string{"?column?"}, []string{"NUMERIC"}, [][]any{{"99.1"}}))
		if !b.PG { // Native-only API, with the same arithmetic SQL and state contract.
			err := stuntgres.Restore(canceled, p.Native, strings.NewReader("CREATE TABLE replacement(n NUMERIC); INSERT INTO replacement VALUES(0.1+0.2)"))
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,n FROM dest", []string{"id", "n"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(99), "99"}}))
		}
	})
}
