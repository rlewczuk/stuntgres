package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Predictions grounded in REL_18_6 opclasscmds.c AlterOpFamilyAdd/Drop:
// complete resolution/addFamilyMember precedes store/dropOperators, then
// store/dropProcedures. Not yet runtime observations; pinned discovery first.
func TestReferenceOperatorFamilyReviewListPhases(t *testing.T) {
	const add = `ALTER OPERATOR FAMILY public.family_review_phase USING btree ADD `
	const drop = `ALTER OPERATOR FAMILY public.family_review_phase USING btree DROP `
	const pinned = `ALTER OPERATOR FAMILY pg_catalog.integer_ops USING btree DROP `
	cases := []struct{name, query, code, message string}{
		{"stored_function_list_duplicate", add+`FUNCTION 1 pg_catalog.btint4cmp(integer,integer), FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, "42P17", `function number 1 for (integer,integer) appears more than once`},
		{"stored_operator_list_duplicate", add+`OPERATOR 1 pg_catalog.<(integer,integer), OPERATOR 1 pg_catalog.<(integer,integer)`, "42P17", `operator number 1 for (integer,integer) appears more than once`},
		{"stored_function_later_invalid", add+`FUNCTION 1 pg_catalog.btint4cmp(integer,integer), FUNCTION 7 pg_catalog.btint4cmp(integer,integer)`, "42P17", `invalid function number 7, must be between 1 and 6`},
		{"stored_operator_later_invalid", add+`OPERATOR 1 pg_catalog.<(integer,integer), FUNCTION 7 pg_catalog.btint4cmp(integer,integer)`, "42P17", `invalid function number 7, must be between 1 and 6`},
		{"stored_function_later_signature", add+`FUNCTION 1 pg_catalog.btint4cmp(integer,integer), FUNCTION 2 (integer,integer) pg_catalog.int4mi(integer,integer)`, "42P17", `ordering sort support functions must accept type "internal"`},
		{"stored_operator_later_signature", add+`OPERATOR 1 pg_catalog.<(integer,integer), FUNCTION 1 pg_catalog.int4eq(integer,integer)`, "42P17", `ordering comparison functions must return integer`},
		{"storage_operator_before_function", add+`FUNCTION 1 pg_catalog.btint4cmp(integer,integer), OPERATOR 1 pg_catalog.<(integer,integer)`, "42710", `operator 1(integer,integer) already exists in operator family "public.family_review_phase"`},
		{"absent_drop_later_invalid", drop+`FUNCTION 2(integer), FUNCTION 7(integer)`, "42P17", `invalid function number 7, must be between 1 and 6`},
		{"absent_drop_duplicate", drop+`FUNCTION 2(integer), FUNCTION 2(integer)`, "42P17", `function number 2 for (integer,integer) appears more than once`},
		{"drop_operator_before_function", drop+`FUNCTION 2(integer), OPERATOR 2(integer)`, "42704", `operator 2(integer,integer) does not exist in operator family "public.family_review_phase"`},
		{"pinned_drop_later_invalid", pinned+`FUNCTION 1(integer), FUNCTION 7(integer)`, "42P17", `invalid function number 7, must be between 1 and 6`},
		{"pinned_drop_duplicate", pinned+`FUNCTION 1(integer), FUNCTION 1(integer)`, "42P17", `function number 1 for (integer,integer) appears more than once`},
		{"pinned_operator_later_invalid", pinned+`OPERATOR 1(integer), OPERATOR 6(integer)`, "42P17", `invalid operator number 6, must be between 1 and 5`},
		{"drop_operator_pin_before_function_pin", pinned+`FUNCTION 1(integer), OPERATOR 1(integer)`, "2BP01", `cannot drop operator 1 (integer, integer) of operator family integer_ops for access method btree: <(integer,integer) because it is required by the database system`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			familyReviewScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE OPERATOR FAMILY public.family_review_phase USING btree`,0))
				run(fixture.Exec(add+`OPERATOR 1 pg_catalog.<(integer,integer), FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`,0))
				var operatorID, functionID int64
				if err := conn.QueryRowContext(p.Ctx, `SELECT a.oid::bigint,p.oid::bigint FROM pg_opfamily f JOIN pg_amop a ON a.amopfamily=f.oid JOIN pg_amproc p ON p.amprocfamily=f.oid WHERE f.opfname='family_review_phase' AND f.opfnamespace='public'::regnamespace`).Scan(&operatorID,&functionID); err != nil { t.Fatal(err) }
				for _, prepared := range []bool{false,true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code:tc.code,Message:tc.message})
					if prepared { s = fixture.Prepared(s) }
					run(s)
					run(fixture.Select(`SELECT a.oid::bigint,p.oid::bigint,a.amopstrategy,a.amopopr::bigint,p.amprocnum,p.amproc::oid::bigint FROM pg_opfamily f JOIN pg_amop a ON a.amopfamily=f.oid JOIN pg_amproc p ON p.amprocfamily=f.oid WHERE f.opfname='family_review_phase' AND f.opfnamespace='public'::regnamespace`, []string{"oid","oid","amopstrategy","amopopr","amprocnum","amproc"}, []string{"INT8","INT8","INT2","INT8","INT2","INT8"}, [][]any{{operatorID,functionID,int64(1),int64(97),int64(1),int64(351)}}))
					run(fixture.Select(`SELECT 42 AS sentinel`,[]string{"sentinel"},[]string{"INT4"},[][]any{{int64(42)}}))
				}
			})
		})
	}
}

