package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestReferenceInsertSelectCapturedConstraintOutput(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			conn, err := backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET bytea_output")
			run := func(s referenceStep) { insertSelectRun(t, ctx, conn, backend.recorder, s) }
			for _, s := range []referenceStep{referenceExec("CREATE TABLE s(v BYTEA,n INT)", 0), referenceExec(`INSERT INTO s VALUES('\x00',NULL)`, 1), referenceExec("CREATE TABLE t(v BYTEA,n INT NOT NULL)", 0)} {
				run(s)
			}
			for _, tc := range []struct{ setting, detail string }{
				{"hex", `Failing row contains (\x00, null).`},
				{"escape", `Failing row contains (\000, null).`},
			} {
				run(referenceExec("SET bytea_output="+tc.setting, 0))
				for _, query := range []string{"INSERT INTO t SELECT v,n FROM s", "INSERT INTO t SELECT v,NULL FROM s"} {
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError(query, referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: tc.detail, Schema: "public", Table: "t", Column: "n"})
						s.prepared, s.errorStage = prepared, "execute"
						run(s)
						run(referenceSelect("SELECT * FROM t", []string{"v", "n"}, []string{"BYTEA", "INT4"}, [][]any{}))
					}
				}
			}
			var count int
			if err := conn.QueryRowContext(ctx, "SELECT num_nulls(v) FROM t").Scan(&count); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("failed INSERT changed empty destination", err)
			}
		})
	}
}
