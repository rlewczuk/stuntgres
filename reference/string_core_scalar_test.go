//go:build reference

package reference_test

import "testing"

func stringCoreScalarNullCases() []stringCoreReferenceCase {
	return []stringCoreReferenceCase{
		{"SELECT '['::text||CASE WHEN false THEN true ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN false ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 42::smallint ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 42::integer ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 42::bigint ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 42::oid ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 1.5::real ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 1.5::double precision ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 12.340::numeric ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 'abc'::name ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '\\x4142'::bytea ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '550e8400-e29b-41d4-a716-446655440000'::uuid ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '2001-02-03'::date ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '04:05:06'::time ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '04:05:06+00'::timetz ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '2001-02-03 04:05:06'::timestamp ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '2001-02-03 04:05:06+00'::timestamptz ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '1 day 02:03:04'::interval ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 'string_core_seed'::regclass ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN setseed(0) ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 'abc'::text ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 'abc '::varchar ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 'abc '::char(5) ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN true ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN false ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 42::smallint ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 42::integer ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 42::bigint ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 42::oid ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 1.5::real ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 1.5::double precision ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 12.340::numeric ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 'abc'::name ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '\\x4142'::bytea ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '550e8400-e29b-41d4-a716-446655440000'::uuid ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '2001-02-03'::date ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '04:05:06'::time ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '04:05:06+00'::timetz ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '2001-02-03 04:05:06'::timestamp ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '2001-02-03 04:05:06+00'::timestamptz ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '1 day 02:03:04'::interval ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 'string_core_seed'::regclass ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN setseed(0) ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 'abc'::text ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 'abc '::varchar ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 'abc '::char(5) ELSE NULL END||']'::text AS v", nil, "TEXT"},
	}
}
func stringCoreReferenceAdditional(t *testing.T, p *referencePair, oracle bool) {
	stringCoreReferenceValues(t, p, stringCoreScalarNullCases(), oracle)
	for _, tc := range []stringCoreReferenceCase{
		{"SELECT 'x'::text||setseed(NULL) AS v", nil, "TEXT"},
		{"SELECT setseed(NULL)||'x'::text AS v", nil, "TEXT"},
		{"SELECT normalize('A̅́') AS v", "A̅́", "TEXT"},
		{"SELECT normalize('̀̕',NFD) AS v", "̀̕", "TEXT"},
	} {
		stringCoreReferenceValues(t, p, []stringCoreReferenceCase{tc}, oracle)
	}
	for _, tc := range []stringCoreDiagnosticCase{
		{"SELECT 'x'::text||setseed(2)", referenceDiagnostic{Code: "22023", Message: "setseed parameter 2 is out of allowed range [-1,1]"}, false},
	} {
		for _, prepared := range []bool{false, true} {
			s := stringCoreDiagnosticStep(tc, prepared)
			if oracle {
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
			} else {
				p.step(t, s)
			}
		}
	}
}
