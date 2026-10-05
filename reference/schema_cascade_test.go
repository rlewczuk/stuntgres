//go:build reference

package reference_test

import (
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceSchemaOwnerCascadeIndependentTable(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			roleReferenceCleanup(t, []string{"scascade_a", "scascade_b"})
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "scascade_owned", "scascade_peer")
			p.step(t, referenceExec("CREATE USER scascade_a", 0))
			p.step(t, referenceExec("CREATE USER scascade_b", 0))
			script := "CREATE SCHEMA scascade_owned AUTHORIZATION scascade_a; CREATE SCHEMA scascade_peer; CREATE TABLE scascade_peer.t(id INT); INSERT INTO scascade_peer.t VALUES(9); SET SESSION AUTHORIZATION scascade_b; CREATE TABLE scascade_owned.t(id INT); INSERT INTO scascade_owned.t VALUES(7);"
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
				t.Fatal(err)
			}
			// Oracle-only privileged setup: native SQL has no GRANT surface.
			oracle, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			for _, q := range []string{"CREATE SCHEMA scascade_owned AUTHORIZATION scascade_a", "CREATE SCHEMA scascade_peer CREATE TABLE t(id INT)", "INSERT INTO scascade_peer.t VALUES(9)", "GRANT USAGE,CREATE ON SCHEMA scascade_owned TO scascade_b", "SET SESSION AUTHORIZATION scascade_b", "CREATE TABLE scascade_owned.t(id INT)", "INSERT INTO scascade_owned.t VALUES(7)", "RESET SESSION AUTHORIZATION"} {
				if _, err := oracle.ExecContext(p.ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			var owner string
			if err := oracle.QueryRowContext(p.ctx, "SELECT relowner::regrole::text FROM pg_class WHERE oid='scascade_owned.t'::regclass").Scan(&owner); err != nil || owner != "scascade_b" {
				t.Fatal(owner, err)
			}
			for _, postgres := range []bool{false, true} {
				var notices []referenceDiagnostic
				_, c, record := noticeReferencePool(t, p, postgres, &notices)
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, record, s) }
				run(referenceExec("SET SESSION AUTHORIZATION scascade_a", 0))
				drop := referenceExec("DROP SCHEMA scascade_owned CASCADE", 0)
				drop.prepared = prepared
				run(drop)
				want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to table scascade_owned.t"}}
				if len(notices) != 1 || notices[0] != want[0] {
					t.Fatal(notices, want)
				}
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				run(referenceSelect("SELECT id FROM scascade_peer.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
				run(referencePrepared(referenceExec("DROP ROLE scascade_a", 0)))
				run(referencePrepared(referenceExec("DROP ROLE scascade_b", 0)))
				run(schemaReferenceCreationError("CREATE TABLE scascade_owned.probe(id INT)", "3F000", `schema "scascade_owned" does not exist`, prepared, "14"))
			}
		})
	}
}
