package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckPreparedExecutionOutputEnvironment(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
			defer func() { run(fixture.Exec("DROP SCHEMA IF EXISTS checks CASCADE", 0)) }()
			run(fixture.Exec("CREATE SCHEMA checks CREATE TABLE t(d DATE,b BYTEA,n INT CONSTRAINT c CHECK(n=1))", 0))
			q := "INSERT INTO checks.t VALUES($1,$2,$3)"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			for _, tc := range []struct{ dateStyle, byteaOutput, detail string }{
				{"SQL, DMY", "escape", `02/01/2024, \000\001\\, 2`},
				{"ISO, MDY", "hex", `2024-01-02, \x00015c, 2`},
			} {
				run(fixture.Exec("SET DateStyle TO '"+tc.dateStyle+"'", 0))
				run(fixture.Exec("SET bytea_output TO '"+tc.byteaOutput+"'", 0))
				want := fixture.CheckViolation("t", "c", tc.detail)
				want.Schema = "checks"
				fixture.CheckRun(t, p.Ctx, held, b.Recorder, fixture.ImplicitError(q, want, "2024-01-02", []byte{0, 1, '\\'}, 2))
			}
		})
	}
}
