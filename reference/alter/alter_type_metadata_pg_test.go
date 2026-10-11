//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTypeCollationSchemaAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_reader"})
	fixture.SchemaReferenceCleanup(t, "denied")
	p := fixture.NewPair(t)
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.Ctx, "RESET ROLE")
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, p.PostgresRecord, s) }
	for _, q := range []string{"CREATE ROLE alter_reader", "CREATE SCHEMA denied", "GRANT USAGE,CREATE ON SCHEMA public TO alter_reader", "SET ROLE alter_reader", "CREATE TABLE t(c TEXT)", "INSERT INTO t VALUES('keep')"} {
		count := int64(0)
		if q == "INSERT INTO t VALUES('keep')" {
			count = 1
		}
		run(fixture.Exec(q, count))
	}
	failure := fixture.ImplicitError(`ALTER TABLE t ALTER c TYPE TEXT COLLATE denied."C"`, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema denied", Position: "33"})
	failure.ErrorStage = "execute"
	run(failure)
	run(fixture.Select("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"keep"}}))
	run(fixture.Exec("RESET ROLE", 0))
}
