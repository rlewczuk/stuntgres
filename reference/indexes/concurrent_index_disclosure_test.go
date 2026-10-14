package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexDisclosure(t *testing.T) {
	for _, kind := range []string{"computed", "parenthesized", "collated"} {
		computed := kind == "computed"
		for _, tableSelect := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/table_select=%t/held=%t", kind, tableSelect, held), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"ci_disclosure_owner"})
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c := fixture.ConcurrentIndexConn(t, p, b)
							run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
							run(fixture.Exec("CREATE ROLE ci_disclosure_owner", 0))
							run(fixture.Exec("SET SESSION AUTHORIZATION ci_disclosure_owner", 0))
							fixture.WithPublicSchemaOwner(t, p, b, "ci_disclosure_owner", func() { run(fixture.Exec("CREATE TABLE t(v TEXT,n INTEGER,payload TEXT)", 0)) })
							seed := "INSERT INTO t VALUES('A',1,'protected'),('a',1,'different')"
							if kind == "collated" {
								seed = "INSERT INTO t VALUES('A',1,'protected'),('A',1,'different')"
							}
							run(fixture.Exec(seed, 2))
							definition := `lower(v) COLLATE "C" text_ops,(n+1)`
							buildDetail := "Key (lower(v), (n + 1))=(a, 2) is duplicated."
							typeDetail := "Key (lower(v), (n + 1))=(a, 1) is duplicated."
							writeDetail := "Key (lower(v), (n + 1))=(a, 2) already exists."
							if !computed {
								definition = `(n),(n)`
								buildDetail = "Key (n, n)=(1, 1) is duplicated."
								typeDetail = "Key (n, n)=(0, 0) is duplicated."
								writeDetail = "Key (n, n)=(1, 1) already exists."
							}
							if kind == "collated" {
								definition = `(v) COLLATE "C" text_ops,n`
								buildDetail = "Key (v, n)=(A, 1) is duplicated."
								typeDetail = "Key (v, n)=(A, 0) is duplicated."
								writeDetail = "Key (v, n)=(A, 1) already exists."
							}
							run(fixture.Exec("REVOKE SELECT ON t FROM ci_disclosure_owner", 0))
							run(fixture.Exec("GRANT SELECT(v,n) ON t TO ci_disclosure_owner", 0))
							// Ownership/MAINTAIN is not SELECT on the full table.
							run(fixture.Exec("GRANT MAINTAIN ON t TO ci_disclosure_owner", 0))
							if tableSelect {
								run(fixture.Exec("GRANT SELECT ON t TO ci_disclosure_owner", 0))
							}
							if computed && !tableSelect {
								buildDetail, typeDetail, writeDetail = "Duplicate keys exist.", "Duplicate keys exist.", ""
							}
							check := func(query string, want fixture.Diagnostic) {
								step := fixture.ErrorDiagnostic(query, want)
								step.ErrorStage = "execute"
								if held {
									step = fixture.Prepared(step)
								}
								run(step)
							}
							fixture.WithPublicSchemaOwner(t, p, b, "ci_disclosure_owner", func() {
								check("CREATE UNIQUE INDEX CONCURRENTLY ui ON t("+definition+") INCLUDE(payload)", fixture.ConcurrentIndexUnique("t", "ui", buildDetail, true))
							})
							check("REINDEX INDEX ui", fixture.ConcurrentIndexUnique("t", "ui", buildDetail, true))
							check("ALTER TABLE t ALTER n TYPE BIGINT USING 0", fixture.ConcurrentIndexUnique("t", "ui", typeDetail, true))
							run(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
							run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
							run(fixture.Exec("DELETE FROM t", 2))
							run(fixture.Exec("INSERT INTO t VALUES('A',1,'protected')", 1))
							run(fixture.Exec("SET SESSION AUTHORIZATION ci_disclosure_owner", 0))
							run(fixture.Exec("REINDEX INDEX ui", 0))
							write := "INSERT INTO t VALUES('a',1,'different')"
							if kind == "collated" {
								write = "INSERT INTO t VALUES('A',1,'different')"
							}
							check(write, fixture.ConcurrentIndexUnique("t", "ui", writeDetail, false))
							run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
							run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						})
					}
				})
			}
		}
	}
}
