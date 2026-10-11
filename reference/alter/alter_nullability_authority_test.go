package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityAuthorizationLifetime(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"nn_nonowner"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			b.DB.SetMaxOpenConns(2)
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if b.PG {
				defer conn.ExecContext(p.Ctx, "RESET ROLE")
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE nn_nonowner", 0))
			run(fixture.Exec("SET ROLE nn_nonowner", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("CREATE TABLE t(a INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
			tx, err := conn.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			if !b.PG {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE ROLE nn_revision", 0))
			} else {
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET ROLE nn_nonowner", 0))
			}
			if err = tx.Commit(); err != nil {
				t.Fatal("later role change invalidated authorized ALTER", err)
			}
			if b.PG {
				run(fixture.Exec("RESET ROLE", 0))
			}
			fixture.TableDDLMetadata(t, p.Ctx, conn, b.PG, false, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
			run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.ImplicitError("INSERT INTO t VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}))
		})
	}
}
