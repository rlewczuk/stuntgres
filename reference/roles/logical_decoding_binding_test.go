package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceLogicalDecodingSignatures(t *testing.T) {
	logicalDecodingCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, sig := range logicalDecodingSignatures {
			logicalDecodingIdentity(s, sig)
			q := "SELECT proargtypes::text,proargnames::text,proargmodes::text,proallargtypes::text,pg_get_expr(proargdefaults,0) AS defaults FROM pg_proc WHERE oid=" + strconv.FormatInt(sig.oid, 10)
			predefinedSelect(s, q, []string{"proargtypes", "proargnames", "proargmodes", "proallargtypes", "defaults"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"19 3220 23 1009", "{slot_name,upto_lsn,upto_nchanges,options,lsn,xid,data}", "{i,i,i,v,o,o,o}", "{19,3220,23,1009,3220,28," + sig.dataOID + "}", "'{}'::text[]"}})
			for _, args := range []string{
				"'ld_absent',NULL,NULL",
				"'ld_absent',NULL,NULL,'k','v'",
				"'ld_absent',NULL,NULL,VARIADIC ARRAY[]::text[]",
				"slot_name => 'ld_absent',upto_lsn => NULL,upto_nchanges => NULL,VARIADIC options => ARRAY[]::text[]",
				"upto_nchanges => NULL,slot_name => 'ld_absent',upto_lsn => NULL,VARIADIC options => ARRAY[]::text[]",
				"'ld_absent',upto_nchanges => NULL,upto_lsn => NULL,VARIADIC options => ARRAY[]::text[]",
				"'ld_absent',NULL,NULL,VARIADIC options => ARRAY['k','v']::text[]",
			} {
				logicalDecodingFailure(s, "SELECT * FROM " + sig.name + "(" + args + ")", logicalDecodingMissing("ld_absent"))
			}
			// Pinned 18.6 tests-1: named notation does not expand omitted
			// variadic options, unlike the positional three-argument default.
			for _, tc := range []struct{ args, types string }{
				{"slot_name => 'ld_absent',upto_lsn => NULL,upto_nchanges => NULL", "slot_name => unknown, upto_lsn => unknown, upto_nchanges => unknown"},
				{"upto_nchanges => NULL,slot_name => 'ld_absent',upto_lsn => NULL", "upto_nchanges => unknown, slot_name => unknown, upto_lsn => unknown"},
				{"'ld_absent',upto_nchanges => NULL,upto_lsn => NULL", "unknown, upto_nchanges => unknown, upto_lsn => unknown"},
			} {
				q := "SELECT * FROM " + sig.name + "(" + tc.args + ")"
				predefinedFailure(s, q, fixture.Diagnostic{Code: "42883", Message: "function " + sig.name + "(" + tc.types + ") does not exist", Hint: fileListSignatureHint, Position: "15"}, true, "prepare")
			}
		}
	})
}

func TestReferenceLogicalDecodingMetadata(t *testing.T) {
	logicalDecodingCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, sig := range logicalDecodingSignatures {
			columns, types := []string{"lsn", "xid", "data"}, []string{"PG_LSN", "XID", sig.data}
			q := "SELECT * FROM " + sig.name + "('ld_absent',NULL,NULL) LIMIT 0"
			predefinedSelect(s, q, columns, types, nil)
			predefinedSelect(s, "SELECT * FROM pg_catalog." + strings.ToUpper(sig.name) + "('ld_absent',NULL,NULL) LIMIT 0", columns, types, nil)
			predefinedSelect(s, "SELECT d.* FROM (VALUES('ld_absent'::name)) AS v(slot) CROSS JOIN LATERAL " + sig.name + "(v.slot,NULL,NULL) AS d LIMIT 0", columns, types, nil)
			predefinedSelect(s, "SELECT * FROM ROWS FROM (" + sig.name + "('ld_absent',NULL,NULL),generate_series(1,1)) AS d(a,b,c,n) LIMIT 0", []string{"a","b","c","n"}, []string{"PG_LSN","XID",sig.data,"INT4"}, nil)
			held := predefinedHeld(s, q)
			for range 2 {
				fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Select(q, columns, types, nil))
			}
			predefinedSelect(s, "SELECT * FROM " + sig.name + "('ld_absent',NULL,NULL) WITH ORDINALITY AS x(a,b,c,n) LIMIT 0", []string{"a", "b", "c", "n"}, []string{"PG_LSN", "XID", sig.data, "INT8"}, nil)
			predefinedSelect(s, "SELECT " + sig.name + "('ld_absent',NULL,NULL) LIMIT 0", []string{sig.name}, []string{"RECORD"}, nil)
			s.Run(fixture.Select("SELECT * FROM " + sig.name + "($1::name,$2::pg_lsn,$3::integer) LIMIT 0", columns, types, nil, "ld_absent", nil, nil))
		}
	})
}

