package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarraySetDiagnostics(t *testing.T) {
	intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		intarrayInstall(t, p, b, c)
		for _, tc := range intarraySetCases {
			for _, named := range []bool{false, true} {
				for _, args := range [][]any{{nil, tc.right}, {"{3,1,3,2}", nil}, {nil, nil}, {"{NULL}", nil}} {
					q := "SELECT " + tc.expression("$1::integer[]", "$2::"+tc.rightType, named) + " AS v"
					intarrayBoth(t, p, b, c, intarraySetResult(b, q, tc.family, nil, args...))
					q = "SELECT " + tc.expression(intarraySetLiteral(args[0], "integer[]"), intarraySetLiteral(args[1], tc.rightType), named) + " AS v"
					intarrayBoth(t, p, b, c, intarraySetResult(b, q, tc.family, nil))
				}
				invalid := [][]any{{"{NULL}", tc.right}, {"{1,NULL,2}", tc.right}}
				if tc.rightType == "integer[]" {
					invalid = append(invalid, []any{"{}", "{NULL}"}, []any{"{NULL}", "{}"}, []any{"{}", "{1,NULL,2}"}, []any{"{1,NULL,2}", "{}"}, []any{"{3}", "{NULL}"})
					q := "SELECT " + tc.expression("NULL::integer[]", "'{NULL}'::integer[]", named) + " AS v"
					intarrayBoth(t, p, b, c, intarraySetResult(b, q, tc.family, nil))
				}
				for _, args := range invalid {
					literal := "SELECT " + tc.expression(intarraySetLiteral(args[0], "integer[]"), intarraySetLiteral(args[1], tc.rightType), named) + " AS v"
					parameter := "SELECT " + tc.expression("$1::integer[]", "$2::"+tc.rightType, named) + " AS v"
					for _, prepared := range []bool{false, true} {
						intarrayError(t, p, b, c, literal, prepared, "execute", intarraySetNullError())
						intarrayError(t, p, b, c, parameter, prepared, "execute", intarraySetNullError(), args...)
					}
				}
			}
		}
		for _, tc := range []struct{ q, signature string }{
			{"SELECT 1 OPERATOR(ia_provider.+) '{2}'::integer[]", "integer ia_provider.+ integer[]"},
			{"SELECT 1 OPERATOR(ia_provider.-) '{2}'::integer[]", "integer ia_provider.- integer[]"},
			{"SELECT 1 OPERATOR(ia_provider.|) '{2}'::integer[]", "integer ia_provider.| integer[]"},
			{"SELECT '{1}'::integer[] OPERATOR(ia_provider.&) 1", "integer[] ia_provider.& integer"},
			{"SELECT '{a}'::text[] OPERATOR(ia_provider.&&) '{a}'::text[]", "text[] ia_provider.&& text[]"},
			{"SELECT '{1}'::integer[] OPERATOR(ia_provider.+) 'x'::text", "integer[] ia_provider.+ text"},
		} {
			want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.signature)
			want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			want.Position = strconv.Itoa(strings.Index(tc.q, "OPERATOR") + 1)
			for _, prepared := range []bool{false, true} {
				stage := "execute"
				if prepared {
					stage = "prepare"
				}
				intarrayError(t, p, b, c, tc.q, prepared, stage, want)
			}
		}
		for _, tc := range []struct{ q, signature string }{
			{"SELECT ia_provider.intarray_push_elem(1,'{2}'::integer[])", "ia_provider.intarray_push_elem(integer, integer[])"},
			{"SELECT ia_provider._int_inter('{1}'::integer[],2)", "ia_provider._int_inter(integer[], integer)"},
			{"SELECT ia_provider._int_union('{a}'::text[],'{a}'::text[])", "ia_provider._int_union(text[], text[])"},
		} {
			want := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			want.Position = "8"
			for _, prepared := range []bool{false, true} {
				stage := "execute"
				if prepared {
					stage = "prepare"
				}
				intarrayError(t, p, b, c, tc.q, prepared, stage, want)
			}
		}
		for _, tc := range []struct{ symbol, result string }{
			{"+", "{1,1,2,2}"},
			{"-", "{1}"},
			{"|", "{1,2}"},
		} {
			ambiguous := "SELECT '{1}' OPERATOR(ia_provider." + tc.symbol + ") '{2}'"
			want := fixture.ControlsReferenceDiagnostic("42725", "operator is not unique: unknown ia_provider."+tc.symbol+" unknown")
			want.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
			want.Position = strconv.Itoa(strings.Index(ambiguous, "OPERATOR") + 1)
			for _, prepared := range []bool{false, true} {
				stage := "execute"
				if prepared {
					stage = "prepare"
				}
				intarrayError(t, p, b, c, ambiguous, prepared, stage, want)
			}
			// A known array selects the array overload before unknown-category
			// ranking, regardless of the right literal's contents.
			query := "SELECT '{1,1}'::integer[] OPERATOR(ia_provider." + tc.symbol + ") '{2,2}' AS v"
			intarrayBoth(t, p, b, c, intarraySetResult(b, query, "_INT4", tc.result))
		}
		for _, prepared := range []bool{false, true} {
			want := fixture.ControlsReferenceDiagnostic("22003", `value "2147483648" is out of range for type integer`)
			want.Position = "8"
			stage := "execute"
			if prepared {
				stage = "prepare"
			}
			intarrayError(t, p, b, c, "SELECT '{2147483648}'::integer[] OPERATOR(ia_provider.|) '{}'::integer[]", prepared, stage, want)
			want.Position, want.Context = "", "unnamed portal parameter $1 = '...'"
			intarrayError(t, p, b, c, "SELECT $1::integer[] OPERATOR(ia_provider.|) '{}'::integer[]", prepared, "execute", want, "{2147483648}")
			malformed := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "x"`)
			malformed.Position = "8"
			intarrayError(t, p, b, c, "SELECT '{x}'::integer[] OPERATOR(ia_provider.|) '{}'::integer[]", prepared, stage, malformed)
			malformed.Position, malformed.Context = "", "unnamed portal parameter $1 = '...'"
			intarrayError(t, p, b, c, "SELECT $1::integer[] OPERATOR(ia_provider.|) '{}'::integer[]", prepared, "execute", malformed, "{x}")
		}
		want := fixture.ControlsReferenceDiagnostic("42846", `cannot cast type unknown to internal`)
		want.Position = "41"
		for _, prepared := range []bool{false, true} {
			stage := "execute"
			if prepared {
				stage = "prepare"
			}
			intarrayError(t, p, b, c, "SELECT ia_provider._int_overlap_sel(NULL::internal,0::oid,NULL::internal,0)", prepared, stage, want)
		}
	})
}
