//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceArithmeticInsertSourceContextualPositions(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE d(f BOOL)", 0))
			p.step(t, referenceExec("INSERT INTO d VALUES(TRUE)", 1))
			p.step(t, referenceExec("CREATE TABLE s(i INT)", 0))
			if populated {
				p.step(t, referenceExec("INSERT INTO s VALUES(7)", 1))
			}
			remaining := referenceSelect("SELECT f FROM d", []string{"f"}, []string{"BOOL"}, [][]any{{true}})
			for _, operand := range []struct {
				expr, typ string
				offset    int
			}{
				{"1+2", "integer", 0}, {"(1+2)", "integer", 1}, {"((1+2)*3)", "integer", 2}, {"-(1+2)", "integer", 0}, {"(1+2)::int4", "integer", 1}, {"(1+2)::bigint", "bigint", 1},
				{"CAST(1+2 AS bigint)", "bigint", 0}, {"int8(1+2)", "bigint", 0},
			} {
				for _, prefix := range []struct {
					text   string
					offset int
				}{{"", 0}, {"/*é*/ ", 6}} {
					for _, source := range []struct {
						before, after, code, message, hint string
						start                              int
					}{
						{"INSERT INTO d SELECT ", "", "42804", `column "f" is of type boolean but expression is of type ` + operand.typ, "You will need to rewrite or cast the expression.", 22},
						{"INSERT INTO d VALUES(TRUE),(", ") ORDER BY 1", "42804", "VALUES types boolean and " + operand.typ + " cannot be matched", "", 29},
						{"INSERT INTO d SELECT TRUE,", "", "42601", "INSERT has more expressions than target columns", "", 27},
						{"INSERT INTO d(f) SELECT TRUE,", "", "42601", "INSERT has more expressions than target columns", "", 30},
					} {
						query := prefix.text + source.before + operand.expr + source.after
						failure := referenceImplicitError(query, referenceDiagnostic{Code: source.code, Message: source.message, Hint: source.hint, Position: fmt.Sprint(source.start + operand.offset + prefix.offset)})
						failure.errorStage = "execute"
						p.step(t, failure)
						p.step(t, remaining)
						failure.errorStage = "prepare"
						p.step(t, referencePrepared(failure))
						p.step(t, remaining)
					}
				}
			}
			for _, tc := range []struct{ query, position, typ string }{
				{"INSERT INTO d SELECT i+1 FROM s", "22", "integer"},
				{"INSERT INTO d SELECT (i+1) FROM s", "23", "integer"},
				{"INSERT INTO d SELECT CAST(i+1 AS bigint) FROM s", "22", "bigint"},
				{"/*é*/ INSERT INTO d SELECT i+1 FROM s", "28", "integer"},
				{"/*é*/ INSERT INTO d SELECT (i+1) FROM s", "29", "integer"},
				{"/*é*/ INSERT INTO d SELECT CAST(i+1 AS bigint) FROM s", "28", "bigint"},
				{"INSERT INTO d SELECT (i+1)::bigint FROM s", "23", "bigint"},
				{"/*é*/ INSERT INTO d SELECT (i+1)::bigint FROM s", "29", "bigint"},
			} {
				failure := referenceImplicitError(tc.query, referenceDiagnostic{Code: "42804", Message: `column "f" is of type boolean but expression is of type ` + tc.typ, Hint: "You will need to rewrite or cast the expression.", Position: tc.position})
				failure.errorStage = "execute"
				p.step(t, failure)
				p.step(t, remaining)
				failure.errorStage = "prepare"
				p.step(t, referencePrepared(failure))
				p.step(t, remaining)
			}
			failure := referenceImplicitError("INSERT INTO d SELECT 1+TRUE", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer + boolean", Position: "23", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
			failure.errorStage = "execute"
			p.step(t, failure)
			failure.errorStage = "prepare"
			p.step(t, referencePrepared(failure))
			p.step(t, remaining)
		})
	}
}

func TestReferenceArithmeticInsertSourceLimitBoundary(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE d(f BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO d VALUES(TRUE)", 1))
	for _, tc := range []struct{ query, nativePosition, pgPosition string }{
		{"INSERT INTO d VALUES(TRUE),(1+2) LIMIT 1", "34", "29"},
		{"/*é*/ INSERT INTO d VALUES(TRUE),(1+2) LIMIT 1", "40", "35"},
	} {
		for _, backend := range []struct {
			db     referenceExecutor
			record *referenceRecorder
			want   referenceDiagnostic
		}{
			{p.memory, p.memoryRecord, referenceDiagnostic{Code: "0A000", Message: "INSERT suffix is unsupported", Position: tc.nativePosition}},
			{p.postgres, p.postgresRecord, referenceDiagnostic{Code: "42804", Message: "VALUES types boolean and integer cannot be matched", Position: tc.pgPosition}},
		} {
			failure := referenceImplicitError(tc.query, backend.want)
			failure.errorStage = "execute"
			referenceRunStep(t, p.ctx, backend.db, backend.record, failure)
			failure.errorStage = "prepare"
			referenceRunStep(t, p.ctx, backend.db, backend.record, referencePrepared(failure))
			referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SELECT f FROM d", []string{"f"}, []string{"BOOL"}, [][]any{{true}}))
		}
	}
}

func TestReferenceArithmeticInsertSourceTableExcessPositions(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE d(i INT)", 0))
			p.step(t, referenceExec("INSERT INTO d VALUES(42)", 1))
			p.step(t, referenceExec("CREATE TABLE s(i INT)", 0))
			if populated {
				p.step(t, referenceExec("INSERT INTO s VALUES(7)", 1))
			}
			remaining := referenceSelect("SELECT i FROM d", []string{"i"}, []string{"INT4"}, [][]any{{int64(42)}})
			for _, operand := range []struct {
				expr   string
				offset int
			}{
				{"i+1", 0}, {"(i+1)", 1}, {"(i+1)::bigint", 1}, {"CAST(i+1 AS bigint)", 0},
			} {
				for _, target := range []struct {
					text   string
					offset int
				}{{"", 0}, {"(i)", 3}} {
					for _, prefix := range []struct {
						text   string
						offset int
					}{{"", 0}, {"/*é*/ ", 6}} {
						query := prefix.text + "INSERT INTO d" + target.text + " SELECT i," + operand.expr + " FROM s"
						failure := referenceImplicitError(query, referenceDiagnostic{Code: "42601", Message: "INSERT has more expressions than target columns", Position: fmt.Sprint(24 + operand.offset + target.offset + prefix.offset)})
						failure.errorStage = "execute"
						p.step(t, failure)
						p.step(t, remaining)
						failure.errorStage = "prepare"
						p.step(t, referencePrepared(failure))
						p.step(t, remaining)
					}
				}
			}
		})
	}
}

func TestReferenceArithmeticInsertSourceSuccess(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE good(id INT,i INT,b BIGINT)", 0))
			insert := referenceExec("INSERT INTO good SELECT 1,7/2,-7%2", 1)
			insert.prepared = prepared
			p.step(t, insert)
			p.step(t, referenceSelect("SELECT id,i,b FROM good", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(3), int64(-1)}}))
		})
	}
}
