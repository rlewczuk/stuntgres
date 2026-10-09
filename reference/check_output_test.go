package reference_test

import "testing"

func TestReferenceCheckPreparedExecutionOutputEnvironment(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
			defer func() { run(referenceExec("DROP SCHEMA IF EXISTS checks CASCADE", 0)) }()
			run(referenceExec("CREATE SCHEMA checks CREATE TABLE t(d DATE,b BYTEA,n INT CONSTRAINT c CHECK(n=1))", 0))
			q := "INSERT INTO checks.t VALUES($1,$2,$3)"
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			for _, tc := range []struct{ dateStyle, byteaOutput, detail string }{
				{"SQL, DMY", "escape", `02/01/2024, \000\001\\, 2`},
				{"ISO, MDY", "hex", `2024-01-02, \x00015c, 2`},
			} {
				run(referenceExec("SET DateStyle TO '"+tc.dateStyle+"'", 0))
				run(referenceExec("SET bytea_output TO '"+tc.byteaOutput+"'", 0))
				want := referenceCheckViolation("t", "c", tc.detail)
				want.Schema = "checks"
				referenceCheckRun(t, p.ctx, held, b.recorder, referenceImplicitError(q, want, "2024-01-02", []byte{0, 1, '\\'}, 2))
			}
		})
	}
}
