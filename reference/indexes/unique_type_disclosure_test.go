package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueTypeBuildOwnerDisclosure(t *testing.T) {
	for _, mode := range []string{"none", "partial", "all", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", mode, prepared), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"unique_type_owner"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
						run(fixture.Exec("CREATE ROLE unique_type_owner", 0))
						if b.PG {
							run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO unique_type_owner", 0))
						}
						run(fixture.Exec("SET SESSION AUTHORIZATION unique_type_owner", 0))
						run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,CONSTRAINT u UNIQUE(a,b))", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1,'protected'),(2,'protected')", 2))
						namespace := fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)
						cq := "SELECT c.oid FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE r.relname='t' AND r.relnamespace=$1 AND c.conname='u'"
						iq := "SELECT oid FROM pg_class WHERE relname='u' AND relnamespace=$1"
						var cid, iid int64
						if err := a.QueryRowContext(p.Ctx, cq, namespace).Scan(&cid); err != nil || cid <= 0 {
							t.Fatal(cid, err)
						}
						if err := a.QueryRowContext(p.Ctx, iq, namespace).Scan(&iid); err != nil || iid <= 0 {
							t.Fatal(iid, err)
						}
						run(fixture.Exec("REVOKE SELECT ON t FROM unique_type_owner", 0))
						if mode == "partial" || mode == "all" {
							run(fixture.Exec("GRANT SELECT(a) ON t TO unique_type_owner", 0))
						}
						if mode == "all" {
							run(fixture.Exec("GRANT SELECT(b) ON t TO unique_type_owner", 0))
						}
						if mode == "table" {
							run(fixture.Exec("GRANT SELECT ON t TO unique_type_owner", 0))
						}
						query := "ALTER TABLE t ALTER a TYPE BIGINT USING 0"
						outcome := fixture.RowExecute(p.Ctx, a, query, prepared)
						detail := "Duplicate keys exist."
						if mode == "all" || mode == "table" {
							detail = "Key (a, b)=(0, protected) is duplicated."
						}
						want := fixture.UniqueReferenceDuplicate("t", "u", detail)
						want.Message = `could not create unique index "u"`
						fixture.AssertDiagnostic(t, outcome.Err, &want)
						runtime := fixture.RowExecute(p.Ctx, a, "INSERT INTO t VALUES(1,'protected')", prepared)
						runtimeDetail := ""
						if mode == "all" || mode == "table" {
							runtimeDetail = "Key (a, b)=(1, protected) already exists."
						}
						runtimeWant := fixture.UniqueReferenceDuplicate("t", "u", runtimeDetail)
						fixture.AssertDiagnostic(t, runtime.Err, &runtimeWant)
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						var retainedC, retainedI int64
						if err := a.QueryRowContext(p.Ctx, cq, namespace).Scan(&retainedC); err != nil || retainedC != cid {
							t.Fatal("failed TYPE changed constraint identity", cid, retainedC, err)
						}
						if err := a.QueryRowContext(p.Ctx, iq, namespace).Scan(&retainedI); err != nil || retainedI != iid {
							t.Fatal("failed TYPE changed backing identity", iid, retainedI, err)
						}
						run(fixture.Select("SELECT a,b FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "protected"}, {int64(2), "protected"}}))
						run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE oid=$1", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"UNIQUE (a, b)"}}, cid))
						run(fixture.Exec("INSERT INTO t VALUES(3,'protected')", 1))
					})
				}
			})
		}
	}
}
