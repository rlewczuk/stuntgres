package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertImplicitShortPreparedIntegerRange(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			route := func(s referenceStep) referenceStep {
				s.nonContext = nonContext
				return s
			}
			p.step(t, route(referenceExec("CREATE TABLE t(id INT NOT NULL,label TEXT)", 0)))
			p.step(t, route(referenceExec("INSERT INTO t VALUES(99)", 1)))
			for _, query := range []string{
				"INSERT INTO t VALUES (2147483648)",
				"INSERT INTO t VALUES (20),(-2147483649)",
				"INSERT INTO t VALUES (NULL),(2147483648)",
			} {
				step := referenceImplicitError(query, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
				step.prepared, step.errorStage = true, "execute"
				p.step(t, route(step))
				p.step(t, route(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), nil}})))
			}
		})
	}
}

func TestReferenceInsertImplicitShortUnicodePosition(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			route := func(s referenceStep) referenceStep {
				s.nonContext = nonContext
				return s
			}
			p.step(t, route(referenceExec("CREATE TABLE t(id INT,label TEXT)", 0)))
			query := "/* é */ INSERT INTO t VALUES(1),('bad')"
			step := referenceImplicitError(query, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: referencePosition(utf8.RuneCountInString(query[:strings.Index(query, "'bad'")]) + 1)})
			step.errorStage = "execute"
			p.step(t, route(step))
			step.errorStage = "prepare"
			p.step(t, route(referencePrepared(step)))
			p.step(t, route(referenceSelect("SELECT * FROM t", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{})))
		})
	}
}

func TestReferenceInsertImplicitShortBoundariesAndCancellation(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			route := func(s referenceStep) referenceStep {
				s.nonContext = nonContext
				return s
			}
			p.step(t, route(referenceExec("CREATE TABLE t(id INT,label TEXT)", 0)))
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					query := "INSERT INTO t VALUES(1) RETURNING id"
					if b.pg {
						// PostgreSQL accepts this shape. Roll back its real returned row
						// so subsequent no-mutation cancellation checks remain identical.
						tx, err := b.db.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						if nonContext {
							referenceRunStep(t, p.ctx, tx, b.recorder, route(referenceExec(query, 1)))
						} else {
							referenceRunStep(t, p.ctx, tx, b.recorder, route(referenceSelect(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})))
						}
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					} else {
						step := referenceImplicitError(query, referenceDiagnostic{Code: "0A000", Message: "INSERT suffix is unsupported", Position: referencePosition(strings.Index(query, "RETURNING") + 1)})
						step.errorStage = "execute"
						referenceRunStep(t, p.ctx, b.db, b.recorder, route(step))
					}
					for _, prepared := range []bool{false, true} {
						for _, step := range []referenceStep{
							referenceImplicitError("INSERT INTO t VALUES()", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "22"}),
							referenceImplicitError("INSERT INTO t VALUES($2)", referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}),
						} {
							// Without arguments pq uses the simple-query route: the
							// absent parameter is rejected before prepared inference.
							if b.pg && !prepared && step.state == "42P18" {
								step = referenceImplicitError(step.query, referenceDiagnostic{Code: "42P02", Message: "there is no parameter $2", Position: "22"})
							}
							step.prepared = prepared
							step.errorStage = "execute"
							if prepared {
								step.errorStage = "prepare"
							}
							referenceRunStep(t, p.ctx, b.db, b.recorder, route(step))
						}
					}
					ctx, cancel := context.WithCancel(p.ctx)
					cancel()
					result, err := b.db.ExecContext(ctx, "INSERT INTO t VALUES(1)")
					if result != nil || !errors.Is(err, context.Canceled) {
						t.Fatalf("direct cancellation: result=%v err=%v", result, err)
					}
					var stmt *sql.Stmt
					if nonContext {
						stmt, err = b.db.Prepare("INSERT INTO t VALUES($1)")
					} else {
						stmt, err = b.db.PrepareContext(p.ctx, "INSERT INTO t VALUES($1)")
					}
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					result, err = stmt.ExecContext(ctx, 1)
					if result != nil || !errors.Is(err, context.Canceled) {
						t.Fatalf("held cancellation: result=%v err=%v", result, err)
					}
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
					empty := referenceSelect("SELECT * FROM t", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{})
					empty.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						referenceInsertShortMetadata(t, rows, b.pg, empty.types, []bool{true, true})
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, route(empty))
				})
			}
		})
	}
}
