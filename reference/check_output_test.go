//go:build reference

package reference_test

import "testing"

func TestReferenceCheckPreparedExecutionOutputEnvironment(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		t.Cleanup(func() { run(referenceExec("DROP SCHEMA IF EXISTS checks CASCADE", 0)) })
		run(referenceExec("CREATE SCHEMA checks CREATE TABLE t(d DATE,b BYTEA,n INT CONSTRAINT c CHECK(n=1))", 0))
		run(referenceExec("SET DateStyle TO 'SQL, DMY'", 0))
		run(referenceExec("SET bytea_output TO 'escape'", 0))
		want := referenceCheckViolation("t", "c", `02/01/2024, \000\001\\, 2`)
		want.Schema = "checks"
		run(referencePrepared(referenceImplicitError("INSERT INTO checks.t VALUES($1,$2,$3)", want, "2024-01-02", []byte{0, 1, '\\'}, 2)))
		run(referenceExec("SET DateStyle TO 'ISO, MDY'", 0))
		run(referenceExec("SET bytea_output TO 'hex'", 0))
		want = referenceCheckViolation("t", "c", `2024-01-02, \x00015c, 2`)
		want.Schema = "checks"
		run(referencePrepared(referenceImplicitError("INSERT INTO checks.t VALUES($1,$2,$3)", want, "2024-01-02", []byte{0, 1, '\\'}, 2)))
	})
}
