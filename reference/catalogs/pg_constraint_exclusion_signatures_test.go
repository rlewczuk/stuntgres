package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// These released equality identities belong to the selected exclusion operator,
// not necessarily to the declared column type: CIDR uses INET, OID aliases use OID.
func TestReferencePGConstraintExclusionSignatures(t *testing.T) {
	for _, tc := range []struct {
		name, typ, operators, declaration string
	}{
		{"cidr", "CIDR", "{1201}", ""},
		{"inet", "INET", "{1201}", ""},
		{"oid", "OID", "{607}", ""},
		{"regclass", "REGCLASS", "{607}", ""},
		{"regtype", "REGTYPE", "{607}", ""},
		{"regproc", "REGPROC", "{607}", ""},
		{"regprocedure", "REGPROCEDURE", "{607}", ""},
		{"regrole", "REGROLE", "{607}", ""},
		{"regoper", "REGOPER", "{607}", ""},
		{"regoperator", "REGOPERATOR", "{607}", ""},
		{"regcollation", "REGCOLLATION", "{607}", ""},
		{"regnamespace", "REGNAMESPACE", "{607}", ""},
		{"domain_cidr", "pc_exsig_domain_cidr_type", "{1201}", "CREATE DOMAIN pc_exsig_domain_cidr_type AS CIDR"},
		{"domain_oid", "pc_exsig_domain_oid_type", "{607}", "CREATE DOMAIN pc_exsig_domain_oid_type AS REGOPERATOR"},
		{"varchar", "VARCHAR(20)", "{98}", ""},
		{"enum", "pc_exsig_enum_type", "{3516}", "CREATE TYPE pc_exsig_enum_type AS ENUM ('first','second')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					prefix := "pc_exsig_" + tc.name
					ordinary := prefix + "_ordinary"
					checkName := prefix + "_ck"
					excluded := prefix + "_excluded"
					exclusionName := prefix + "_x"
					fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE "+ordinary+"(a INT,CONSTRAINT "+checkName+" CHECK(a>0))")
					unrelatedQuery := "SELECT conname FROM pg_constraint WHERE conrelid=$1::regclass::oid AND conname=$2"
					unrelated := fixture.Select(unrelatedQuery, []string{"conname"}, []string{"NAME"}, [][]any{{checkName}}, ordinary, checkName)
					heldUnrelated := fixture.ViewHold(t, p, b, unrelatedQuery)
					pgConstraintCheck(t, p, b, heldUnrelated, unrelated)

					if tc.declaration != "" {
						fixture.PGClassExec(t, p, b, b.DB, tc.declaration)
					}
					fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE "+excluded+"(a "+tc.typ+",CONSTRAINT "+exclusionName+" EXCLUDE USING btree(a WITH =))")
					q := "SELECT conname,contype,conkey::text,conexclop::text,conindid<>0::oid AS backed,conindid<>oid AS distinct_identity,conbin IS NULL AS no_tree FROM pg_constraint WHERE conrelid=$1::regclass::oid ORDER BY conname"
					metadata := fixture.Select(q, []string{"conname", "contype", "conkey", "conexclop", "backed", "distinct_identity", "no_tree"}, []string{"NAME", "CHAR", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{exclusionName, "x", "{1}", tc.operators, true, true, true}}, excluded)
					pgConstraintCheck(t, p, b, b.DB, metadata)
					pgConstraintCheck(t, p, b, b.DB, fixture.Prepared(metadata))
					heldMetadata := fixture.ViewHold(t, p, b, q)
					for range 2 {
						pgConstraintCheck(t, p, b, heldMetadata, metadata)
					}

					// Capture opens the complete source before applying this predicate.
					// An admitted exclusion must not poison unrelated catalog discovery,
					// including a statement prepared before the exclusion existed.
					pgConstraintCheck(t, p, b, b.DB, unrelated)
					pgConstraintCheck(t, p, b, b.DB, fixture.Prepared(unrelated))
					pgConstraintCheck(t, p, b, heldUnrelated, unrelated)
				})
			}
		})
	}
}