func TestReferenceLogicalDecodingBindingDiagnostics(t *testing.T) {
	logicalDecodingCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, sig := range logicalDecodingSignatures {
			for _, tc := range []struct{ args, types string }{
				{"", ""}, {"'x'", "unknown"}, {"'x',NULL", "unknown, unknown"},
				{"1::integer,NULL,NULL", "integer, unknown, unknown"},
				{"'x',true,NULL", "unknown, boolean, unknown"},
				{"'x',NULL,true", "unknown, unknown, boolean"},
				{"'x',NULL,NULL,ARRAY['k','v']::text[]", "unknown, unknown, unknown, text[]"},
				{"'x',NULL,NULL,true", "unknown, unknown, unknown, boolean"},
			} {
				q := "SELECT * FROM " + sig.name + "(" + tc.args + ")"
				predefinedFailure(s, q, fixture.Diagnostic{Code: "42883", Message: "function " + sig.name + "(" + tc.types + ") does not exist", Hint: fileListSignatureHint, Position: "15"}, true, "prepare")
			}
			for _, prefix := range []string{"SELECT * FROM ", "SELECT /* λ雪 */ * FROM "} {
				q := prefix + sig.name + "('x','bad_lsn',NULL)"
				position := utf8.RuneCountInString(q[:strings.Index(q, "'bad_lsn'")]) + 1
				predefinedFailure(s, q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type pg_lsn: "bad_lsn"`, Position: fmt.Sprint(position)}, true, "prepare")
			}
			for _, tc := range []struct{ args, code, message, token string }{
				{"'x',NULL,'bad_int'", "22P02", `invalid input syntax for type integer: "bad_int"`, "'bad_int'"},
				{"'x',NULL,NULL,VARIADIC 'bad_array'::text[]", "22P02", `malformed array literal: "bad_array"`, "'bad_array'"},
			} {
				q := "SELECT * FROM " + sig.name + "(" + tc.args + ")"
				d := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fmt.Sprint(strings.Index(q, tc.token) + 1)}
				if tc.token == "'bad_array'" {
					d.Detail = `Array value must start with "{" or dimension information.`
				}
				predefinedFailure(s, q, d, true, "prepare")
			}
			q := "SELECT * FROM " + sig.name + "(slot_name => 'x',slot_name => 'y',upto_lsn => NULL,upto_nchanges => NULL)"
			predefinedFailure(s, q, fixture.Diagnostic{Code: "42601", Message: `argument name "slot_name" used more than once`, Position: fmt.Sprint(strings.Index(q, "slot_name => 'y'") + 1)}, true, "prepare")
			q = "SELECT * FROM " + sig.name + "(slot_name => 'x',NULL,NULL)"
			predefinedFailure(s, q, fixture.Diagnostic{Code: "42601", Message: "positional argument cannot follow named argument", Position: fmt.Sprint(strings.Index(q, "NULL") + 1)}, true, "prepare")
			q = "SELECT * FROM " + sig.name + "(no_such_argument => 'x',upto_lsn => NULL,upto_nchanges => NULL)"
			predefinedFailure(s, q, fixture.Diagnostic{Code: "42883", Message: "function " + sig.name + "(no_such_argument => unknown, upto_lsn => unknown, upto_nchanges => unknown) does not exist", Hint: fileListSignatureHint, Position: "15"}, true, "prepare")
		}
	})
}
