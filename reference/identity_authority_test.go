package reference_test

import "testing"

func TestReferenceIdentityInsertOnlyAuthority(t *testing.T) {
	roleReferenceCleanup(t, []string{"identity_writer"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		c, err := backend.db.Conn(p.ctx)
		if err != nil { t.Fatal(err) }
		defer c.Close()
		defer c.ExecContext(p.ctx,"RESET ROLE")
		run := func(s referenceStep) { referenceRunStep(t,p.ctx,c,backend.recorder,s) }
		if backend.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO PUBLIC",0)) }
		for _, q := range []string{`CREATE TABLE secured(id INT GENERATED ALWAYS AS IDENTITY)`, `CREATE ROLE identity_writer`, `GRANT INSERT ON secured TO identity_writer`, `SET ROLE identity_writer`} { run(referenceExec(q,0)) }
		run(referenceExec(`INSERT INTO secured DEFAULT VALUES`,1))
		run(referenceImplicitError(`SELECT nextval('secured_id_seq')`,referenceDiagnostic{Code:"42501",Message:"permission denied for sequence secured_id_seq"}))
		run(referenceExec(`RESET ROLE`,0))
		run(referenceSelect(`SELECT id FROM secured`,[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
	}
}
