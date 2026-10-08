package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceUpdateCoreRecursivePreflightRegression(t *testing.T) {
	p := newReferenceScenario(t)
	for _, table := range []string{"empty", "rejected"} {
		p.step(t, referenceExec("CREATE TABLE "+table+"(a INT,f BOOL,n BIGINT,s TEXT)", 0))
	}
	p.step(t, referenceExec("INSERT INTO rejected VALUES(1,TRUE,2147483648,'oops')", 1))
	cases := []struct {
		name, clause  string
		bad, good     any
		parameter     bool
		code, message string
	}{
		{name: "where", clause: "SET a=1 WHERE a=CAST(2147483648 AS INTEGER)"},
		{name: "where_rejected_comparison", clause: "SET a=1 WHERE f IS FALSE AND a=CAST(2147483648 AS INTEGER)"},
		{name: "where_rejected_null", clause: "SET a=1 WHERE f IS FALSE AND CAST(2147483648 AS INTEGER) IS NULL"},
		{name: "where_rejected_truth", clause: "SET a=1 WHERE f IS FALSE AND bool(CAST(2147483648 AS INTEGER)) IS TRUE"},
		{name: "where_rejected_distinct", clause: "SET a=1 WHERE f IS FALSE AND a IS DISTINCT FROM CAST(2147483648 AS INTEGER)"},
		{name: "frame_where_rejected", clause: "SET a=1 WHERE f IS FALSE AND a=CAST($1::BIGINT AS INTEGER)", bad: int64(2147483648), good: int64(7), parameter: true},
		{name: "rhs_comparison", clause: "SET f=a=CAST(2147483648 AS INTEGER)"},
		{name: "rhs_second_assignment", clause: "SET a=1,f=a=CAST(2147483648 AS INTEGER)"},
		{name: "rhs_nested_cast", clause: "SET f=a=CAST(CAST(2147483648 AS BIGINT) AS INTEGER)"},
		{name: "rhs_logical", clause: "SET f=(a=CAST(2147483648 AS INTEGER)) AND TRUE"},
		{name: "rhs_null", clause: "SET f=CAST(2147483648 AS INTEGER) IS NULL"},
		{name: "rhs_truth", clause: "SET f=bool(CAST(2147483648 AS INTEGER)) IS TRUE"},
		{name: "rhs_distinct", clause: "SET f=a IS DISTINCT FROM CAST(2147483648 AS INTEGER)"},
		{name: "rhs_null_count", clause: "SET a=num_nulls(CAST(2147483648 AS INTEGER))"},
		{name: "frame_rhs", clause: "SET a=CAST($1::BIGINT AS INTEGER)", bad: int64(2147483648), good: int64(7), parameter: true},
		{name: "frame_where", clause: "SET a=1 WHERE a=CAST($1::BIGINT AS INTEGER)", bad: int64(2147483648), good: int64(7), parameter: true},
		{name: "frame_text", clause: "SET f=int4($1::TEXT) IS NOT NULL", bad: "oops", good: "7", parameter: true, code: "22P02", message: `invalid input syntax for type integer: "oops"`},
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, table := range []string{"empty", "rejected"} {
				for _, tc := range cases {
					t.Run(table+"/"+tc.name, func(t *testing.T) {
						q := "UPDATE " + table + " " + tc.clause
						if table == "rejected" && !strings.Contains(tc.clause, " WHERE ") {
							q += " WHERE FALSE"
						}
						want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "integer out of range"}
						if tc.code != "" {
							want.Code, want.Message = tc.code, tc.message
						}
						args := []any{}
						if tc.parameter {
							args = []any{tc.bad}
						}
						t.Run("direct", func(t *testing.T) {
							result, err := backend.db.ExecContext(p.ctx, q, args...)
							if result != nil {
								t.Fatal("conversion exposed successful result", result, err)
							}
							referenceAssertDiagnostic(t, err, &want)
						})
						stmt, err := backend.db.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal("conversion failed Prepare", err)
						}
						defer stmt.Close()
						frames := [][]any{args}
						if tc.parameter {
							frames = [][]any{{tc.bad}, {tc.good}, {nil}, {tc.bad}, {tc.good}}
						}
						for index, frame := range frames {
							t.Run(fmt.Sprintf("retained_frame_%d", index), func(t *testing.T) {
								result, err := stmt.ExecContext(p.ctx, frame...)
								bad := !tc.parameter || index == 0 || index == 3
								if bad {
									if result != nil {
										t.Fatal("conversion exposed successful result", result, err)
									}
									referenceAssertDiagnostic(t, err, &want)
								} else {
									if err != nil {
										t.Fatal(err)
									}
									referenceLockAffected(t, result, 0)
								}
							})
						}
					})
				}
			}
		})
	}
}
