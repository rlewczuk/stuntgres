//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReferenceUpdateCoreRecursivePreflightRegression(t *testing.T) {
	const reader = "update_preflight_reader"
	roleReferenceCleanup(t, []string{reader})
	p := newReferencePair(t)
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
	for _, backend := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
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
			// Preserve the independently observed boundary: PostgreSQL simplifies this
			// FALSE predicate; native logical evaluation makes no short-circuit promise.
			// Both still skip row-dependent RHS casts once WHERE rejects a row.
			for _, table := range []string{"empty", "rejected"} {
				for _, clause := range []string{"SET a=CAST(n AS INTEGER) WHERE FALSE", "SET f=int4(s) IS NULL WHERE FALSE", "SET a=1 WHERE FALSE AND CAST(n AS INTEGER)=0"} {
					q := "UPDATE " + table + " " + clause
					for _, prepared := range []bool{false, true} {
						step := referenceExec(q, 0)
						if backend.name == "native" && table == "rejected" && strings.Contains(clause, "FALSE AND") {
							step = referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
						}
						step.prepared = prepared
						referenceRunStep(t, p.ctx, backend.db, backend.record, step)
					}
				}
			}
			referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SELECT a,f,n,s FROM rejected", []string{"a", "f", "n", "s"}, []string{"INT4", "BOOL", "INT8", "TEXT"}, [][]any{{int64(1), true, int64(2147483648), "oops"}}))
			t.Run("acl_precedence", func(t *testing.T) {
				c, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
						t.Error(err)
					}
				}()
				referenceRunStep(t, p.ctx, c, backend.record, referenceExec("CREATE ROLE "+reader, 0))
				if backend.name == "postgres" {
					referenceRunStep(t, p.ctx, c, backend.record, referenceExec("GRANT USAGE ON SCHEMA public TO "+reader, 0))
				}
				referenceRunStep(t, p.ctx, c, backend.record, referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
				for _, tc := range []struct {
					clause        string
					arg           any
					parameter     bool
					code, message string
				}{
					{clause: "SET a=1 WHERE a=CAST(2147483648 AS INTEGER)", code: "22003", message: "integer out of range"},
					{clause: "SET f=a=CAST(2147483648 AS INTEGER)", code: "22003", message: "integer out of range"},
					{clause: "SET a=CAST($1::BIGINT AS INTEGER)", arg: int64(2147483648), parameter: true, code: "22003", message: "integer out of range"},
					{clause: "SET a=CAST($1::BIGINT AS INTEGER)", arg: int64(7), parameter: true, code: "42501", message: "permission denied for table rejected"},
					{clause: "SET a=CAST(n AS INTEGER)", code: "42501", message: "permission denied for table rejected"},
				} {
					q := "UPDATE rejected " + tc.clause
					args := []any{}
					if tc.parameter {
						args = []any{tc.arg}
					}
					for _, prepared := range []bool{false, true} {
						t.Run(q+fmt.Sprint(prepared)+tc.code, func(t *testing.T) {
							var err error
							if prepared {
								stmt, e := c.PrepareContext(p.ctx, q)
								if e != nil {
									t.Fatal(e)
								}
								_, err = stmt.ExecContext(p.ctx, args...)
								stmt.Close()
							} else {
								_, err = c.ExecContext(p.ctx, q, args...)
							}
							want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
							referenceAssertDiagnostic(t, err, &want)
						})
					}
				}
			})
		})
	}
}
