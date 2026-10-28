package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLogicalDecodingOptions(t *testing.T) {
	logicalDecodingCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, sig := range logicalDecodingSignatures {
			for _, tc := range []struct{ slot, options, code, message string }{
				{"NULL", "NULL::text[]", "22004", "slot name must not be null"},
				{"NULL", "ARRAY[['k',NULL]]::text[]", "22004", "slot name must not be null"},
				{"'ld_absent'", "NULL::text[]", "22004", "options array must not be null"},
				{"'ld_absent'", "ARRAY[['k','v']]::text[]", "0A000", "array must be one-dimensional"},
				{"'ld_absent'", "ARRAY[['k',NULL]]::text[]", "0A000", "array must be one-dimensional"},
				{"'ld_absent'", "ARRAY[NULL]::text[]", "0A000", "array must not contain nulls"},
				{"'ld_absent'", "ARRAY['k',NULL]::text[]", "0A000", "array must not contain nulls"},
				{"'ld_absent'", "ARRAY['k']::text[]", "0A000", "array must have even number of elements"},
			} {
				logicalDecodingFailure(s, "SELECT * FROM " + sig.name + "(" + tc.slot + ",NULL,NULL,VARIADIC " + tc.options + ")", fixture.Diagnostic{Code: tc.code, Message: tc.message})
			}
			for _, options := range []string{"ARRAY[]::text[]", "ARRAY['k','v']", "ARRAY['','']", "ARRAY['unknown','v','unknown','again']", "'[0:1]={k,v}'::text[]", "'[-5:-4]={k,v}'::text[]"} {
				logicalDecodingFailure(s, "SELECT * FROM " + sig.name + "('ld_absent',NULL,NULL,VARIADIC " + options + ")", logicalDecodingMissing("ld_absent"))
			}
			logicalDecodingFailure(s, "SELECT * FROM " + sig.name + "('ld_absent',NULL,NULL,'k')", fixture.Diagnostic{Code: "0A000", Message: "array must have even number of elements"})
			logicalDecodingFailure(s, "SELECT * FROM " + sig.name + "('ld_absent',NULL,NULL,'k','v')", logicalDecodingMissing("ld_absent"))
			q := "SELECT * FROM " + sig.name + "($1::name,NULL,NULL,VARIADIC $2::text[])"
			held := predefinedHeld(s, q)
			logicalDecodingHeldFailure(s, held, fixture.Diagnostic{Code: "22004", Message: "options array must not be null"}, "ld_absent", nil)
			logicalDecodingHeldFailure(s, held, fixture.Diagnostic{Code: "22004", Message: "slot name must not be null"}, nil, nil)
			logicalDecodingHeldFailure(s, held, logicalDecodingMissing("ld_absent"), "ld_absent", "{}")
		}
	})
}

func TestReferenceLogicalDecodingMissingSlots(t *testing.T) {
	logicalDecodingCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, sig := range logicalDecodingSignatures {
			for _, slot := range []string{"ld_absent", "", "UPPER", "a/b", "a\"b", "雪", strings.Repeat("x", 80)} {
				want := slot
				if len(want) > 63 {
					want = want[:63]
				}
				for _, limits := range []string{"NULL,NULL", "'0/0',0", "'FFFFFFFF/FFFFFFFF',-1", "NULL,1", "'0/1',100"} {
					q := "SELECT * FROM " + sig.name + "(" + fileListLiteral(slot) + "," + limits + ")"
					for range 2 {
						logicalDecodingFailure(s, q, logicalDecodingMissing(want))
					}
				}
			}
		}
	})
}
