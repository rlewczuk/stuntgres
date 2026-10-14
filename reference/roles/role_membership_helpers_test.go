package reference_test

import (
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

type membershipScenario struct {
	t         *testing.T
	p         *fixture.Pair
	backend   fixture.Backend
	c         *sql.Conn
	record    *fixture.Recorder
	notices   *[]fixture.Diagnostic
	prepared  bool
	bootstrap string
}

func membershipCases(t *testing.T, names []string, body func(*membershipScenario)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					bootstrap := "postgres"
					if backend.PG {
						bootstrap = "stuntgres"
					}
					s := &membershipScenario{t, p, backend, c, record, &notices, prepared, bootstrap}
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					for _, name := range names {
						s.exec("CREATE ROLE " + pq.QuoteIdentifier(name) + " LOGIN")
					}
					body(s)
				})
			}
		})
	}
}

func (s *membershipScenario) run(step fixture.Step) {
	s.t.Helper()
	step.Prepared = s.prepared
	fixture.RunStep(s.t, s.p.Ctx, s.c, s.record, step)
}
func (s *membershipScenario) exec(q string) { s.t.Helper(); s.run(fixture.Exec(q, 0)) }
func (s *membershipScenario) actor(name string) {
	s.t.Helper()
	s.exec("RESET SESSION AUTHORIZATION")
	if name != "" {
		s.exec("SET SESSION AUTHORIZATION " + pq.QuoteIdentifier(name))
	}
}
func (s *membershipScenario) fail(q, code, message, detail string) {
	s.t.Helper()
	s.run(fixture.RoleReferenceError(q, code, message, detail, s.prepared, "execute"))
}
func (s *membershipScenario) restrict(q string) {
	s.t.Helper()
	step := fixture.RoleReferenceError(q, "2BP01", "dependent privileges exist", "", s.prepared, "execute")
	step.Diagnostic.Hint = "Use CASCADE to revoke them too."
	s.run(step)
}
func (s *membershipScenario) read(allowed bool) {
	s.t.Helper()
	if allowed {
		s.run(fixture.Select("SELECT id FROM membership_owned ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		return
	}
	step := fixture.RoleReferenceError("SELECT id FROM membership_owned ORDER BY id", "42501", "permission denied for table membership_owned", "", s.prepared, "execute")
	step.SelectRows = true
	s.run(step)
}
func (s *membershipScenario) owned(owner string) {
	s.t.Helper()
	s.actor(owner)
	fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, owner, func() {
		s.exec("CREATE TABLE membership_owned(id INT)")
	})
	s.run(fixture.Exec("INSERT INTO membership_owned VALUES(7)", 1))
	s.actor("")
}
func (s *membershipScenario) flags(role, member, grantor string, count int, admin, inherit, set bool) {
	s.t.Helper()
	// Independent additional catalog witness, not a replacement for shared SQL.
	if !s.backend.PG {
		return
	}
	rows, err := s.c.QueryContext(s.p.Ctx, `SELECT a.admin_option,a.inherit_option,a.set_option FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member JOIN pg_roles g ON g.oid=a.grantor WHERE r.rolname=$1 AND m.rolname=$2 AND g.rolname=$3`, role, member, grantor)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var a, i, x bool
		if err := rows.Scan(&a, &i, &x); err != nil {
			s.t.Fatal(err)
		}
		if a != admin || i != inherit || x != set {
			s.t.Fatalf("flags %v/%v/%v want %v/%v/%v", a, i, x, admin, inherit, set)
		}
		n++
	}
	if rows.Err() != nil || n != count {
		s.t.Fatal("triple count", n, count, rows.Err())
	}
}
func (s *membershipScenario) notice(severity, code, message string) {
	s.t.Helper()
	want := []fixture.Diagnostic{{Severity: severity, SeverityUnlocalized: severity, Code: code, Message: message}}
	if !reflect.DeepEqual(*s.notices, want) {
		s.t.Fatalf("notices %#v want %#v", *s.notices, want)
	}
	*s.notices = nil
}
func (s *membershipScenario) clearNotices() { *s.notices = nil }
func membershipDenied(role, verb string) (string, string) {
	return fmt.Sprintf("permission denied to %s role \"%s\"", verb, role), fmt.Sprintf("Only roles with the ADMIN option on role \"%s\" may %s this role.", role, verb)
}
