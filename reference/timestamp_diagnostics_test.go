package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReferenceTimestampDiagnosticsAndAtomicity(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(v TIMESTAMP NOT NULL)", 0))
			for _, tc := range timestampBadInputs {
				q := "/*é*/ SELECT TIMESTAMP '" + tc.input + "' FROM t"
				position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
				step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position})
				step.selectRows = true
				step.errorStage = "prepare"
				run(referencePrepared(step))
			}
			run(referenceExec("INSERT INTO t VALUES('epoch')", 1))
			unchanged := referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
			for _, tc := range timestampBadInputs {
				t.Run(tc.input, func(t *testing.T) {
					for _, q := range []string{"INSERT INTO t VALUES ('" + tc.input + "')", "/*é*/ SELECT TIMESTAMP '" + tc.input + "' FROM t", "INSERT INTO t VALUES('2000-01-01'),('" + tc.input + "')"} {
						position := strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, "'"+tc.input)]) + 1)
						step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position})
						step.selectRows = strings.Contains(q, "SELECT")
						step.errorStage = "execute"
						run(step)
						if step.selectRows {
							execStep := step
							execStep.selectRows = false
							run(execStep)
						}
						run(unchanged)
						step.errorStage = "prepare"
						run(referencePrepared(step))
						run(unchanged)
					}
					for _, q := range []string{"INSERT INTO t VALUES($1)", "INSERT INTO t VALUES('2000-01-01'),($1)"} {
						step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint}, tc.input)
						pg := *step.diagnostic
						pg.Context = "unnamed portal parameter $1 = '...'"
						step.diagnostic = &pg
						step.errorStage = "execute"
						run(step)
						run(unchanged)
						run(referencePrepared(step))
						run(unchanged)
					}
				})
			}

		})
	}
}

func TestReferenceTimestampEmptyDiagnosticsAndNotNullTransaction(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			referenceTimestampExec(t, ctx, db, "CREATE TABLE empty(v TIMESTAMP)")
			for _, q := range []string{"SELECT TIMESTAMP 'nonsense' FROM empty", "/*é*/ SELECT TIMESTAMP 'nonsense' FROM empty"} {
				pos := strconv.Itoa(len([]rune(q[:strings.Index(q, "'")])) + 1)
				want := stuntgres.Error{Code: "22007", Message: `invalid input syntax for type timestamp: "nonsense"`, Position: pos}
				rows, e := db.QueryContext(ctx, q)
				if rows != nil {
					rows.Close()
					t.Fatal("failed query exposed cursor")
				}
				referenceTemporalDiagnostic(t, e, want)
				stmt, e := db.PrepareContext(ctx, q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed prepare exposed handle")
				}
				referenceTemporalDiagnostic(t, e, want)
			}
			for _, tc := range []struct{ p, token string }{{"+1", "+"}, {"1.5", "1.5"}, {"1e2", "1e2"}, {".5", ".5"}, {"1,2", ","}, {"2147483648", "2147483648"}} {
				q := "CREATE TABLE bad (v TIMESTAMP(" + tc.p + "))"
				_, e := db.ExecContext(ctx, q)
				referenceTemporalDiagnostic(t, e, stuntgres.Error{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: strconv.Itoa(strings.Index(q, tc.token) + 1)})
			}
			referenceTimestampExec(t, ctx, db, "CREATE TABLE t(v TIMESTAMP NOT NULL)")
			referenceTimestampExec(t, ctx, db, "INSERT INTO t VALUES('epoch')")
			_, e := db.ExecContext(ctx, "INSERT INTO t VALUES('2000-01-01'),(NULL)")
			referenceTemporalDiagnostic(t, e, stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"})
			tx, e := db.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			_, e = tx.ExecContext(ctx, "INSERT INTO t VALUES('2000-01-01'),('nonsense')")
			referenceTemporalDiagnostic(t, e, stuntgres.Error{Code: "22007", Message: `invalid input syntax for type timestamp: "nonsense"`, Position: "37"})
			_, e = tx.ExecContext(ctx, "INSERT INTO t VALUES('now')")
			referenceTemporalDiagnostic(t, e, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			if e = tx.Rollback(); e != nil {
				t.Fatal(e)
			}
			referenceTimestampQuery(t, ctx, db, backend.recorder, "SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
		})
	}
}

func TestReferenceTimestampDiagnosticModifiersAndInference(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(v TIMESTAMP)", 0))
			run(referenceExec("INSERT INTO t VALUES('epoch')", 1))
			conflict := referenceImplicitError("SELECT $1 FROM t WHERE v=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "timestamp without time zone versus text", Position: "8"})
			conflict.selectRows = true
			conflict.errorStage = "prepare"
			run(referencePrepared(conflict))
			for _, tc := range []struct{ q, code, msg, stage string }{
				{"CREATE TABLE bad (v TIMESTAMP(-1))", "42601", "syntax error at or near \"-\"", "prepare"},
				{`CREATE TABLE bad (v "timestamp"(-1))`, "22023", "TIMESTAMP(-1) precision must not be negative", "execute"},
				{"CREATE TABLE bad (v TIMESTAMP(+1))", "42601", "syntax error at or near \"+\"", "prepare"},
				{"CREATE TABLE bad (v TIMESTAMP(1.5))", "42601", "syntax error at or near \"1.5\"", "prepare"},
				{"CREATE TABLE bad (v TIMESTAMP(1e2))", "42601", "syntax error at or near \"1e2\"", "prepare"},
				{"CREATE TABLE bad (v TIMESTAMP(.5))", "42601", "syntax error at or near \".5\"", "prepare"},
			} {
				offset := strings.LastIndex(tc.q, "(") + 1
				if tc.code == "22023" {
					offset = strings.Index(tc.q, `"timestamp"`)
				}
				position := strconv.Itoa(utf8.RuneCountInString(tc.q[:offset]) + 1)
				step := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.msg, Position: position})
				step.errorStage = tc.stage
				run(referencePrepared(step))
				step.errorStage = "execute"
				run(step)
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			_, err := b.db.ExecContext(canceled, "INSERT INTO t VALUES('now')")
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}}))
		})
	}
}