func TestReferenceOperatorFamilyReviewResolutionOrder(t *testing.T) {
	// DefineOpFamily: creation namespace/CREATE authority precede method.
	// ADD: function lookup precedes processTypesSpec; sort-family lookup
	// precedes assignOperTypes. processTypesSpec resolves first two, then count.
	cases := []struct{name, query, code, message string}{
		{"create_namespace_before_method", `CREATE OPERATOR FAMILY family_review_missing.f USING family_review_missing_method`, "3F000", `schema "family_review_missing" does not exist`},
		{"function_before_association", `ALTER OPERATOR FAMILY public.family_review_resolution USING btree ADD FUNCTION 1 (family_review_missing_type) pg_catalog.family_review_missing_function(integer,integer)`, "42883", `function pg_catalog.family_review_missing_function(integer, integer) does not exist`},
		{"sort_family_before_capability", `ALTER OPERATOR FAMILY public.family_review_resolution USING btree ADD OPERATOR 1 pg_catalog.<(integer,integer) FOR ORDER BY public.family_review_missing_sort`, "42704", `operator family "public.family_review_missing_sort" does not exist for access method "btree"`},
		{"three_existing_associated_types", `ALTER OPERATOR FAMILY public.family_review_resolution USING btree ADD FUNCTION 1 (integer,integer,bigint) pg_catalog.btint4cmp(integer,integer)`, "42601", `one or two argument types must be specified`},
		{"third_type_not_resolved", `ALTER OPERATOR FAMILY public.family_review_resolution USING btree ADD FUNCTION 1 (integer,integer,family_review_missing_third) pg_catalog.btint4cmp(integer,integer)`, "42601", `one or two argument types must be specified`},
		{"first_type_before_count", `ALTER OPERATOR FAMILY public.family_review_resolution USING btree ADD FUNCTION 1 (family_review_missing_first,integer,bigint) pg_catalog.btint4cmp(integer,integer)`, "42704", `type "family_review_missing_first" does not exist`},
		{"second_type_before_count", `ALTER OPERATOR FAMILY public.family_review_resolution USING btree ADD FUNCTION 1 (integer,family_review_missing_second,bigint) pg_catalog.btint4cmp(integer,integer)`, "42704", `type "family_review_missing_second" does not exist`},
	}
	for _, tc := range cases {
		t.Run(tc.name,func(t *testing.T) {
			familyReviewScenario(t,func(t *testing.T,_ *fixture.Pair,_ fixture.Backend,_ *sql.Conn,run func(fixture.Step)) {
				run(fixture.Exec(`CREATE OPERATOR FAMILY public.family_review_resolution USING btree`,0))
				for _, prepared := range []bool{false,true} {
					s := fixture.ImplicitError(tc.query,fixture.Diagnostic{Code:tc.code,Message:tc.message})
					if prepared { s = fixture.Prepared(s) }
					run(s)
					run(fixture.Select(`SELECT count(*) FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_review_resolution' AND f.opfnamespace='public'::regnamespace`,[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
				}
			})
		})
	}
}

func TestReferenceOperatorFamilyReviewCreateAuthorityOrder(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"family_review_creator"})
	fixture.SchemaReferenceCleanup(t,"family_review_denied")
	familyReviewScenario(t,func(t *testing.T,_ *fixture.Pair,_ fixture.Backend,_ *sql.Conn,run func(fixture.Step)) {
		run(fixture.Exec(`CREATE ROLE family_review_creator`,0))
		run(fixture.Exec(`CREATE SCHEMA family_review_denied`,0))
		run(fixture.Exec(`GRANT USAGE ON SCHEMA family_review_denied TO family_review_creator`,0))
		run(fixture.Exec(`SET ROLE family_review_creator`,0))
		for _, prepared := range []bool{false,true} {
			s := fixture.ImplicitError(`CREATE OPERATOR FAMILY family_review_denied.f USING family_review_missing_method`,fixture.Diagnostic{Code:"42501",Message:`permission denied for schema family_review_denied`})
			if prepared { s = fixture.Prepared(s) }
			run(s)
		}
		run(fixture.Exec(`RESET ROLE`,0))
	})
}
