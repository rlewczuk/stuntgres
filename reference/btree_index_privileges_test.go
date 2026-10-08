//go:build reference

package reference_test

import "testing"

// GRANT/REVOKE here are real pinned PostgreSQL SQL, not a native expansion.
func TestReferenceBtreeIndexPrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"btree_owner", "btree_reader"})
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "btree_acl")
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	recorder := &referenceRecorder{}
	run := func(q string, count int64) { referenceRunStep(t, p.ctx, c, recorder, referenceExec(q, count)) }
	absent := func(name string) {
		referenceRunStep(t, p.ctx, c, recorder, referenceSelect("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, nil, name))
	}
	denied := func(query, message string, prepared bool) {
		step := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: message})
		step.prepared = prepared
		step.errorStage = "execute"
		referenceRunStep(t, p.ctx, c, recorder, step)
	}
	for _, q := range []string{"CREATE USER btree_owner", "CREATE USER btree_reader", "CREATE SCHEMA btree_acl", "GRANT USAGE,CREATE ON SCHEMA btree_acl TO btree_owner,btree_reader", "SET ROLE btree_owner", "CREATE TABLE btree_acl.t(n INT)"} {
		run(q, 0)
	}
	run("INSERT INTO btree_acl.t VALUES(1)", 1)
	run("CREATE INDEX owner_i ON btree_acl.t(n)", 0)
	referenceRunStep(t, p.ctx, c, recorder, referencePrepared(referenceExec("CREATE INDEX owner_prepared_i ON btree_acl.t(n)", 0)))
	run("RESET ROLE", 0)
	run("GRANT SELECT,INSERT ON btree_acl.t TO btree_reader", 0)
	referenceRunStep(t, p.ctx, c, recorder, referenceSelect("SELECT c.relname,r.rolname,n.nspname FROM pg_class c JOIN pg_roles r ON c.relowner=r.oid JOIN pg_namespace n ON c.relnamespace=n.oid WHERE c.relname='owner_i'", []string{"relname", "rolname", "nspname"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"owner_i", "btree_owner", "btree_acl"}}))
	run("SET ROLE btree_reader", 0)
	for _, prepared := range []bool{false, true} {
		denied("CREATE INDEX reader_i ON btree_acl.t(n)", "must be owner of table t", prepared)
		absent("reader_i")
	}
	run("RESET ROLE", 0)
	run("ALTER SCHEMA btree_acl OWNER TO btree_reader", 0)
	run("SET ROLE btree_reader", 0)
	for _, prepared := range []bool{false, true} {
		denied("CREATE INDEX schema_owner_i ON btree_acl.t(n)", "must be owner of table t", prepared)
		absent("schema_owner_i")
	}
	run("RESET ROLE", 0)
	run("ALTER SCHEMA btree_acl OWNER TO postgres", 0)
	run("GRANT USAGE,CREATE ON SCHEMA btree_acl TO btree_reader", 0)
	run("REVOKE CREATE ON SCHEMA btree_acl FROM btree_owner,PUBLIC", 0)
	run("SET ROLE btree_owner", 0)
	for _, prepared := range []bool{false, true} {
		denied("CREATE INDEX denied_i ON btree_acl.t(n)", "permission denied for schema btree_acl", prepared)
		absent("denied_i")
	}
	run("RESET ROLE", 0)
	run("GRANT CREATE ON SCHEMA btree_acl TO btree_owner", 0)
	run("SET ROLE btree_owner", 0)
	const heldQuery = "CREATE INDEX role_i ON btree_acl.t(n)"
	stmt, err := c.PrepareContext(p.ctx, heldQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: heldQuery}
	run("SET ROLE btree_reader", 0)
	step := referenceImplicitError(heldQuery, referenceDiagnostic{Code: "42501", Message: "must be owner of table t"})
	step.errorStage = "execute"
	referenceRunStep(t, p.ctx, held, recorder, step)
	absent("role_i")
	run("SET ROLE btree_owner", 0)
	referenceRunStep(t, p.ctx, held, recorder, referenceExec(heldQuery, 0))
	run("RESET ROLE", 0)
	writer, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceLockExec(t, p.ctx, tx, "INSERT INTO btree_acl.t VALUES(2)", 1)
	run("SET ROLE btree_reader", 0)
	run("SET lock_timeout='20ms'", 0)
	for _, prepared := range []bool{false, true} {
		denied("CREATE INDEX blocked_reader_i ON btree_acl.t(n)", "must be owner of table t", prepared)
		absent("blocked_reader_i")
	}
	run("RESET ROLE", 0)
	run("SET lock_timeout=0", 0)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	run("SET ROLE btree_owner", 0)
	run("CREATE INDEX final_i ON btree_acl.t(n)", 0)
	run("RESET ROLE", 0)
	referenceRunStep(t, p.ctx, c, recorder, referenceSelect("SELECT n FROM btree_acl.t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
}
