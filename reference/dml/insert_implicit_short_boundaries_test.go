package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertImplicitShortPreparedIntegerRange(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			route := func(s fixture.Step) fixture.Step {
				s.NonContext = nonContext
				return s
			}
			p.Step(t, route(fixture.Exec("CREATE TABLE t(id INT NOT NULL,label TEXT)", 0)))
			p.Step(t, route(fixture.Exec("INSERT INTO t VALUES(99)", 1)))
			for _, query := range []string{
				"INSERT INTO t VALUES (2147483648)",
				"INSERT INTO t VALUES (20),(-2147483649)",
				"INSERT INTO t VALUES (NULL),(2147483648)",
			} {
				step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
				step.Prepared, step.ErrorStage = true, "execute"
				p.Step(t, route(step))
				p.Step(t, route(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), nil}})))
			}
		})
	}
}

func TestReferenceInsertImplicitShortUnicodePosition(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			route := func(s fixture.Step) fixture.Step {
				s.NonContext = nonContext
				return s
			}
			p.Step(t, route(fixture.Exec("CREATE TABLE t(id INT,label TEXT)", 0)))
			query := "/* é */ INSERT INTO t VALUES(1),('bad')"
			step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: fixture.Position(utf8.RuneCountInString(query[:strings.Index(query, "'bad'")]) + 1)})
			step.ErrorStage = "execute"
			p.Step(t, route(step))
			step.ErrorStage = "prepare"
			p.Step(t, route(fixture.Prepared(step)))
			p.Step(t, route(fixture.Select("SELECT * FROM t", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{})))
		})
	}
}

func TestReferenceInsertImplicitShortBoundariesAndCancellation(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			route := func(s fixture.Step) fixture.Step {
				s.NonContext = nonContext
				return s
			}
			p.Step(t, route(fixture.Exec("CREATE TABLE t(id INT,label TEXT)", 0)))
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					query := "INSERT INTO t VALUES(1) RETURNING id"
					tx, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if nonContext {
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, route(fixture.Exec(query, 1)))
					} else {
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, route(fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})))
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					for _, prepared := range []bool{false, true} {
						for _, step := range []fixture.Step{
							fixture.ImplicitError("INSERT INTO t VALUES()", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "22"}),
							fixture.ImplicitError("INSERT INTO t VALUES($2)", fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}),
						} {
							// Without arguments pq uses the simple-query route: the
							// absent parameter is rejected before prepared inference.
							if b.PG && !prepared && step.State == "42P18" {
								step = fixture.ImplicitError(step.Query, fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $2", Position: "22"})
							}
							step.Prepared = prepared
							step.ErrorStage = "execute"
							if prepared {
								step.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, route(step))
						}
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					result, err := b.DB.ExecContext(ctx, "INSERT INTO t VALUES(1)")
					if result != nil || !errors.Is(err, context.Canceled) {
						t.Fatalf("direct cancellation: result=%v err=%v", result, err)
					}
					var stmt *sql.Stmt
					if nonContext {
						stmt, err = b.DB.Prepare("INSERT INTO t VALUES($1)")
					} else {
						stmt, err = b.DB.PrepareContext(p.Ctx, "INSERT INTO t VALUES($1)")
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
					empty := fixture.Select("SELECT * FROM t", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{})
					empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						referenceInsertShortMetadata(t, rows, b.PG, empty.Types, []bool{true, true})
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, route(empty))
				})
			}
		})
	}
}
