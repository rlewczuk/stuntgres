package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePredefinedSafeCatalog(t *testing.T) {
	predefinedCases(t, []string{"pdef_observer"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		// Released fixed identities and direct attributes, not values discovered
		// from the native role root or inherited capability graph.
		columns := []string{"rolname", "rolsuper", "rolinherit", "rolcreaterole", "rolcreatedb", "rolcanlogin", "rolreplication", "rolconnlimit", "rolpassword", "rolvaliduntil", "rolbypassrls", "rolconfig", "oid"}
		types := []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT4", "TEXT", "TIMESTAMPTZ", "BOOL", "_TEXT", "OID"}
		query := "SELECT rolname,rolsuper,rolinherit,rolcreaterole,rolcreatedb,rolcanlogin,rolreplication,rolconnlimit,rolpassword,rolvaliduntil,rolbypassrls,rolconfig,oid FROM pg_roles WHERE rolname=$1"
		// The ordinary observer has no predefined membership or catalog grant.
		for _, actor := range []string{"", "pdef_observer"} {
			s.Actor(actor)
			for _, role := range predefinedRoles {
				row := []any{role.name, false, true, false, false, false, false, int64(-1), "********", nil, false, nil, role.oid}
				step := fixture.Select(query, columns, types, [][]any{row}, role.name)
				step.Prepared = s.Prepared
				backend := s.Backend
				backend.Recorder = s.Record
				fixture.PgDescriptionExecutorRows(s.T, s.P, backend, s.C, step)
			}
		}
		s.Actor("")
	})
}
