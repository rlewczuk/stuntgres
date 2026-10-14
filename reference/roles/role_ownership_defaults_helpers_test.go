package reference_test

import (
	"fmt"
	"github.com/lib/pq"
	"strings"
	"stuntgres/reference/fixture"
)

type ownershipDefaultClass struct{ sql, noun, code string }

var ownershipDefaultClasses = []ownershipDefaultClass{{"TABLES", "relations", "r"}, {"SEQUENCES", "sequences", "S"}, {"FUNCTIONS", "functions", "f"}, {"TYPES", "types", "T"}, {"SCHEMAS", "schemas", "n"}, {"LARGE OBJECTS", "large objects", "L"}}

func ownershipDefault(s *membershipScenario, creator, schema, action string) {
	s.t.Helper()
	q := "ALTER DEFAULT PRIVILEGES FOR ROLE " + pq.QuoteIdentifier(creator)
	if schema != "" {
		q += " IN SCHEMA " + pq.QuoteIdentifier(schema)
	}
	s.exec(q + " " + action)
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
func ownershipDefaultTen(s *membershipScenario, creator, schema, recipient string) []string {
	s.t.Helper()
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
func ownershipDefaultSchemas(s *membershipScenario) {
	s.t.Helper()
	for _, schema := range []string{"own_schema", "own_bulk"} {
		s.exec("CREATE SCHEMA " + schema + " AUTHORIZATION own_middle")
	}
	for _, role := range []string{"own_source", "own_target", "own_other", "own_alt", "own_reader", "own_actor", "own_downstream"} {
		s.exec("GRANT own_middle TO " + role + " WITH INHERIT TRUE, SET FALSE")
	}
}
func ownershipDefaultTable(s *membershipScenario, name, creator string) {
	s.t.Helper()
	s.actor(creator)
	if !strings.Contains(name, ".") || strings.HasPrefix(name, "public.") {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, creator, func() { s.exec("CREATE TABLE " + name + "(id INT)") })
	} else {
		s.exec("CREATE TABLE " + name + "(id INT)")
	}
	s.run(fixture.Exec("INSERT INTO "+name+" VALUES(7)", 1))
	s.actor("")
}
func ownershipDefaultDenied(s *membershipScenario, name string) {
	s.t.Helper()
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
func ownershipDefaultProjection(s *membershipScenario, where string, rows [][]any) {
	s.t.Helper()
	dpProjection(s.t, s.p, s.c, s.run, where, rows)
}

// Only the real PG catalog supplement exposes default record OIDs/keys. The
// shared test body proves policies through actual creation, authorization and role dependencies.
func ownershipDefaultIDs(s *membershipScenario, creator string, count int) []int64 {
	s.t.Helper()
	if !s.backend.PG {
		return nil
	}
	rows, err := s.c.QueryContext(s.p.Ctx, "SELECT d.oid::BIGINT FROM pg_default_acl d JOIN pg_roles r ON r.oid=d.defaclrole WHERE r.rolname=$1 ORDER BY d.oid", creator)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil || id <= 0 {
			s.t.Fatal("default identity", id, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil || len(ids) != count {
		s.t.Fatal("default identity count", ids, count, err)
	}
	ownershipBool(s, "SELECT 'pg_default_acl'::regclass::oid=826::oid", true)
	return ids
}
func ownershipDefaultStable(s *membershipScenario, creator string, ids []int64) {
	s.t.Helper()
	if !s.backend.PG {
		return
	}
	for _, id := range ids {
		ownershipCount(s, fmt.Sprintf("SELECT count(*) FROM pg_default_acl d JOIN pg_roles r ON r.oid=d.defaclrole WHERE d.oid=%d AND r.rolname=%s", id, pq.QuoteLiteral(creator)), 1)
	}
}
func ownershipDefaultKeyCount(s *membershipScenario, creator, schema, code string, want int64) {
	s.t.Helper()
	if !s.backend.PG {
		return
	}
	q := "SELECT count(*) FROM pg_default_acl d JOIN pg_roles r ON r.oid=d.defaclrole WHERE r.rolname=" + pq.QuoteLiteral(creator) + " AND d.defaclobjtype=" + pq.QuoteLiteral(code)
	if schema == "" {
		q += " AND d.defaclnamespace=0"
	} else {
		q += " AND d.defaclnamespace=(SELECT oid FROM pg_namespace WHERE nspname=" + pq.QuoteLiteral(schema) + ")"
	}
	ownershipCount(s, q, want)
}
