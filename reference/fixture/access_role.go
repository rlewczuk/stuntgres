package fixture

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/lib/pq"
)

func AccessRoleCases(t *testing.T, names []string, body func(*RoleMembershipScenario)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			RoleReferenceCleanup(t, names)
			for _, schema := range []string{"ar_private", "ar_first", "ar_second"} {
				SchemaReferenceCleanup(t, schema)
			}
			p := NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []Diagnostic
					_, c, record := NoticeReferencePool(t, p, b.PG, &notices)
					bootstrap := "postgres"
					if b.PG {
						bootstrap = "stuntgres"
					}
					s := &RoleMembershipScenario{T: t, P: p, Backend: b, C: c, Record: record, Notices: &notices, Prepared: prepared, Bootstrap: bootstrap}
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					if b.PG {
						s.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC")
					}
					for _, name := range names {
						s.Exec("CREATE ROLE " + pq.QuoteIdentifier(name) + " LOGIN")
					}
					body(s)
				})
			}
		})
	}
}

func AccessRoleMetadata(b Backend, strictNullability bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range columns {
			if c.DatabaseTypeName() != "BOOL" || c.ScanType() != reflect.TypeFor[bool]() {
				t.Fatalf("Boolean descriptor %s: %s %v", c.Name(), c.DatabaseTypeName(), c.ScanType())
			}
			if strictNullability {
				nullable, known := c.Nullable()
				if b.PG {
					if nullable || known {
						t.Fatalf("pq computed nullability %v %v", nullable, known)
					}
				} else if !known || !nullable {
					t.Fatalf("native strict nullable Boolean %v %v", nullable, known)
				}
			}
			if n, ok := c.Length(); ok || n != 0 {
				t.Fatalf("Boolean length %d %v", n, ok)
			}
			if p, scale, ok := c.DecimalSize(); ok || p != 0 || scale != 0 {
				t.Fatalf("Boolean decimal %d %d %v", p, scale, ok)
			}
		}
	}
}

func AccessRoleSelect(s *RoleMembershipScenario, q string, columns []string, rows [][]any, args ...any) {
	s.T.Helper()
	types := make([]string, len(columns))
	for i := range types {
		types[i] = "BOOL"
	}
	step := Select(q, columns, types, rows, args...)
	step.CheckMetadata = AccessRoleMetadata(s.Backend, true)
	s.Run(step)
}
func AccessRoleBool(s *RoleMembershipScenario, q string, want any, args ...any) {
	s.T.Helper()
	AccessRoleSelect(s, q, []string{"v"}, [][]any{{want}}, args...)
}
func AccessRoleOID(s *RoleMembershipScenario, name string) int64 {
	s.T.Helper()
	var oid int64
	if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", name).Scan(&oid); err != nil {
		s.T.Fatal(err)
	}
	return oid
}
func AccessRoleVector(s *RoleMembershipScenario, actor, target string, want ...any) {
	s.T.Helper()
	AccessRoleSelect(s, "SELECT pg_has_role($1::name,$2::name,'MEMBER') AS m,pg_has_role($1::name,$2::name,'USAGE') AS u,pg_has_role($1::name,$2::name,'SET') AS s,pg_has_role($1::name,$2::name,'MEMBER WITH ADMIN OPTION') AS a", []string{"m", "u", "s", "a"}, [][]any{want}, actor, target)
}
func AccessRoleGraph(s *RoleMembershipScenario) {
	s.T.Helper()
	s.Exec("GRANT ar_middle TO ar_actor WITH INHERIT FALSE, SET TRUE")
	s.Exec("GRANT ar_target TO ar_middle WITH ADMIN TRUE, INHERIT TRUE, SET FALSE")
}
func AccessRoleTable(s *RoleMembershipScenario) {
	s.T.Helper()
	s.Actor("ar_target")
	WithPublicSchemaOwner(s.T, s.P, s.Backend, "ar_target", func() { s.Exec("CREATE TABLE ar_t(id INT)") })
	s.Actor("")
}
func AccessRoleRuntimeError(s *RoleMembershipScenario, q, code, message string, args ...any) {
	s.T.Helper()
	for _, query := range []bool{true, false} {
		d := ControlsReferenceDiagnostic(code, message)
		step := ErrorDiagnostic(q, d, args...)
		step.SelectRows = query
		step.ErrorStage = "execute"
		s.Run(step)
	}
}
func AccessRoleHeldBool(s *RoleMembershipScenario, held HeldStatement, want any, args ...any) {
	s.T.Helper()
	step := Select(held.SQL, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
	step.CheckMetadata = AccessRoleMetadata(s.Backend, true)
	RunStep(s.T, s.P.Ctx, held, s.Record, step)
}
