package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestReferenceUpdateCoreRecursivePreflightACL(t *testing.T) {
	const reader = "update_preflight_reader"
	p := newReferenceScenario(t)
	for _, table := range []string{"empty", "rejected"} {
		p.step(t, referenceExec("CREATE TABLE "+table+"(a INT,f BOOL,n BIGINT,s TEXT)", 0))
	}
	p.step(t, referenceExec("INSERT INTO rejected VALUES(1,TRUE,2147483648,'oops')", 1))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			t.Run("acl_precedence", func(t *testing.T) {
				c, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				defer func() {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					queries := []string{"RESET SESSION AUTHORIZATION"}
					if backend.pg {
						queries = append(queries, "DROP OWNED BY "+reader)
					}
					queries = append(queries, "DROP ROLE "+reader)
					for _, q := range queries {
						if _, err := c.ExecContext(cleanup, q); err != nil {
							t.Error(q, err)
						}
					}
				}()
				if backend.pg {
					referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("DROP ROLE IF EXISTS "+reader, 0))
				}
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
						t.Error(err)
					}
				}()
				referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("CREATE ROLE "+reader, 0))
				if backend.pg {
					referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("GRANT USAGE ON SCHEMA public TO "+reader, 0))
				}
				referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
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
