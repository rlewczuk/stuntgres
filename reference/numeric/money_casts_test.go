package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMoneyCastsAndCatalog(t *testing.T) {
	moneyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			expression, typ string
			value           any
		}{
			{"'92233720368547758.07'::money::numeric", "NUMERIC", "92233720368547758.07"},
			{"'-92233720368547758.08'::money::numeric", "NUMERIC", "-92233720368547758.08"},
			{"1.235::numeric::money", "MONEY", "$1.24"}, {"(-1.235)::numeric::money", "MONEY", "-$1.24"}, {"1.2300::numeric::money", "MONEY", "$1.23"},
			{"123::integer::money", "MONEY", "$123.00"}, {"123::bigint::money", "MONEY", "$123.00"},
			{"1.235::float8::numeric::money", "MONEY", "$1.24"}, {"NULL::money::numeric", "NUMERIC", nil}, {"NULL::numeric::money", "MONEY", nil},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := moneyReferenceValue(tc.expression, tc.typ, tc.value)
					s.Prepared = prepared
					moneyReferenceRun(t, p, b, s)
				}
			})
		}
		for _, tc := range []struct{ expression, code, message string }{
			{"92233720368547759::bigint::money", "22003", "bigint out of range"},
			{"92233720368547758.075::numeric::money", "22003", "bigint out of range"},
			{"(-92233720368547758.085)::numeric::money", "22003", "bigint out of range"},
			{"'NaN'::numeric::money", "0A000", "cannot convert NaN to bigint"}, {"'Infinity'::numeric::money", "0A000", "cannot convert infinity to bigint"}, {"'-Infinity'::numeric::money", "0A000", "cannot convert infinity to bigint"},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := moneyReferenceFailure("SELECT "+tc.expression, tc.code, tc.message, "", "", "execute")
					s.Prepared = prepared
					moneyReferenceRun(t, p, b, s)
				}
			})
		}
		for _, tc := range []struct{ expression, from, to, position string }{
			{"123::smallint::money", "smallint", "money", "21"},
			{"1::float8::money", "double precision", "money", "17"}, {"1::float4::money", "real", "money", "17"},
			{"'1'::money::integer", "money", "integer", "18"}, {"'1'::money::bigint", "money", "bigint", "18"},
			{"'1'::money::float8", "money", "double precision", "18"}, {"'1'::money::float4", "money", "real", "18"},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					q := "SELECT " + tc.expression
					stage := "execute"
					if prepared {
						stage = "prepare"
					}
					s := moneyReferenceFailure(q, "42846", "cannot cast type "+tc.from+" to "+tc.to, tc.position, "", stage)
					s.Prepared = prepared
					moneyReferenceRun(t, p, b, s)
				}
			})
		}
		facts := [][]any{{int64(20), int64(790), int64(3812), "a", "f"}, {int64(23), int64(790), int64(3811), "a", "f"}, {int64(790), int64(1700), int64(3823), "a", "f"}, {int64(1700), int64(790), int64(3824), "a", "f"}}
		fixture.PgCastCheckFacts(t, p, b, b.DB, facts, false)
		moneyReferenceRun(t, p, b, fixture.Select("SELECT castsource,casttarget FROM pg_catalog.pg_cast WHERE castsource=21 AND casttarget=790", []string{"castsource", "casttarget"}, []string{"OID", "OID"}, nil), fixture.Select("SELECT obj_description(790,'pg_type') AS description", []string{"description"}, []string{"TEXT"}, [][]any{{"monetary amounts, $d,ddd.cc"}}))
		// This independently observed inventory is PG-only: native does not
		// implement the adjacent pg_opclass virtual relation. Native provider
		// identity and shared functional index behavior are covered separately.
		if b.PG {
			fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid,opcfamily,opcintype,opcmethod::text,opcname,opcdefault FROM pg_catalog.pg_opclass WHERE opcname='money_ops'",
				[]string{"oid", "opcfamily", "opcintype", "opcmethod", "opcname", "opcdefault"},
				[]string{"OID", "OID", "OID", "TEXT", "NAME", "BOOL"},
				[][]any{{int64(10047), int64(2099), int64(790), "403", "money_ops", true}}))
		}
	})
}
