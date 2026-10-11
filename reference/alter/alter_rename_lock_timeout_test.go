package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenameLockTimeout(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT)", 0))
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0))
			run(fixture.Exec("SET lock_timeout='20ms'", 0))
			for _, q := range []string{"ALTER TABLE t RENAME a TO renamed", "ALTER TABLE t RENAME TO newtable"} {
				run(fixture.RoleReferenceError(q, "55P03", "canceling statement due to lock timeout", "", true, "execute"))
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, nil))
		})
	}
}
