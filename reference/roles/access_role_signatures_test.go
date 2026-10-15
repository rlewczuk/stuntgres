package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessRoleSignatures(t *testing.T) {
	accessRoleCases(t, []string{"ar_actor", "ar_target"}, func(s *membershipScenario) {
		s.exec("GRANT ar_target TO ar_actor")
		actor, target := accessRoleOID(s, "ar_actor"), accessRoleOID(s, "ar_target")
		s.actor("ar_actor")
		for _, tc := range []struct {
			q    string
			args []any
		}{
			{"SELECT pg_has_role($1::name,$2::name,'MEMBER') AS v", []any{"ar_actor", "ar_target"}},
			{"SELECT pg_has_role($1::name,$2::oid,'MEMBER') AS v", []any{"ar_actor", target}},
			{"SELECT pg_has_role($1::oid,$2::name,'MEMBER') AS v", []any{actor, "ar_target"}},
			{"SELECT pg_has_role($1::oid,$2::oid,'MEMBER') AS v", []any{actor, target}},
			{"SELECT pg_has_role($1::name,'MEMBER') AS v", []any{"ar_target"}},
			{"SELECT pg_has_role($1::oid,'MEMBER') AS v", []any{target}},
			{"SELECT pg_catalog.pg_has_role('ar_target','MEMBER') AS v", nil},
			{"SELECT pg_has_role($1,$2,$3) AS v", []any{"ar_actor", "ar_target", "MEMBER"}},
			{"SELECT pg_has_role($1,$2) AS v", []any{"ar_target", "USAGE"}},
			{"SELECT pg_has_role('ar_actor'::text,'ar_target'::varchar,'SET') AS v", nil},
			{"SELECT pg_has_role('ar_actor'::regrole,'ar_target'::regrole,'MEMBER') AS v", nil},
		} {
			accessRoleBool(s, tc.q, true, tc.args...)
		}
		for _, typ := range []string{"smallint", "integer", "bigint", "oid"} {
			// Use fixed predefined OIDs representable by every integer width.
			accessRoleBool(s, fmt.Sprintf("SELECT pg_has_role(6171::%s,6171::%s,'MEMBER') AS v", typ, typ), true)
		}
		s.actor("")
		s.exec("CREATE DOMAIN ar_name AS name")
		s.exec("CREATE DOMAIN ar_oid AS oid")
		accessRoleBool(s, "SELECT pg_has_role('ar_actor'::ar_name,$1::ar_oid,'MEMBER') AS v", true, target)
		s.exec("CREATE TABLE ar_sig_t(id INT)")
		for _, q := range []string{"SELECT row_security_active('ar_sig_t'::text) AS v", "SELECT row_security_active('ar_sig_t'::regclass) AS v", "SELECT pg_catalog.row_security_active('ar_sig_t') AS v", "SELECT row_security_active($1) AS v"} {
			var args []any
			if q == "SELECT row_security_active($1) AS v" {
				args = []any{"ar_sig_t"}
			}
			accessRoleBool(s, q, false, args...)
		}
		accessRoleBool(s, "SELECT row_security_active(0::ar_oid) AS v", false)
		s.run(fixture.Exec("SELECT pg_has_role('ar_actor','ar_target','MEMBER')", 1))
		s.run(fixture.Exec("SELECT row_security_active('ar_sig_t')", 1))
		s.exec("DROP DOMAIN ar_name")
		s.exec("DROP DOMAIN ar_oid")
	})
}

func TestReferenceAccessRoleNullAndMetadata(t *testing.T) {
	accessRoleCases(t, []string{"ar_actor"}, func(s *membershipScenario) {
		for _, q := range []string{
			"SELECT pg_has_role(NULL::name,'missing','bad') AS v",
			"SELECT pg_has_role('missing',NULL::name,'bad') AS v",
			"SELECT pg_has_role('missing','missing',NULL::text) AS v",
			"SELECT pg_has_role(NULL::oid,0::oid,'bad') AS v",
			"SELECT pg_has_role(0::oid,NULL::oid,'bad') AS v",
			"SELECT pg_has_role(NULL::name,'bad') AS v",
			"SELECT pg_has_role(NULL::oid,'bad') AS v",
			"SELECT pg_has_role('missing',NULL::text) AS v",
			"SELECT row_security_active(NULL::text) AS v",
			"SELECT row_security_active(NULL::oid) AS v",
			"SELECT pg_has_role(NULL,'bad') AS v",
		} {
			accessRoleBool(s, q, nil)
		}
		accessRoleSelect(s, "SELECT pg_has_role(NULL::name,'bad') AS v WHERE false", []string{"v"}, nil)
		accessRoleSelect(s, "SELECT row_security_active(NULL::oid) AS v WHERE false", []string{"v"}, nil)
		accessRoleSelect(s, "SELECT pg_has_role('ar_actor','MEMBER'),row_security_active(0::oid)", []string{"pg_has_role", "row_security_active"}, [][]any{{true, false}})
		s.exec("CREATE TABLE ar_inputs(role_name name,rel oid)")
		s.run(fixture.Exec("INSERT INTO ar_inputs VALUES(NULL,NULL),('ar_actor',0)", 2))
		accessRoleSelect(s, "SELECT pg_has_role(role_name,'MEMBER') AS m,row_security_active(rel) AS r FROM ar_inputs ORDER BY role_name NULLS FIRST", []string{"m", "r"}, [][]any{{nil, nil}, {true, false}})
		// Strict provider lookup is bypassed, not argument analysis.
		predefinedFailure(s, "SELECT pg_has_role(NULL::name,missing_column,'bad')", fixture.Diagnostic{Code: "42703", Message: `column "missing_column" does not exist`, Position: "31"}, true, "prepare")
		// STRICT bypasses provider lookup, not evaluation of this erroring sibling.
		accessRoleRuntimeError(s, "SELECT pg_has_role(NULL::name,(1/0)::oid,'bad') AS v", "22012", "division by zero")
	})
}
