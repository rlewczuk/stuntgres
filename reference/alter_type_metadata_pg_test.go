//go:build reference

package reference_test

import (
	"testing"
)

func TestReferenceAlterTypeCollationSchemaAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"alter_reader"})
	schemaReferenceCleanup(t, "denied")
	p := newReferencePair(t)
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.ctx, "RESET ROLE")
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, p.postgresRecord, s) }
	for _, q := range []string{"CREATE ROLE alter_reader", "CREATE SCHEMA denied", "GRANT USAGE,CREATE ON SCHEMA public TO alter_reader", "SET ROLE alter_reader", "CREATE TABLE t(c TEXT)", "INSERT INTO t VALUES('keep')"} {
		count := int64(0)
		if q == "INSERT INTO t VALUES('keep')" {
			count = 1
		}
		run(referenceExec(q, count))
	}
	failure := referenceImplicitError(`ALTER TABLE t ALTER c TYPE TEXT COLLATE denied."C"`, referenceDiagnostic{Code: "42501", Message: "permission denied for schema denied", Position: "33"})
	failure.errorStage = "execute"
	run(failure)
	run(referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"keep"}}))
	run(referenceExec("RESET ROLE", 0))
}
