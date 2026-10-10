package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceConcurrentIndexDisclosure(t *testing.T) {
	for _, kind := range []string{"computed", "parenthesized", "collated"} {
		computed := kind == "computed"
		for _, tableSelect := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/table_select=%t/held=%t", kind, tableSelect, held), func(t *testing.T) {
					roleReferenceCleanup(t, []string{"ci_disclosure_owner"})
					p := newReferenceScenario(t)
					for _, b := range p.backends() {
						t.Run(b.name, func(t *testing.T) {
							c := concurrentIndexConn(t, p, b)
							run := func(step referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, step) }
							run(referenceExec("CREATE ROLE ci_disclosure_owner", 0))
							if b.pg { run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO ci_disclosure_owner", 0)) }
							run(referenceExec("SET SESSION AUTHORIZATION ci_disclosure_owner", 0))
							run(referenceExec("CREATE TABLE t(v TEXT,n INTEGER,payload TEXT)", 0))
							seed := "INSERT INTO t VALUES('A',1,'protected'),('a',1,'different')"
							if kind == "collated" {
								seed = "INSERT INTO t VALUES('A',1,'protected'),('A',1,'different')"
							}
							run(referenceExec(seed, 2))
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
							run(referenceExec("REVOKE SELECT ON t FROM ci_disclosure_owner", 0))
							run(referenceExec("GRANT SELECT(v,n) ON t TO ci_disclosure_owner", 0))
							// Ownership/MAINTAIN is not SELECT on the full table.
							run(referenceExec("GRANT MAINTAIN ON t TO ci_disclosure_owner", 0))
							if tableSelect {
								run(referenceExec("GRANT SELECT ON t TO ci_disclosure_owner", 0))
							}
							if computed && !tableSelect {
								buildDetail, typeDetail, writeDetail = "Duplicate keys exist.", "Duplicate keys exist.", ""
							}
							check := func(query string, want referenceDiagnostic) {
								step := referenceErrorDiagnostic(query, want)
								step.errorStage = "execute"
								if held {
									step = referencePrepared(step)
								}
								run(step)
							}
							check("CREATE UNIQUE INDEX CONCURRENTLY ui ON t("+definition+") INCLUDE(payload)", concurrentIndexUnique("t", "ui", buildDetail, true))
							check("REINDEX INDEX ui", concurrentIndexUnique("t", "ui", buildDetail, true))
							check("ALTER TABLE t ALTER n TYPE BIGINT USING 0", concurrentIndexUnique("t", "ui", typeDetail, true))
							run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
							run(referenceExec("RESET SESSION AUTHORIZATION", 0))
							run(referenceExec("DELETE FROM t", 2))
							run(referenceExec("INSERT INTO t VALUES('A',1,'protected')", 1))
							run(referenceExec("SET SESSION AUTHORIZATION ci_disclosure_owner", 0))
							run(referenceExec("REINDEX INDEX ui", 0))
							write := "INSERT INTO t VALUES('a',1,'different')"
							if kind == "collated" {
								write = "INSERT INTO t VALUES('A',1,'different')"
							}
							check(write, concurrentIndexUnique("t", "ui", writeDetail, false))
							run(referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
							run(referenceExec("RESET SESSION AUTHORIZATION", 0))
						})
					}
				})
			}
		}
	}
}
