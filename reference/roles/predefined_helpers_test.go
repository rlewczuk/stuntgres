package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

var predefinedRoles = []struct {
	name string
	oid  int64
}{
	{"pg_checkpoint", 4544}, {"pg_create_subscription", 6304},
	{"pg_database_owner", 6171}, {"pg_maintain", 6337},
	{"pg_monitor", 3373}, {"pg_read_all_settings", 3374},
	{"pg_read_all_stats", 3375}, {"pg_stat_scan_tables", 3377},
	{"pg_read_all_data", 6181}, {"pg_write_all_data", 6182},
	{"pg_read_server_files", 4569}, {"pg_write_server_files", 4570},
	{"pg_execute_server_program", 4571}, {"pg_signal_autovacuum_worker", 6392},
	{"pg_signal_backend", 4200}, {"pg_use_reserved_connections", 4550},
}

// Shared membership fixture semantics, without ever creating/dropping builtins.
func predefinedCases(t *testing.T, names []string, body func(*fixture.RoleMembershipScenario, *sql.DB), beforeScenario ...func(*testing.T)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, names)
			fixture.RoleReferenceCleanup(t, []string{"pdef_new"})
			for _, schema := range []string{"pdef_ad", "pdef_owner", "pdef_private", "pdef_rls"} {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			for _, register := range beforeScenario {
				register(t)
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					db, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					bootstrap := "postgres"
					if b.PG {
						bootstrap = "stuntgres"
					}
					s := &fixture.RoleMembershipScenario{T: t, P: p, Backend: b, C: c, Record: record, Notices: &notices, Prepared: prepared, Bootstrap: bootstrap}
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					if b.PG {
						s.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC")
					}
					for _, name := range names {
						s.Exec("CREATE ROLE " + name + " LOGIN")
					}
					body(s, db)
				})
			}
		})
	}
}

func predefinedSelect(s *fixture.RoleMembershipScenario, q string, columns, types []string, rows [][]any) {
	s.T.Helper()
	s.Run(fixture.Select(q, columns, types, rows))
}

func predefinedFailure(s *fixture.RoleMembershipScenario, q string, d fixture.Diagnostic, query bool, stage string) {
	s.T.Helper()
	d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
	step := fixture.ErrorDiagnostic(q, d)
	step.SelectRows = query
	step.ErrorStage = "execute"
	if s.Prepared {
		step.ErrorStage = stage
	}
	s.Run(step)
}

func predefinedDenied(s *fixture.RoleMembershipScenario, q, message string, query bool) {
	s.T.Helper()
	predefinedFailure(s, q, fixture.Diagnostic{Code: "42501", Message: message}, query, "execute")
}

func predefinedRows(s *fixture.RoleMembershipScenario, target string, rows [][]any) {
	s.T.Helper()
	predefinedSelect(s, "SELECT id,v FROM "+target+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, rows)
}

func predefinedDataSetup(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	s.Exec("CREATE SCHEMA pdef_ad")
	s.Exec("CREATE TABLE pdef_ad.t(id INT,v TEXT)")
	s.Run(fixture.Exec("INSERT INTO pdef_ad.t VALUES(1,'a'),(2,'b')", 2))
	s.Exec("CREATE VIEW pdef_ad.v AS SELECT id,v FROM pdef_ad.t")
	// CREATE MATERIALIZED VIEW's SELECT tag reports the population count.
	s.Run(fixture.Exec("CREATE MATERIALIZED VIEW pdef_ad.mv AS SELECT id,v FROM pdef_ad.t", 2))
	s.Exec("CREATE SEQUENCE pdef_ad.s START 10")
}

func predefinedInitialRows() [][]any { return [][]any{{int64(1), "a"}, {int64(2), "b"}} }

func predefinedHeld(s *fixture.RoleMembershipScenario, q string) fixture.HeldStatement {
	s.T.Helper()
	stmt, err := s.C.PrepareContext(s.P.Ctx, q)
	if err != nil {
		s.T.Fatal(err)
	}
	s.T.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			s.T.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: q}
}

func predefinedPhysical(s *fixture.RoleMembershipScenario, name string) (driver.Conn, error) {
	s.T.Helper()
	if s.Backend.PG {
		return fixture.RoleAttributesPhysical(s.T, s.P, s.Backend, name)
	}
	// NewScenario belongs to the prepared subtest, not its backend child.
	dsn := "reference/" + strings.TrimSuffix(s.T.Name(), "/"+s.Backend.Name)
	connector, err := s.P.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, name)
	if err != nil {
		s.T.Fatal(err)
	}
	return connector.Connect(s.P.Ctx)
}
