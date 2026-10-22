package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOperatorFamilyReviewCallApplicability(t *testing.T) {
	// REL_18_6 parse_func.c ParseFuncOrColumn resolves known argument types
	// before coercion. Unknown string literals instead reach integer input.
	// These are source-derived full records pending the pinned first run.
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	cases := []struct {
		name, query, code, message, hint, position string
		args []any
	}{
		{"cmp_boolean", `SELECT pg_catalog.btint4cmp(TRUE,1)`, "42883", `function pg_catalog.btint4cmp(boolean, integer) does not exist`, hint, "8", nil},
		{"hash_boolean", `SELECT pg_catalog.hashint4(TRUE)`, "42883", `function pg_catalog.hashint4(boolean) does not exist`, hint, "8", nil},
		{"extended_boolean", `SELECT pg_catalog.hashint4extended(TRUE,0)`, "42883", `function pg_catalog.hashint4extended(boolean, integer) does not exist`, hint, "8", nil},
		{"cmp_text", `SELECT pg_catalog.btint4cmp('1'::text,1)`, "42883", `function pg_catalog.btint4cmp(text, integer) does not exist`, hint, "8", nil},
		{"hash_bigint_narrowing", `SELECT pg_catalog.hashint4(1::bigint)`, "42883", `function pg_catalog.hashint4(bigint) does not exist`, hint, "8", nil},
		{"extended_seed_boolean", `SELECT pg_catalog.hashint4extended(1,TRUE)`, "42883", `function pg_catalog.hashint4extended(integer, boolean) does not exist`, hint, "8", nil},
		{"cmp_parameter_boolean", `SELECT pg_catalog.btint4cmp($1::boolean,1)`, "42883", `function pg_catalog.btint4cmp(boolean, integer) does not exist`, hint, "8", []any{true}},
		{"hash_parameter_boolean", `SELECT pg_catalog.hashint4($1::boolean)`, "42883", `function pg_catalog.hashint4(boolean) does not exist`, hint, "8", []any{true}},
		{"extended_parameter_boolean", `SELECT pg_catalog.hashint4extended(1,$1::boolean)`, "42883", `function pg_catalog.hashint4extended(integer, boolean) does not exist`, hint, "8", []any{true}},
	}
	for _, fn := range []string{"btint4cmp", "hashint4", "hashint4extended"} {
		q := "SELECT pg_catalog."+fn+"('family_review_bad'"
		if fn != "hashint4" { q += ",1" }
		q += ")"
		cases = append(cases, struct{name,query,code,message,hint,position string; args []any}{fn+"_unknown_literal",q,"22P02",`invalid input syntax for type integer: "family_review_bad"`,"",strconv.Itoa(strings.Index(q,"'family_review_bad'")+1),nil})
	}
	q := `SELECT pg_catalog.hashint4extended(1,'family_review_bad')`
	cases = append(cases,struct{name,query,code,message,hint,position string; args []any}{"extended_unknown_seed",q,"22P02",`invalid input syntax for type bigint: "family_review_bad"`,"",strconv.Itoa(strings.Index(q,"'family_review_bad'")+1),nil})
	for _, tc := range cases {
		t.Run(tc.name,func(t *testing.T) {
			familyReviewScenario(t,func(t *testing.T,_ *fixture.Pair,_ fixture.Backend,_ *sql.Conn,run func(fixture.Step)) {
				for _, prepared := range []bool{false,true} {
					s := fixture.ImplicitError(tc.query,fixture.Diagnostic{Code:tc.code,Message:tc.message,Hint:tc.hint,Position:tc.position},tc.args...)
					s.SelectRows = true
					if prepared { s = fixture.Prepared(s); s.ErrorStage = "prepare" } else { s.ErrorStage = "execute" }
					run(s)
					run(fixture.Select(`SELECT 42 AS sentinel`,[]string{"sentinel"},[]string{"INT4"},[][]any{{int64(42)}}))
				}
			})
		})
	}
}

func TestReferenceOperatorFamilyReviewCallInputs(t *testing.T) {
	// Values taken from existing independent pinned support vectors. smallint
	// widens to int4, integer seed widens to int8; no assignment narrowing.
	cases := []struct{name, query string; args []any; columns, types []string; rows [][]any}{
		{"implicit_widening", `SELECT pg_catalog.btint4cmp(1::smallint,2::smallint),pg_catalog.hashint4(1::smallint),pg_catalog.hashint4extended(1::smallint,0::integer)`, nil, []string{"btint4cmp","hashint4","hashint4extended"}, []string{"INT4","INT4","INT8"}, [][]any{{int64(-1),int64(-1905060026),int64(-3670598878359251130)}}},
		{"unknown_literals", `SELECT pg_catalog.btint4cmp('1','2'),pg_catalog.hashint4('1'),pg_catalog.hashint4extended('1','0')`, nil, []string{"btint4cmp","hashint4","hashint4extended"}, []string{"INT4","INT4","INT8"}, [][]any{{int64(-1),int64(-1905060026),int64(-3670598878359251130)}}},
		{"inferred_parameters", `SELECT pg_catalog.btint4cmp($1,$2),pg_catalog.hashint4($1),pg_catalog.hashint4extended($1,$3)`, []any{int64(1),int64(2),int64(0)}, []string{"btint4cmp","hashint4","hashint4extended"}, []string{"INT4","INT4","INT8"}, [][]any{{int64(-1),int64(-1905060026),int64(-3670598878359251130)}}},
		{"widened_parameters", `SELECT pg_catalog.btint4cmp($1::smallint,$2::smallint),pg_catalog.hashint4($1::smallint),pg_catalog.hashint4extended($1::smallint,$3::integer)`, []any{int64(1),int64(2),int64(0)}, []string{"btint4cmp","hashint4","hashint4extended"}, []string{"INT4","INT4","INT8"}, [][]any{{int64(-1),int64(-1905060026),int64(-3670598878359251130)}}},
		{"null_parameters", `SELECT pg_catalog.btint4cmp($1,1),pg_catalog.hashint4($1),pg_catalog.hashint4extended(1,$2)`, []any{nil,nil}, []string{"btint4cmp","hashint4","hashint4extended"}, []string{"INT4","INT4","INT8"}, [][]any{{nil,nil,nil}}},
	}
	for _, tc := range cases {
		t.Run(tc.name,func(t *testing.T) {
			familyReviewScenario(t,func(t *testing.T,_ *fixture.Pair,_ fixture.Backend,_ *sql.Conn,run func(fixture.Step)) {
				for _, prepared := range []bool{false,true} {
					t.Run(fmt.Sprintf("prepared_%t",prepared),func(t *testing.T) {
						s := fixture.Select(tc.query,tc.columns,tc.types,tc.rows,tc.args...)
						if prepared { s = fixture.Prepared(s) }
						run(s)
					})
				}
			})
		})
	}
}
