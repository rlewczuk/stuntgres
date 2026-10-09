package reference_test

import "testing"

func TestReferenceAlterRenameLockTimeout(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT)", 0))
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0))
			run(referenceExec("SET lock_timeout='20ms'", 0))
			for _, q := range []string{"ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
				run(roleReferenceError(q, "55P03", "canceling statement due to lock timeout", "", true, "execute"))
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, nil))
		})
	}
}
