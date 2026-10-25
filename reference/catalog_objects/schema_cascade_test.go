//go:build reference

package reference_test

import (
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSchemaOwnerCascadeIndependentTable(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"scascade_a", "scascade_b"})
			p := fixture.NewPair(t)
			fixture.SchemaReferenceCleanup(t, "scascade_owned", "scascade_peer")
			p.Step(t, fixture.Exec("CREATE USER scascade_a", 0))
			p.Step(t, fixture.Exec("CREATE USER scascade_b", 0))
			script := "CREATE SCHEMA scascade_owned AUTHORIZATION scascade_a; CREATE SCHEMA scascade_peer; CREATE TABLE scascade_peer.t(id INT); INSERT INTO scascade_peer.t VALUES(9); SET SESSION AUTHORIZATION scascade_b; CREATE TABLE scascade_owned.t(id INT); INSERT INTO scascade_owned.t VALUES(7);"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
				t.Fatal(err)
			}
			// Oracle-only privileged setup: native SQL has no GRANT surface.
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			for _, q := range []string{"CREATE SCHEMA scascade_owned AUTHORIZATION scascade_a", "CREATE SCHEMA scascade_peer CREATE TABLE t(id INT)", "INSERT INTO scascade_peer.t VALUES(9)", "GRANT USAGE,CREATE ON SCHEMA scascade_owned TO scascade_b", "SET SESSION AUTHORIZATION scascade_b", "CREATE TABLE scascade_owned.t(id INT)", "INSERT INTO scascade_owned.t VALUES(7)", "RESET SESSION AUTHORIZATION"} {
				if _, err := oracle.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			var owner string
			if err := oracle.QueryRowContext(p.Ctx, "SELECT relowner::regrole::text FROM pg_class WHERE oid='scascade_owned.t'::regclass").Scan(&owner); err != nil || owner != "scascade_b" {
				t.Fatal(owner, err)
			}
			for _, postgres := range []bool{false, true} {
				var notices []fixture.Diagnostic
				_, c, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, record, s) }
				run(fixture.Exec("SET SESSION AUTHORIZATION scascade_a", 0))
				drop := fixture.Exec("DROP SCHEMA scascade_owned CASCADE", 0)
				drop.Prepared = prepared
				run(drop)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to table scascade_owned.t"}}
				if len(notices) != 1 || notices[0] != want[0] {
					t.Fatal(notices, want)
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Select("SELECT id FROM scascade_peer.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
				run(fixture.Prepared(fixture.Exec("DROP ROLE scascade_a", 0)))
				run(fixture.Prepared(fixture.Exec("DROP ROLE scascade_b", 0)))
				run(fixture.SchemaReferenceCreationError("CREATE TABLE scascade_owned.probe(id INT)", "3F000", `schema "scascade_owned" does not exist`, prepared, "14"))
			}
		})
	}
}
