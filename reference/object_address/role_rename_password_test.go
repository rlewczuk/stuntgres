package reference_test

import (
	"crypto/md5"
	"fmt"
	"reflect"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceRoleRenamePasswords(t *testing.T) {
	for _, route := range []string{"direct", "fresh", "held"} {
		for _, kind := range []string{"md5", "scram", "null"} {
			t.Run(route+"/"+kind, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"rr_password", "rr_password_new", "rr_password_collision"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					var notices []fixture.Diagnostic
					_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
					verifier := "NULL"
					value := ""
					b.Recorder = recorder
					if kind == "md5" {
						value = fmt.Sprintf("md5%x", md5.Sum([]byte("rename-passwordrr_password")))
					}
					if kind == "scram" {
						value = fixture.RolePasswordEncoded(t, "rename-password", []byte("0123456789abcdef"), 4096)
					}
					if kind != "null" {
						verifier = pq.QuoteLiteral(value)
					}
					run(fixture.Exec("CREATE ROLE rr_password LOGIN PASSWORD "+verifier, 0))
					run(fixture.Exec("CREATE ROLE rr_password_collision", 0))
					notices = nil
					failed := fixture.RoleReferenceError("ALTER ROLE rr_password RENAME TO rr_password_collision", "42710", `role "rr_password_collision" already exists`, "", route == "fresh", "execute")
					if route == "held" {
						fixture.RoleTransactionStep(t, p, b, c, true, failed)
					} else {
						run(failed)
					}
					if len(notices) != 0 {
						t.Fatalf("failed rename emitted notices: %#v", notices)
					}
					var stored any
					if kind != "null" {
						stored = value
					}
					run(fixture.Select("SELECT rolpassword FROM pg_authid WHERE rolname='rr_password'", []string{"rolpassword"}, []string{"TEXT"}, [][]any{{stored}}))
					q := "ALTER USER rr_password RENAME TO rr_password_new"
					s := fixture.Exec(q, 0)
					if route == "held" {
						fixture.RoleTransactionStep(t, p, b, c, true, s)
					} else {
						s.Prepared = route == "fresh"
						run(s)
					}
					var want []fixture.Diagnostic
					if kind == "md5" {
						want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "MD5 password cleared because of role rename"}}
					}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("rename notices=%#v want=%#v", notices, want)
					}
					stored = nil
					if kind == "scram" {
						stored = value
					}
					run(fixture.Select("SELECT rolpassword FROM pg_authid WHERE rolname='rr_password_new'", []string{"rolpassword"}, []string{"TEXT"}, [][]any{{stored}}))
				}
			})
		}
	}
}
