package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func accessRoleCases(t *testing.T, names []string, body func(*membershipScenario)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, names)
			for _, schema := range []string{"ar_private", "ar_first", "ar_second"} {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					bootstrap := "postgres"
					if b.PG {
						bootstrap = "stuntgres"
					}
					s := &membershipScenario{t, p, b, c, record, &notices, prepared, bootstrap}
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					if b.PG {
						s.exec("GRANT USAGE ON SCHEMA public TO PUBLIC")
					}
					for _, name := range names {
						s.exec("CREATE ROLE " + pq.QuoteIdentifier(name) + " LOGIN")
					}
					body(s)
				})
			}
		})
	}
}

func accessRoleMetadata(b fixture.Backend, strictNullability bool) func(*testing.T, *sql.Rows) {
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

func accessRoleSelect(s *membershipScenario, q string, columns []string, rows [][]any, args ...any) {
	s.t.Helper()
	types := make([]string, len(columns))
	for i := range types {
		types[i] = "BOOL"
	}
	step := fixture.Select(q, columns, types, rows, args...)
	step.CheckMetadata = accessRoleMetadata(s.backend, true)
	s.run(step)
}
func accessRoleBool(s *membershipScenario, q string, want any, args ...any) {
	s.t.Helper()
	accessRoleSelect(s, q, []string{"v"}, [][]any{{want}}, args...)
}
func accessRoleOID(s *membershipScenario, name string) int64 {
	s.t.Helper()
	var oid int64
	if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", name).Scan(&oid); err != nil {
		s.t.Fatal(err)
	}
	return oid
}
func accessRoleVector(s *membershipScenario, actor, target string, want ...any) {
	s.t.Helper()
	accessRoleSelect(s, "SELECT pg_has_role($1::name,$2::name,'MEMBER') AS m,pg_has_role($1::name,$2::name,'USAGE') AS u,pg_has_role($1::name,$2::name,'SET') AS s,pg_has_role($1::name,$2::name,'MEMBER WITH ADMIN OPTION') AS a", []string{"m", "u", "s", "a"}, [][]any{want}, actor, target)
}
func accessRoleGraph(s *membershipScenario) {
	s.t.Helper()
	s.exec("GRANT ar_middle TO ar_actor WITH INHERIT FALSE, SET TRUE")
	s.exec("GRANT ar_target TO ar_middle WITH ADMIN TRUE, INHERIT TRUE, SET FALSE")
}
func accessRoleTable(s *membershipScenario) {
	s.t.Helper()
	s.actor("ar_target")
	fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "ar_target", func() { s.exec("CREATE TABLE ar_t(id INT)") })
	s.actor("")
}
func accessRoleRuntimeError(s *membershipScenario, q, code, message string, args ...any) {
	s.t.Helper()
	for _, query := range []bool{true, false} {
		d := fixture.ControlsReferenceDiagnostic(code, message)
		step := fixture.ErrorDiagnostic(q, d, args...)
		step.SelectRows = query
		step.ErrorStage = "execute"
		s.run(step)
	}
}
func accessRoleHeldBool(s *membershipScenario, held fixture.HeldStatement, want any, args ...any) {
	s.t.Helper()
	step := fixture.Select(held.SQL, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
	step.CheckMetadata = accessRoleMetadata(s.backend, true)
	fixture.RunStep(s.t, s.p.Ctx, held, s.record, step)
}
