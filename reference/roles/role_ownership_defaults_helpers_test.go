package reference_test

import (
	"fmt"
	"github.com/lib/pq"
	"strings"
	"stuntgres/reference/fixture"
)

type ownershipDefaultClass struct{ sql, noun, code string }

var ownershipDefaultClasses = []ownershipDefaultClass{{"TABLES", "relations", "r"}, {"SEQUENCES", "sequences", "S"}, {"FUNCTIONS", "functions", "f"}, {"TYPES", "types", "T"}, {"SCHEMAS", "schemas", "n"}, {"LARGE OBJECTS", "large objects", "L"}}

func ownershipDefault(s *fixture.RoleMembershipScenario, creator, schema, action string) {
	s.T.Helper()
	q := "ALTER DEFAULT PRIVILEGES FOR ROLE " + pq.QuoteIdentifier(creator)
	if schema != "" {
		q += " IN SCHEMA " + pq.QuoteIdentifier(schema)
	}
	s.Exec(q + " " + action)
}
func ownershipDefaultLine(creator, schema, noun string, owner bool) string {
	prefix := "privileges for "
	if owner {
		prefix = "owner of "
	}
	text := prefix + "default privileges on new " + noun + " belonging to role " + creator
	if schema != "" {
		text += " in schema " + schema
	}
	return text
}
func ownershipDefaultTen(s *fixture.RoleMembershipScenario, creator, schema, recipient string) []string {
	s.T.Helper()
	var lines []string
	for _, c := range ownershipDefaultClasses {
		if c.code == "f" {
			ownershipDefault(s, creator, "", "REVOKE ALL ON FUNCTIONS FROM "+creator+",PUBLIC")
		} else {
			ownershipDefault(s, creator, "", "GRANT ALL ON "+c.sql+" TO "+recipient+" WITH GRANT OPTION")
		}
		lines = append(lines, ownershipDefaultLine(creator, "", c.noun, true))
	}
	// FUNCTIONS and ROUTINES address the same global key, including its stored empty ACL.
	ownershipDefault(s, creator, "", "REVOKE EXECUTE ON ROUTINES FROM PUBLIC")
	for _, c := range ownershipDefaultClasses[:4] {
		ownershipDefault(s, creator, schema, "GRANT ALL ON "+c.sql+" TO "+recipient+" WITH GRANT OPTION")
		lines = append(lines, ownershipDefaultLine(creator, schema, c.noun, true))
	}
	return lines
}
func ownershipDefaultSchemas(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	for _, schema := range []string{"own_schema", "own_bulk"} {
		s.Exec("CREATE SCHEMA " + schema + " AUTHORIZATION own_middle")
	}
	for _, role := range []string{"own_source", "own_target", "own_other", "own_alt", "own_reader", "own_actor", "own_downstream"} {
		s.Exec("GRANT own_middle TO " + role + " WITH INHERIT TRUE, SET FALSE")
	}
}
func ownershipDefaultTable(s *fixture.RoleMembershipScenario, name, creator string) {
	s.T.Helper()
	s.Actor(creator)
	if !strings.Contains(name, ".") || strings.HasPrefix(name, "public.") {
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, creator, func() { s.Exec("CREATE TABLE " + name + "(id INT)") })
	} else {
		s.Exec("CREATE TABLE " + name + "(id INT)")
	}
	s.Run(fixture.Exec("INSERT INTO "+name+" VALUES(7)", 1))
	s.Actor("")
}
func ownershipDefaultDenied(s *fixture.RoleMembershipScenario, name string) {
	s.T.Helper()
	// Qualified lookup has real namespace authority; the table diagnostic uses its local name.
	local := name
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			local = name[i+1:]
			break
		}
	}
	ownershipFailure(s, "SELECT id FROM "+name, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + local})
}
func ownershipDefaultProjection(s *fixture.RoleMembershipScenario, where string, rows [][]any) {
	s.T.Helper()
	dpProjection(s.T, s.P, s.C, s.Run, where, rows)
}

func ownershipDefaultIDs(s *fixture.RoleMembershipScenario, creator string, count int) []int64 {
	s.T.Helper()
	rows, err := s.C.QueryContext(s.P.Ctx, "SELECT d.oid::BIGINT FROM pg_default_acl d JOIN pg_roles r ON r.oid=d.defaclrole WHERE r.rolname=$1 ORDER BY d.oid", creator)
	if err != nil {
		s.T.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil || id <= 0 || id > 4294967295 {
			s.T.Fatal("default identity", id, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil || len(ids) != count {
		s.T.Fatal("default identity count", ids, count, err)
	}
	ownershipBool(s, "SELECT 'pg_default_acl'::regclass::oid=826::oid", true)
	return ids
}
func ownershipDefaultStable(s *fixture.RoleMembershipScenario, creator string, ids []int64) {
	s.T.Helper()
	for _, id := range ids {
		ownershipCount(s, fmt.Sprintf("SELECT count(*) FROM pg_default_acl d JOIN pg_roles r ON r.oid=d.defaclrole WHERE d.oid=%d AND r.rolname=%s", id, pq.QuoteLiteral(creator)), 1)
	}
}
func ownershipDefaultKeyCount(s *fixture.RoleMembershipScenario, creator, schema, code string, want int64) {
	s.T.Helper()
	q := "SELECT count(*) FROM pg_default_acl d JOIN pg_roles r ON r.oid=d.defaclrole WHERE r.rolname=" + pq.QuoteLiteral(creator) + " AND d.defaclobjtype=" + pq.QuoteLiteral(code)
	if schema == "" {
		q += " AND d.defaclnamespace=0"
	} else {
		q += " AND d.defaclnamespace=(SELECT oid FROM pg_namespace WHERE nspname=" + pq.QuoteLiteral(schema) + ")"
	}
	ownershipCount(s, q, want)
}
