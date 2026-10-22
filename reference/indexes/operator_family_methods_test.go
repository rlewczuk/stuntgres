package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type familyDiagnosticCase struct {
	name, sql, code, message, hint string
}

// Complete diagnostics observed on pinned PostgreSQL 18.6 UTF8/C.
var familyDiagnosticCases = []familyDiagnosticCase{
	{"duplicate_family", `CREATE OPERATOR FAMILY public.family_probe USING btree`, "42710", `operator family "family_probe" for access method "btree" already exists`, ""},
	{"missing_method", `CREATE OPERATOR FAMILY public.family_absent USING family_missing`, "42704", `access method "family_missing" does not exist`, ""},
	{"missing_family", `ALTER OPERATOR FAMILY public.family_absent USING btree ADD OPERATOR 1 pg_catalog.<(integer,integer)`, "42704", `operator family "public.family_absent" does not exist for access method "btree"`, ""},
	{"btree_strategy", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 6 pg_catalog.<(integer,integer)`, "42P17", `invalid operator number 6, must be between 1 and 5`, ""},
	{"btree_support", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 7 pg_catalog.btint4cmp(integer,integer)`, "42P17", `invalid function number 7, must be between 1 and 6`, ""},
	{"hash_support", `ALTER OPERATOR FAMILY public.family_probe USING hash ADD FUNCTION 4 pg_catalog.hashint4(integer)`, "42P17", `invalid function number 4, must be between 1 and 3`, ""},
	{"gist_support", `ALTER OPERATOR FAMILY public.family_probe USING gist ADD FUNCTION 13 (integer,integer) pg_catalog.int4mi(integer,integer)`, "42P17", `invalid function number 13, must be between 1 and 12`, ""},
	{"hash_strategy", `ALTER OPERATOR FAMILY public.family_probe USING hash ADD OPERATOR 2 pg_catalog.=(integer,integer)`, "42P17", `invalid operator number 2, must be between 1 and 1`, ""},
	{"no_operator_signature", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 1 pg_catalog.<`, "42601", `operator argument types must be specified in ALTER OPERATOR FAMILY`, ""},
	{"unary", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 1 pg_catalog.-(NONE,integer)`, "42P17", `index operators must be binary`, ""},
	{"nonboolean", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 1 pg_catalog.+(integer,integer)`, "42P17", `index search operators must return boolean`, ""},
	{"compare_arity", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 1 pg_catalog.abs(integer)`, "42P17", `ordering comparison functions must have two arguments`, ""},
	{"compare_result", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 1 pg_catalog.int4eq(integer,integer)`, "42P17", `ordering comparison functions must return integer`, ""},
	{"hash1_arity", `ALTER OPERATOR FAMILY public.family_probe USING hash ADD FUNCTION 1 pg_catalog.int4mi(integer,integer)`, "42P17", `hash function 1 must have one argument`, ""},
	{"hash1_result", `ALTER OPERATOR FAMILY public.family_probe USING hash ADD FUNCTION 1 pg_catalog.int8abs(bigint)`, "42P17", `hash function 1 must return integer`, ""},
	{"hash2_arity", `ALTER OPERATOR FAMILY public.family_probe USING hash ADD FUNCTION 2 pg_catalog.int8abs(bigint)`, "42P17", `hash function 2 must have two arguments`, ""},
	{"hash2_result", `ALTER OPERATOR FAMILY public.family_probe USING hash ADD FUNCTION 2 pg_catalog.int4mi(integer,integer)`, "42P17", `hash function 2 must return bigint`, ""},
	{"generic_association", `ALTER OPERATOR FAMILY public.family_probe USING gist ADD FUNCTION 1 pg_catalog.int4mi(integer,integer)`, "42P17", `associated data types must be specified for index support function`, ""},
	{"sort_association", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 2 pg_catalog.btint4sortsupport(internal)`, "42P17", `associated data types must be specified for index support function`, ""},
	{"options", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 5 (integer,integer) pg_catalog.int4mi(integer,integer)`, "42P17", `invalid operator class options parsing function`, `Valid signature of operator class options parsing function is (internal) RETURNS void.`},
	{"duplicate_list", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer), FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, "42P17", `function number 1 for (integer,integer) appears more than once`, ""},
	{"missing_operator", `ALTER OPERATOR FAMILY public.family_probe USING btree DROP OPERATOR 1(integer)`, "42704", `operator 1(integer,integer) does not exist in operator family "public.family_probe"`, ""},
	{"missing_function", `ALTER OPERATOR FAMILY public.family_probe USING btree DROP FUNCTION 1(integer)`, "42704", `function 1(integer,integer) does not exist in operator family "public.family_probe"`, ""},
	{"ordering_method", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 1 pg_catalog.<(integer,integer) FOR ORDER BY pg_catalog.integer_ops`, "42P17", `access method "btree" does not support ordering operators`, ""},
	{"unknown_function", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 1 pg_catalog.family_unknown(integer,integer)`, "42883", `function pg_catalog.family_unknown(integer, integer) does not exist`, ""},
	{"ambiguous_function", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 1 pg_catalog.abs`, "42725", `function name "pg_catalog.abs" is not unique`, `Specify the argument list to select the function unambiguously.`},
	{"unknown_operator", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 1 public.===(integer,integer)`, "42883", `operator does not exist: integer public.=== integer`, ""},
	{"zero_strategy", `ALTER OPERATOR FAMILY public.family_probe USING gist ADD OPERATOR 0 pg_catalog.=(integer,integer)`, "42P17", `invalid operator number 0, must be between 1 and 32767`, ""},
	{"gist_strategy_overflow", `ALTER OPERATOR FAMILY public.family_probe USING gist ADD OPERATOR 32768 pg_catalog.=(integer,integer)`, "42P17", `invalid operator number 32768, must be between 1 and 32767`, ""},
	{"cross_options", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 5 (integer,bigint) pg_catalog.btint4sortsupport(internal)`, "42P17", `left and right associated data types for operator class options parsing functions must match`, ""},
	{"cross_skip", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 6 (integer,bigint) pg_catalog.btint4sortsupport(internal)`, "42P17", `btree skip support functions must not be cross-type`, ""},
	{"cross_equalimage", `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 4 (integer,bigint) pg_catalog.boolin(cstring)`, "42P17", `ordering equal image functions must not be cross-type`, ""},
}

func TestReferenceOperatorFamilyDiagnostics(t *testing.T) {
	for _, tc := range familyDiagnosticCases {
		t.Run(tc.name, func(t *testing.T) {
			familyScenario(t, func(t *testing.T, _ context.Context, _ *sql.Conn, run func(fixture.Step)) {
				for _, method := range []string{"btree", "hash", "gist"} {
					run(fixture.Exec("CREATE OPERATOR FAMILY public.family_probe USING "+method, 0))
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.sql, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint})
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select(`SELECT 42 AS sentinel`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
				}
			})
		})
	}
}

func TestReferenceOperatorFamilyMethods(t *testing.T) {
	familyScenario(t, func(t *testing.T, _ context.Context, _ *sql.Conn, run func(fixture.Step)) {
		for _, method := range []string{"btree", "hash", "gist"} {
			run(fixture.Exec("CREATE OPERATOR FAMILY public.family_probe USING "+method, 0))
		}
		btree := []string{
			`FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`,
			`FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`,
			`FUNCTION 3 pg_catalog.in_range(integer,integer,bigint,boolean,boolean)`,
			`FUNCTION 4 (integer,integer) pg_catalog.boolin(cstring)`,
			`FUNCTION 5 (integer,integer) pg_catalog.btint4sortsupport(internal)`,
			`FUNCTION 6 (integer,integer) pg_catalog.btint4sortsupport(internal)`,
		}
		for _, member := range btree {
			run(fixture.Exec("ALTER OPERATOR FAMILY public.family_probe USING btree ADD "+member, 0))
		}
		// Slot3 infers its pair from the first and third formal, not first two.
		run(fixture.Select(`SELECT p.amprocnum,p.amproclefttype,p.amprocrighttype FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_probe' AND f.opfmethod=403 ORDER BY p.amprocnum`, []string{"amprocnum", "amproclefttype", "amprocrighttype"}, []string{"INT2", "OID", "OID"}, [][]any{
			{int64(1), int64(23), int64(23)}, {int64(2), int64(23), int64(23)}, {int64(3), int64(23), int64(20)}, {int64(4), int64(23), int64(23)}, {int64(5), int64(23), int64(23)}, {int64(6), int64(23), int64(23)},
		}))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING hash ADD FUNCTION 1 pg_catalog.hashint4(integer), FUNCTION 2 (integer,integer) pg_catalog.int8pl(bigint,bigint), FUNCTION 3 (integer,integer) pg_catalog.btint4sortsupport(internal)`, 0))
		for slot := 1; slot <= 12; slot++ {
			fn := `pg_catalog.int4mi(integer,integer)`
			if slot == 10 {
				fn = `pg_catalog.btint4sortsupport(internal)`
			}
			run(fixture.Exec(fmt.Sprintf(`ALTER OPERATOR FAMILY public.family_probe USING gist ADD FUNCTION %d (integer,integer) %s`, slot, fn), 0))
		}
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING gist ADD OPERATOR 32767 pg_catalog.=(integer,integer)`, 0))
		// The sort family is resolved under btree, not under the target GiST
		// method; this exact ADD succeeded in the pinned discovery round.
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING gist ADD OPERATOR 1 pg_catalog.<(integer,integer) FOR ORDER BY public.family_probe`, 0))
		run(fixture.Select(`SELECT a.amoppurpose,a.amopsortfamily=f.oid AS matching_sort_family FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopsortfamily WHERE a.amopmethod=783 AND a.amopstrategy=1 AND f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace AND f.opfmethod=403`, []string{"amoppurpose", "matching_sort_family"}, []string{"CHAR", "BOOL"}, [][]any{{"o", true}}))
		run(fixture.Select(`SELECT f.opfmethod,count(*) FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_probe' GROUP BY f.opfmethod ORDER BY f.opfmethod`, []string{"opfmethod", "count"}, []string{"OID", "INT8"}, [][]any{{int64(403), int64(6)}, {int64(405), int64(3)}, {int64(783), int64(12)}}))
		// Explicit association deliberately differs from real function formals.
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 1 (smallint,bigint) pg_catalog.btint4cmp(integer,integer)`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree DROP FUNCTION 1(smallint,bigint)`, 0))
	})
}
