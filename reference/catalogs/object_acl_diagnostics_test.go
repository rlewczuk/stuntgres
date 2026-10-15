package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectACLDiagnostics(t *testing.T) {
	objectACLScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		type diagnosticCase struct {
			name, query string
			want        fixture.Diagnostic
			runtime     bool
		}
		cases := []diagnosticCase{
			{"arity_zero", "SELECT pg_get_acl()", fixture.Diagnostic{Code: "42883", Message: "function pg_get_acl() does not exist", Hint: hint, Position: "8"}, false},
			{"arity_two", "SELECT pg_get_acl(1259,0)", fixture.Diagnostic{Code: "42883", Message: "function pg_get_acl(integer, integer) does not exist", Hint: hint, Position: "8"}, false},
			{"arity_four", "SELECT pg_get_acl(1259,0,0,0)", fixture.Diagnostic{Code: "42883", Message: "function pg_get_acl(integer, integer, integer, integer) does not exist", Hint: hint, Position: "8"}, false},
			{"typed_text", "SELECT pg_get_acl('1259'::text,0,0)", fixture.Diagnostic{Code: "42883", Message: "function pg_get_acl(text, integer, integer) does not exist", Hint: hint, Position: "8"}, false},
			{"array", "SELECT pg_get_acl(ARRAY[1259],0,0)", fixture.Diagnostic{Code: "42883", Message: "function pg_get_acl(integer[], integer, integer) does not exist", Hint: hint, Position: "8"}, false},
			{"null_wrong_type", "SELECT pg_get_acl(NULL,TRUE,0)", fixture.Diagnostic{Code: "42883", Message: "function pg_get_acl(unknown, boolean, integer) does not exist", Hint: hint, Position: "8"}, false},
			{"wrong_name", "SELECT pg_get_acl(wrong => 1259,objid => 0,objsubid => 0)", fixture.Diagnostic{Code: "42883", Message: "function pg_get_acl(wrong => integer, objid => integer, objsubid => integer) does not exist", Hint: hint, Position: "8"}, false},
			{"quoted_wrong_name", `SELECT pg_get_acl("Classid" => 1259,objid => 0,objsubid => 0)`, fixture.Diagnostic{Code: "42883", Message: "function pg_get_acl(Classid => integer, objid => integer, objsubid => integer) does not exist", Hint: hint, Position: "8"}, false},
			{"quoted_function", `SELECT "PG_GET_ACL"(1259,0,0)`, fixture.Diagnostic{Code: "42883", Message: "function PG_GET_ACL(integer, integer, integer) does not exist", Hint: hint, Position: "8"}, false},
			{"public_function", "SELECT public.pg_get_acl(1259,0,0)", fixture.Diagnostic{Code: "42883", Message: "function public.pg_get_acl(integer, integer, integer) does not exist", Hint: hint, Position: "8"}, false},
			{"multibyte_position", "SELECT /*é*/ pg_get_acl(1259,0)", fixture.Diagnostic{Code: "42883", Message: "function pg_get_acl(integer, integer) does not exist", Hint: hint, Position: "14"}, false},
			{"bad_oid", "SELECT pg_get_acl('bad',0,0)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: "19"}, false},
			{"oid_range", "SELECT pg_get_acl('4294967296',0,0)", fixture.Diagnostic{Code: "22003", Message: `value "4294967296" is out of range for type oid`, Position: "19"}, false},
			{"bad_int4", "SELECT pg_get_acl(1259,0,'bad')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "26"}, false},
			{"int4_range", "SELECT pg_get_acl(1259,0,'2147483648')", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "26"}, false},
			{"distinct", "SELECT pg_get_acl(DISTINCT 1259,0,0)", fixture.Diagnostic{Code: "42809", Message: "DISTINCT specified, but pg_get_acl is not an aggregate function", Position: "8"}, false},
			{"order_by", "SELECT pg_get_acl(1259,0,0 ORDER BY 1)", fixture.Diagnostic{Code: "42809", Message: "ORDER BY specified, but pg_get_acl is not an aggregate function", Position: "8"}, false},
			{"filter", "SELECT pg_get_acl(1259,0,0) FILTER (WHERE TRUE)", fixture.Diagnostic{Code: "42809", Message: "FILTER specified, but pg_get_acl is not an aggregate function", Position: "8"}, false},
			{"unknown_zero_class", "SELECT pg_get_acl(0,1,0)", fixture.Diagnostic{Code: "XX000", Message: "unrecognized class ID: 0"}, true},
			{"unknown_class", "SELECT pg_get_acl(999999,0,0)", fixture.Diagnostic{Code: "XX000", Message: "unrecognized class ID: 999999"}, true},
			{"attribute_class", "SELECT pg_get_acl(1249,0,1)", fixture.Diagnostic{Code: "XX000", Message: "unrecognized class ID: 1249"}, true},
			{"parameter_acl_class", "SELECT pg_get_acl(6243,0,0)", fixture.Diagnostic{Code: "XX000", Message: "unrecognized class ID: 6243"}, true},
		}
		duplicate := "SELECT pg_get_acl(classid => 1259,classid => 0,objsubid => 0)"
		positional := "SELECT pg_get_acl(classid => 1259,0,0)"
		cases = append(cases,
			diagnosticCase{"duplicate_label", duplicate, fixture.Diagnostic{Code: "42601", Message: `argument name "classid" used more than once`, Position: strconv.Itoa(strings.LastIndex(duplicate, "classid") + 1)}, false},
			diagnosticCase{"positional_after_named", positional, fixture.Diagnostic{Code: "42601", Message: "positional argument cannot follow named argument", Position: strconv.Itoa(strings.Index(positional, ",0") + 2)}, false},
		)
		// Competing invalid constants establish coercion order independently
		// of the formal slot order. Positions point at the written literals.
		writtenFirst := "SELECT pg_get_acl(objsubid => 'bad_subid',classid => 'bad_class',objid => 0)"
		cases = append(cases, diagnosticCase{"written_coercion_order", writtenFirst, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad_subid"`, Position: strconv.Itoa(strings.Index(writtenFirst, "'bad_subid'") + 1)}, false})
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, tc.want)
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						if !tc.runtime {
							s.ErrorStage = "prepare"
						}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		// Parameter input errors occur at Bind/Execute on a valid held recipe.
		q := "SELECT pg_get_acl($1,$2,$3) AS acl"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			args []any
			want fixture.Diagnostic
		}{
			{[]any{"bad", int64(0), int64(0)}, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}},
			{[]any{int64(1259), int64(0), "2147483648"}, fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Context: "unnamed portal parameter $3 = '...'"}},
		} {
			s := fixture.ImplicitError(q, tc.want, tc.args...)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"acl"}, []string{"_ACLITEM"}, [][]any{{nil}}, int64(1259), int64(0), int64(0)))
	})
}
