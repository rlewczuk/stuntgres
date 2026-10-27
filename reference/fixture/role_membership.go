package fixture

import (
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"reflect"
	"testing"
)

type RoleMembershipScenario struct {
	T         *testing.T
	P         *Pair
	Backend   Backend
	C         *sql.Conn
	Record    *Recorder
	Notices   *[]Diagnostic
	Prepared  bool
	Bootstrap string
}

func RoleMembershipCases(t *testing.T, names []string, body func(*RoleMembershipScenario)) {
	t.Helper()
	RoleMembershipCasesFor(t, names, []bool{false, true}, body)
}

// RoleMembershipCasesFor runs body once per listed prepared mode.
func RoleMembershipCasesFor(t *testing.T, names []string, modes []bool, body func(*RoleMembershipScenario)) {
	t.Helper()
	for _, prepared := range modes {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			RoleReferenceCleanup(t, names)
			p := NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []Diagnostic
					_, c, record := NoticeReferencePool(t, p, backend.PG, &notices)
					bootstrap := "postgres"
					if backend.PG {
						bootstrap = "stuntgres"
					}
					s := &RoleMembershipScenario{T: t, P: p, Backend: backend, C: c, Record: record, Notices: &notices, Prepared: prepared, Bootstrap: bootstrap}
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					for _, name := range names {
						s.Exec("CREATE ROLE " + pq.QuoteIdentifier(name) + " LOGIN")
					}
					body(s)
				})
			}
		})
	}
}

func (s *RoleMembershipScenario) Run(step Step) {
	s.T.Helper()
	step.Prepared = s.Prepared
	RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
}
func (s *RoleMembershipScenario) Exec(q string) { s.T.Helper(); s.Run(Exec(q, 0)) }
func (s *RoleMembershipScenario) Actor(name string) {
	s.T.Helper()
	s.Exec("RESET SESSION AUTHORIZATION")
	if name != "" {
		s.Exec("SET SESSION AUTHORIZATION " + pq.QuoteIdentifier(name))
	}
}
func (s *RoleMembershipScenario) Fail(q, code, message, detail string) {
	s.T.Helper()
	s.Run(RoleReferenceError(q, code, message, detail, s.Prepared, "execute"))
}
func (s *RoleMembershipScenario) Restrict(q string) {
	s.T.Helper()
	step := RoleReferenceError(q, "2BP01", "dependent privileges exist", "", s.Prepared, "execute")
	step.Diagnostic.Hint = "Use CASCADE to revoke them too."
	s.Run(step)
}
func (s *RoleMembershipScenario) Read(allowed bool) {
	s.T.Helper()
	if allowed {
		s.Run(Select("SELECT id FROM membership_owned ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		return
	}
	step := RoleReferenceError("SELECT id FROM membership_owned ORDER BY id", "42501", "permission denied for table membership_owned", "", s.Prepared, "execute")
	step.SelectRows = true
	s.Run(step)
}
func (s *RoleMembershipScenario) Owned(owner string) {
	s.T.Helper()
	s.Actor(owner)
	WithPublicSchemaOwner(s.T, s.P, s.Backend, owner, func() {
		s.Exec("CREATE TABLE membership_owned(id INT)")
	})
	s.Run(Exec("INSERT INTO membership_owned VALUES(7)", 1))
	s.Actor("")
}
func (s *RoleMembershipScenario) Flags(role, member, grantor string, count int, admin, inherit, set bool) {
	s.T.Helper()
	// Shared direct-edge witness; options are stored metadata on both backends.
	rows, err := s.C.QueryContext(s.P.Ctx, `SELECT a.admin_option,a.inherit_option,a.set_option FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member JOIN pg_roles g ON g.oid=a.grantor WHERE r.rolname=$1 AND m.rolname=$2 AND g.rolname=$3`, role, member, grantor)
	if err != nil {
		s.T.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var a, i, x bool
		if err := rows.Scan(&a, &i, &x); err != nil {
			s.T.Fatal(err)
		}
		if a != admin || i != inherit || x != set {
			s.T.Fatalf("flags %v/%v/%v want %v/%v/%v", a, i, x, admin, inherit, set)
		}
		n++
	}
	if rows.Err() != nil || n != count {
		s.T.Fatal("triple count", n, count, rows.Err())
	}
}
func (s *RoleMembershipScenario) Notice(severity, code, message string) {
	s.T.Helper()
	want := []Diagnostic{{Severity: severity, SeverityUnlocalized: severity, Code: code, Message: message}}
	if !reflect.DeepEqual(*s.Notices, want) {
		s.T.Fatalf("notices %#v want %#v", *s.Notices, want)
	}
	*s.Notices = nil
}
func (s *RoleMembershipScenario) ClearNotices() { *s.Notices = nil }
func RoleMembershipDenied(role, verb string) (string, string) {
	return fmt.Sprintf("permission denied to %s role \"%s\"", verb, role), fmt.Sprintf("Only roles with the ADMIN option on role \"%s\" may %s this role.", role, verb)
}
