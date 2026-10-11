package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectCapturedConstraintOutput(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			conn, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET bytea_output")
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, ctx, conn, backend.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE s(v BYTEA,n INT)", 0), fixture.Exec(`INSERT INTO s VALUES('\x00',NULL)`, 1), fixture.Exec("CREATE TABLE t(v BYTEA,n INT NOT NULL)", 0)} {
				run(s)
			}
			for _, tc := range []struct{ setting, detail string }{
				{"hex", `Failing row contains (\x00, null).`},
				{"escape", `Failing row contains (\000, null).`},
			} {
				run(fixture.Exec("SET bytea_output="+tc.setting, 0))
				for _, query := range []string{"INSERT INTO t SELECT v,n FROM s", "INSERT INTO t SELECT v,NULL FROM s"} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: tc.detail, Schema: "public", Table: "t", Column: "n"})
						s.Prepared, s.ErrorStage = prepared, "execute"
						run(s)
						run(fixture.Select("SELECT * FROM t", []string{"v", "n"}, []string{"BYTEA", "INT4"}, [][]any{}))
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
